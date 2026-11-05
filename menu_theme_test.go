package main

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// An isolated native popup; set AWAYTRACE_MANUAL_MENU to dark or light, open
// it, inspect its colors and select Settings. It does not touch the tray app.
func TestManualNativeMenuTheme(t *testing.T) {
	mode := os.Getenv("AWAYTRACE_MANUAL_MENU")
	if mode != "dark" && mode != "light" {
		t.Skip("manual menu check")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	old := wantsDarkTheme
	wantsDarkTheme = func() bool { return mode == "dark" }
	defer func() { wantsDarkTheme = old }()
	theme := newTrayMenuTheme()
	defer theme.dispose()
	theme.refresh()
	menu := win.CreatePopupMenu()
	if menu == 0 {
		t.Fatal("creating menu failed")
	}
	defer win.DestroyMenu(menu)
	for i, label := range []string{"Settings...", "Status: Capture stopped", "Quit"} {
		state := uint32(win.MFS_ENABLED)
		if i == 1 {
			state = win.MFS_DISABLED
		}
		item := win.MENUITEMINFO{CbSize: uint32(unsafe.Sizeof(win.MENUITEMINFO{})), FMask: win.MIIM_STRING | win.MIIM_ID | win.MIIM_STATE, WID: uint32(100 + i), FState: state, DwTypeData: syscall.StringToUTF16Ptr(label)}
		if !win.InsertMenuItem(menu, uint32(i), true, &item) {
			t.Fatal("adding menu item failed")
		}
	}
	var dlg *walk.Dialog
	var button *walk.PushButton
	selected := uintptr(0)
	spec := Dialog{AssignTo: &dlg, Title: "AwayTrace menu test", MinSize: Size{Width: 300, Height: 120}, Layout: VBox{}, Children: []Widget{
		PushButton{AssignTo: &button, Text: "Show menu", OnClicked: func() {
			var r win.RECT
			win.GetWindowRect(button.Handle(), &r)
			win.SetForegroundWindow(dlg.Handle())
			selected = uintptr(win.TrackPopupMenuEx(menu, win.TPM_NOANIMATION|win.TPM_RETURNCMD, r.Left, r.Bottom, dlg.Handle(), nil))
			if selected != 0 {
				dlg.Accept()
			}
		}},
	}}
	if err := spec.Create(nil); err != nil {
		t.Fatal(err)
	}
	defer dlg.Dispose()
	dlg.Run()
	if selected != 100 {
		t.Fatalf("selected command = %d; expected Settings", selected)
	}
}
