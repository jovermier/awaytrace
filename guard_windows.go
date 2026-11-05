package main

import (
	"fmt"
	"image"
	"image/draw"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
var lastInputProc = user32.NewProc("GetLastInputInfo")
var cursorProc = user32.NewProc("GetCursorInfo")
var ticksProc = kernel32.NewProc("GetTickCount")
var dpiProc = user32.NewProc("SetProcessDpiAwarenessContext")
var wtsRegister = wtsapi32.NewProc("WTSRegisterSessionNotification")
var wtsUnregister = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
var getIdle = idleDuration
var captureDesktop = screenshot.Capture
var drawTimestamp = addClock
var applyStartup = setStartup
var desktopBounds = func() (int, int, int, int) {
	return int(win.GetSystemMetrics(76)), int(win.GetSystemMetrics(77)), int(win.GetSystemMetrics(78)), int(win.GetSystemMetrics(79))
}

type lastInput struct {
	Size uint32
	Tick uint32
}
type cursorInfo struct {
	Size   uint32
	Flags  uint32
	Cursor uintptr
	X, Y   int32
}

func idleDuration() time.Duration {
	v := lastInput{Size: uint32(unsafe.Sizeof(lastInput{}))}
	ok, _, _ := lastInputProc.Call(uintptr(unsafe.Pointer(&v)))
	if ok == 0 {
		return 0
	}
	now, _, _ := ticksProc.Call()
	return time.Duration(uint32(now)-v.Tick) * time.Millisecond
}
func currentCursor() (int, int, bool) {
	c := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	ok, _, _ := cursorProc.Call(uintptr(unsafe.Pointer(&c)))
	return int(c.X), int(c.Y), ok != 0 && c.Flags == 1
}
func setStartup(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err = key.SetStringValue("AwayTrace", `"`+exe+`"`); err != nil {
			return err
		}
	} else {
		err = key.DeleteValue("AwayTrace")
		if err != nil && err != registry.ErrNotExist {
			return err
		}
	}
	old, _, err := key.GetStringValue("ScreenGuard")
	if err == nil && strings.EqualFold(strings.Trim(strings.TrimSpace(old), `"`), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "ScreenGuard", "ScreenGuard.exe")) {
		_ = key.DeleteValue("ScreenGuard")
	}
	return nil
}
func freeSpace(folder string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(folder)
	if err != nil {
		return 0, err
	}
	var n uint64
	err = windows.GetDiskFreeSpaceEx(p, &n, nil, nil)
	return n, err
}

type Guard struct {
	mu                                              sync.Mutex
	config                                          Config
	configPath, dataDir, ffmpeg                     string
	enabled, locked, failed, sessionError, shutting bool
	status                                          string
	deadline, nextFrame                             time.Time
	rec                                             *Recorder
	idleGap                                         bool
	done                                            chan struct{}
	stopOnce                                        sync.Once
}

