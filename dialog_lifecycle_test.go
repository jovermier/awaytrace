package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// Tray dialogs must not make the hidden main window visible. Close the
// settings dialog with Cancel or the title-bar close button when prompted.
func TestManualSettingsKeepsOwnerHidden(t *testing.T) {
	if os.Getenv("AWAYTRACE_MANUAL_OWNER") != "1" {
		t.Skip("manual dialog lifecycle check")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mw, err := walk.NewMainWindow()
	if err != nil {
		t.Fatal(err)
	}
	defer mw.Dispose()
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpegForTest(t), true)
	openTraySettings(g)
	if mw.Handle() == 0 {
		t.Fatal("closing settings destroyed the tray window")
	}
	if win.IsWindowVisible(mw.Handle()) {
		t.Fatal("closing settings made the hidden tray owner visible")
	}
}

// This exercises the real tray application's window and stopped guard while
// a person closes the isolated settings dialog. It never records the desktop.
func TestManualTraySurvivesSettings(t *testing.T) {
	if os.Getenv("AWAYTRACE_MANUAL_TRAY") != "1" {
		t.Skip("manual tray lifecycle check")
	}
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	c.CheckUpdates = false
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpegForTest(t), true)
	visible := false
	completed := false
	onUIReady = func(h win.HWND) {
		openTraySettings(g)
		visible = win.IsWindowVisible(h)
		completed = true
		win.PostMessage(h, win.WM_CLOSE, 0, 0)
	}
	defer func() { onUIReady = nil }()
	if err := runUI(g); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("settings dialog did not return")
	}
	if visible {
		t.Fatal("closing settings made the tray window visible")
	}
}
