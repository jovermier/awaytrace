package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lxn/walk"
)

type updateManager struct {
	guard        *Guard
	window       *walk.MainWindow
	mu           sync.Mutex
	checking     bool
	pending      *stagedUpdate
	available    string
	settingsOpen bool
}

var launchStagedUpdate = launchUpdateHelper
var newUpdateClient = updateHTTPClient

func (u *updateManager) message(text string) {
	u.window.Synchronize(func() { walk.MsgBox(u.window, "AwayTrace updates", text, walk.MsgBoxIconInformation) })
}
func (u *updateManager) check(manual bool) {
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		if manual {
			u.message("An update check is already in progress.")
		}
		return
	}
	if u.pending != nil {
		u.mu.Unlock()
		if manual {
			u.message("An update is ready and will be installed after recording stops.")
		}
		return
	}
	u.checking = true
	u.mu.Unlock()
	go func() {
		defer func() { u.mu.Lock(); u.checking = false; u.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		release, err := getLatestRelease(ctx, newUpdateClient(), latestReleaseURL)
		cancel()
		if err != nil {
			log.Printf("update check: %v", err)
			if manual {
				u.message("Could not check for updates: " + err.Error())
			}
			return
		}
		if release.Draft || release.Prerelease {
			u.updateError(manual, fmt.Errorf("latest release is not stable"))
			return
		}
		newer, err := isNewerVersion(release.Tag, appVersion)
		if err != nil {
			log.Printf("update version: %v", err)
			if manual {
				u.message("The latest release has an unsupported version.")
			}
			return
		}
		if !newer {
			if manual {
				u.message("AwayTrace is up to date (" + appVersion + ").")
			}
			return
		}
		u.mu.Lock()
		u.available = release.Tag
		u.mu.Unlock()
		cfg, _, _ := u.guard.snapshot()
		install := cfg.AutoUpdate
		if manual {
			response := make(chan bool, 1)
			u.window.Synchronize(func() {
				response <- walk.MsgBox(u.window, "AwayTrace updates", fmt.Sprintf("AwayTrace %s is available. Download it and install when recording is stopped?", release.Tag), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) == walk.DlgCmdYes
			})
			install = <-response
		}
		if !install {
			log.Printf("AwayTrace %s is available", release.Tag)
			return
		}
		exe, err := os.Executable()
		if err != nil {
			u.updateError(manual, err)
			return
		}
		name := "AwayTrace.exe"
		if installedExecutable(exe) {
			name = "AwayTrace-Setup.exe"
		}
		asset, err := release.findAsset(name)
		if err != nil {
			u.updateError(manual, err)
			return
		}
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Minute)
		path, err := downloadVerified(ctx, newUpdateClient(), asset, filepath.Join(u.guard.dataDir, "updates"))
		cancel()
		if err != nil {
			u.updateError(manual, err)
			return
		}
		staged, err := prepareUpdateHelper(path, asset.Digest, release.Tag, u.guard.dataDir)
		if err != nil {
			_ = os.Remove(path)
			u.updateError(manual, err)
			return
		}
		staged.manual = manual
		u.mu.Lock()
		u.pending = &staged
		u.mu.Unlock()
		if manual {
			u.message("Update downloaded and verified. It will install when recording is stopped.")
		}
	}()
}
func (u *updateManager) updateError(manual bool, err error) {
	log.Printf("update error: %v", err)
	if manual {
		u.message("Could not prepare the update: " + err.Error())
	}
}
func (u *updateManager) schedule() {
	select {
	case <-u.guard.done:
		return
	case <-time.After(10 * time.Second):
	}
	var last time.Time
	previousAuto := false
	for {
		cfg, _, _ := u.guard.snapshot()
		if cfg.AutoUpdate && !previousAuto {
			last = time.Time{}
		}
		previousAuto = cfg.AutoUpdate
		if cfg.CheckUpdates && (last.IsZero() || time.Since(last) >= 24*time.Hour) {
			u.check(false)
			last = time.Now()
		} else if !cfg.CheckUpdates {
			last = time.Time{}
		}
		select {
		case <-u.guard.done:
			return
		case <-time.After(time.Minute):
		}
	}
}

// Called on the UI thread. It never interrupts a live recording or starts an
// installation while Windows is locked or shutting down.
func (u *updateManager) applyIfReady() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pending == nil || u.settingsOpen {
		return false
	}
	g := u.guard
	if !g.mu.TryLock() {
		return false
	}
	defer g.mu.Unlock()
	if !u.pending.manual && !g.config.AutoUpdate {
		_ = os.Remove(u.pending.path)
		_ = os.Remove(u.pending.helper)
		u.pending = nil
		return false
	}
	if g.rec != nil || g.locked || g.shutting {
		return false
	}
	g.shutting = true
	if err := launchStagedUpdate(*u.pending, g.enabled); err != nil {
		g.shutting = false
		log.Printf("update launch failed: %v", err)
		_ = os.Remove(u.pending.path)
		_ = os.Remove(u.pending.helper)
		u.pending = nil
		return false
	}
	u.pending = nil
	return true
}