func newGuard(c Config, configPath, dataDir, ffmpeg string, stopped bool) *Guard {
	return &Guard{config: c, configPath: configPath, dataDir: dataDir, ffmpeg: ffmpeg, enabled: !stopped, deadline: time.Now().Add(time.Duration(c.Grace) * time.Second), done: make(chan struct{})}
}
func (g *Guard) stopLocked(timeout time.Duration) {
	if g.rec != nil {
		r := g.rec
		g.rec = nil
		g.idleGap = false
		if err := r.stop(timeout); err != nil {
			log.Printf("recording stopped with error: %v", err)
		}
		log.Printf("recording stopped: %s", r.path)
	}
}
func (g *Guard) toggle() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = !g.enabled
	if g.enabled {
		g.deadline = time.Now().Add(time.Duration(g.config.Grace) * time.Second)
	} else {
		g.stopLocked(5 * time.Second)
	}
}
func (g *Guard) lockSession() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.locked = true
	g.stopLocked(5 * time.Second)
}
func (g *Guard) unlockSession() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.locked = false
	g.enabled = true
	g.failed = false
	g.deadline = time.Now().Add(time.Duration(g.config.Grace) * time.Second)
}
func (g *Guard) shutdownQuery() bool {
	finished := make(chan struct{})
	go func() { g.mu.Lock(); g.shutting = true; g.stopLocked(time.Second); g.mu.Unlock(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
	}
	return true
}
func (g *Guard) cancelShutdown() {
	go func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.shutting = false
		g.deadline = time.Now().Add(time.Duration(g.config.Grace) * time.Second)
	}()
}
func (g *Guard) quit() {
	g.stopOnce.Do(func() { close(g.done) })
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopLocked(5 * time.Second)
}
func (g *Guard) snapshot() (Config, bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.config, g.enabled, g.status
}
func (g *Guard) updateConfig(c Config) error {
	if err := c.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(c.Folder, 0700); err != nil {
		return err
	}
	if err := applyStartup(c.Startup); err != nil {
		return err
	}
	if err := saveConfig(g.configPath, c); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.config = c
	g.deadline = time.Now().Add(time.Duration(c.Grace) * time.Second)
	return nil
}
func (g *Guard) run() {
	timer := time.NewTicker(50 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-timer.C:
			g.tick()
		}
	}
}
func (g *Guard) tick() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rec != nil {
		select {
		case err := <-g.rec.done:
			g.fail(fmt.Errorf("encoder exited unexpectedly: %v", err))
		default:
		}
	}
	switch {
	case g.shutting:
		g.status = "Finishing session"
		return
	case g.sessionError:
		g.stopLocked(5 * time.Second)
		g.status = "Error: lock detection unavailable"
		return
	case g.locked:
		g.stopLocked(5 * time.Second)
		g.status = "Locked"
		return
	case !g.enabled:
		g.status = "Capture stopped"
		return
	case time.Now().Before(g.deadline):
		g.status = "Grace period"
		return
	case getIdle() >= time.Duration(g.config.Idle)*time.Second:
		if g.rec != nil && !g.rec.lastFrame.IsZero() {
			g.idleGap = true
		}
		g.status = "Idle; waiting for activity"
		return
	case g.failed:
		g.status = "Recorder error; waiting for unlock"
		return
	}
	if g.rec == nil {
		if err := os.MkdirAll(g.config.Folder, 0700); err != nil {
			g.fail(err)
			return
		}
		n, err := freeSpace(g.config.Folder)
		if err != nil || n < 500*1024*1024 {
			g.fail(fmt.Errorf("recording storage unavailable or less than 500 MB free: %v", err))
			return
		}
		_, _, w, h := desktopBounds()
		if w <= 0 || h <= 0 {
			g.fail(fmt.Errorf("no display"))
			return
		}
		g.rec, err = startRecorder(g.config, g.ffmpeg, g.dataDir, w, h)
		if err != nil {
			g.fail(err)
			return
		}
		g.nextFrame = time.Time{}
	}
	if time.Now().Before(g.nextFrame) {
		g.status = "Recording"
		return
	}
	g.nextFrame = time.Now().Add(time.Second / time.Duration(g.rec.fps))
	if g.idleGap && !g.rec.lastFrame.IsZero() {
		if g.rec.showIdleMarker {
			marker := gapFrame(g.rec.width, g.rec.height, time.Since(g.rec.lastFrame))
			for i := 0; i < max(1, int(float64(g.rec.fps)*0.7+0.5)); i++ {
				if err := g.rec.write(marker); err != nil {
					g.fail(err)
					return
				}
			}
		}
		g.idleGap = false
	}
	x, y, w, h := desktopBounds()
	img, err := captureDesktop(x, y, w, h)
	if err != nil {
		g.fail(err)
		return
	}
	if img.Bounds().Dx() != g.rec.width || img.Bounds().Dy() != g.rec.height {
		scaled := image.NewRGBA(image.Rect(0, 0, g.rec.width, g.rec.height))
		xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Src, nil)
		img = scaled
	}
	if cx, cy, ok := currentCursor(); ok {
		addCursor(img, (cx-x)*g.rec.width/w, (cy-y)*g.rec.height/h)
	}
	if g.rec.showTimestamp {
		drawTimestamp(img, time.Now(), g.rec.timeFormat)
	}
	if err = g.rec.write(img); err != nil {
		g.fail(err)
		return
	}
	g.status = "Recording"
}
func (g *Guard) fail(err error) {
	log.Printf("recording error: %v", err)
	g.stopLocked(5 * time.Second)
	g.failed = true
	g.status = "Recorder error; waiting for unlock"
}
func launchFolder(path string) error { return exec.Command("explorer.exe", path).Start() }
