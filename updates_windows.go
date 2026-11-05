package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

type stagedUpdate struct {
	path, helper, target, digest, version string
	installed, manual                     bool
}

func prepareUpdateHelper(updatePath, digest, version, dataDir string) (stagedUpdate, error) {
	var s stagedUpdate
	exe, err := os.Executable()
	if err != nil {
		return s, err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return s, err
	}
	f, err := os.Open(exe)
	if err != nil {
		return s, err
	}
	defer f.Close()
	out, err := os.CreateTemp(filepath.Join(dataDir, "updates"), "awaytrace-helper-*.exe")
	if err != nil {
		return s, err
	}
	name := out.Name()
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	if _, err = io.Copy(out, f); err != nil {
		return s, err
	}
	if err = out.Sync(); err != nil {
		return s, err
	}
	if err = out.Close(); err != nil {
		return s, err
	}
	ok = true
	return stagedUpdate{path: updatePath, helper: name, target: exe, digest: digest, version: version, installed: installedExecutable(exe)}, nil
}
func launchUpdateHelper(s stagedUpdate, enabled bool) error {
	args := []string{"--apply-update", s.path, s.target, strconv.Itoa(os.Getpid()), s.digest, strconv.FormatBool(s.installed), strconv.FormatBool(enabled)}
	cmd := exec.Command(s.helper, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Start()
}

// The helper runs only after the regular process has decided that no recording
// is active. Waiting for it to exit releases the installer mutex and image.
func runUpdateHelper(args []string) error {
	if len(args) != 6 {
		return fmt.Errorf("invalid update helper arguments")
	}
	updatePath, target := args[0], args[1]
	pid, err := strconv.Atoi(args[2])
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid parent process")
	}
	digest := strings.TrimPrefix(strings.ToLower(args[3]), "sha256:")
	if len(digest) != 64 {
		return fmt.Errorf("invalid update digest")
	}
	installed, err := strconv.ParseBool(args[4])
	if err != nil {
		return err
	}
	enabled, err := strconv.ParseBool(args[5])
	if err != nil {
		return err
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == nil {
		status, waitErr := windows.WaitForSingleObject(h, 30000)
		windows.CloseHandle(h)
		if waitErr != nil || status != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("application did not exit")
		}
	} else if err != windows.ERROR_INVALID_PARAMETER {
		return fmt.Errorf("could not wait for application: %w", err)
	}
	if fileHash(updatePath) != digest {
		_ = restartAfterUpdate(target, enabled)
		return fmt.Errorf("update integrity check failed")
	}
	if installed {
		if !installedExecutable(target) {
			_ = restartAfterUpdate(target, enabled)
			return fmt.Errorf("unexpected installer target")
		}
		cmd := exec.Command(updatePath, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if err = cmd.Run(); err != nil {
			_ = restartAfterUpdate(target, enabled)
			return fmt.Errorf("installer failed: %w", err)
		}
	} else {
		if err = replaceStandalone(updatePath, target); err != nil {
			_ = restartAfterUpdate(target, enabled)
			return err
		}
	}
	return restartAfterUpdate(target, enabled)
}
func replaceStandalone(updatePath, target string) error {
	src, err := os.Open(updatePath)
	if err != nil {
		return err
	}
	defer src.Close()
	staged, err := os.CreateTemp(filepath.Dir(target), ".awaytrace-update-*.exe")
	if err != nil {
		return err
	}
	stagedName := staged.Name()
	defer os.Remove(stagedName)
	if _, err = io.Copy(staged, src); err != nil {
		staged.Close()
		return err
	}
	if err = staged.Sync(); err != nil {
		staged.Close()
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	backup := target + ".awaytrace-backup"
	if _, err := os.Stat(backup); err == nil {
		return fmt.Errorf("previous update backup exists")
	}
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(stagedName, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("update failed: %v; restore failed: %w", err, restoreErr)
		}
		return err
	}
	_ = os.Remove(backup)
	return nil
}
func restartAfterUpdate(target string, enabled bool) error {
	args := []string{}
	if !enabled {
		args = append(args, "--start-stopped")
	}
	cmd := exec.Command(target, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Start()
}
func cleanUpdateFiles(dataDir string) {
	dir := filepath.Join(dataDir, "updates")
	for _, pattern := range []string{"awaytrace-update-*.exe", "awaytrace-helper-*.exe"} {
		files, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, path := range files {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("update cache cleanup: %v", err)
			}
		}
	}
}
func logUpdateHelperError(err error) {
	if err == nil {
		return
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "AwayTrace")
	_ = os.MkdirAll(dir, 0700)
	f, e := os.OpenFile(filepath.Join(dir, "awaytrace.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e == nil {
		fmt.Fprintf(f, "%s update helper: %v\n", time.Now().Format(time.RFC3339), err)
		f.Close()
	}
}
