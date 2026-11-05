package main

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows' native popup-menu theme opt-in is exported by ordinal, not by a
// documented API. Restrict it to Windows 10 version 1903 and later; if the
// exports are unavailable, retain the standard Windows menu.
type trayMenuTheme struct {
	library windows.Handle
	setMode uintptr
	flush   uintptr
	known   bool
	dark    bool
}

func newTrayMenuTheme() *trayMenuTheme {
	t := &trayMenuTheme{}
	v := windows.RtlGetVersion()
	if v.MajorVersion != 10 || v.BuildNumber < 18362 {
		return t
	}
	library, err := windows.LoadLibraryEx("uxtheme.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return t
	}
	setMode, err1 := windows.GetProcAddressByOrdinal(library, 135)
	flush, err2 := windows.GetProcAddressByOrdinal(library, 136)
	if err1 != nil || err2 != nil {
		windows.FreeLibrary(library)
		return t
	}
	t.library, t.setMode, t.flush = library, setMode, flush
	return t
}

func (t *trayMenuTheme) refresh() {
	if t.setMode == 0 || t.flush == 0 {
		return
	}
	dark := wantsDarkTheme()
	if t.known && t.dark == dark {
		return
	}
	var mode uintptr // Default, including when Windows high contrast is on.
	if dark {
		mode = 2
	} // ForceDark; menus do not reliably honor AllowDark.
	syscall.SyscallN(t.setMode, mode)
	syscall.SyscallN(t.flush)
	t.known, t.dark = true, dark
}

func (t *trayMenuTheme) dispose() {
	if t.library != 0 {
		windows.FreeLibrary(t.library)
	}
}
