package main

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var dwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
var setWindowTheme = windows.NewLazySystemDLL("uxtheme.dll").NewProc("SetWindowTheme")
var fillRect = user32.NewProc("FillRect")
var wantsDarkTheme = systemWantsDarkTheme

type darkWindow struct {
	previous uintptr
	brush    win.HBRUSH
	label    bool
}

var darkWindows = struct {
	sync.RWMutex
	items map[win.HWND]darkWindow
}{items: make(map[win.HWND]darkWindow)}
var darkWindowCallback = syscall.NewCallback(darkWindowProc)

func darkWindowProc(h win.HWND, msg uint32, wp, lp uintptr) uintptr {
	darkWindows.RLock()
	entry, ok := darkWindows.items[h]
	darkWindows.RUnlock()
	if !ok {
		return win.DefWindowProc(h, msg, wp, lp)
	}
	bg, fg := win.COLORREF(walk.RGB(32, 32, 32)), win.COLORREF(walk.RGB(240, 240, 240))
	if !entry.label && msg == win.WM_ERASEBKGND {
		var rect win.RECT
		if win.GetClientRect(h, &rect) {
			fillRect.Call(wp, uintptr(unsafe.Pointer(&rect)), uintptr(entry.brush))
			return 1
		}
	}
	if msg == win.WM_CTLCOLORSTATIC {
		match := entry.label
		if !match && lp != 0 {
			var name [32]uint16
			win.GetClassName(win.HWND(lp), &name[0], len(name))
			class := windows.UTF16ToString(name[:])
			match = class == "Button" || class == "Static"
		}
		if match {
			win.SetTextColor(win.HDC(wp), fg)
			win.SetBkColor(win.HDC(wp), bg)
			return uintptr(entry.brush)
		}
	}
	return win.CallWindowProc(entry.previous, h, msg, wp, lp)
}

func systemWantsDarkTheme() bool {
	hc := win.HIGHCONTRAST{CbSize: uint32(unsafe.Sizeof(win.HIGHCONTRAST{}))}
	if win.SystemParametersInfo(win.SPI_GETHIGHCONTRAST, hc.CbSize, unsafe.Pointer(&hc), 0) && hc.DwFlags&1 != 0 {
		return false
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err == nil && value == 0
}

// Win32 does not automatically recolor classic controls when app dark mode is
// enabled. Apply an accessible dark surface while leaving native list boxes
// and buttons in their system colors where Windows supplies their painting.
func applyDarkTheme(dlg *walk.Dialog) func() {
	if !wantsDarkTheme() {
		return func() {}
	}
	var enabled int32 = 1
	dwmSetWindowAttribute.Call(uintptr(dlg.Handle()), 20, uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled))
	bg := walk.RGB(32, 32, 32)
	fg := walk.RGB(240, 240, 240)
	field := walk.RGB(53, 53, 53)
	background, err := walk.NewSolidColorBrush(bg)
	if err != nil {
		return func() {}
	}
	fields, err := walk.NewSolidColorBrush(field)
	if err != nil {
		background.Dispose()
		return func() {}
	}
	checkboxBrush := win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(bg)})
	var hooked []win.HWND
	hook := func(hwnd win.HWND, label bool) {
		previous := win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, darkWindowCallback)
		if previous != 0 {
			darkWindows.Lock()
			darkWindows.items[hwnd] = darkWindow{previous: previous, brush: checkboxBrush, label: label}
			darkWindows.Unlock()
			hooked = append(hooked, hwnd)
		}
	}
	dlg.SetBackground(background)
	var visit func(walk.Container)
	visit = func(parent walk.Container) {
		parent.SetBackground(background)
		hwnd := parent.AsContainerBase().Handle()
		hook(hwnd, false)
		for i := 0; i < parent.Children().Len(); i++ {
			w := parent.Children().At(i)
			switch v := w.(type) {
			case *walk.Label:
				v.SetBackground(background)
				v.SetTextColor(fg)
				hook(v.Handle(), true)
			case *walk.LineEdit:
				v.SetBackground(fields)
				v.SetTextColor(fg)
			case *walk.CheckBox:
				empty, _ := windows.UTF16PtrFromString("")
				setWindowTheme.Call(uintptr(v.Handle()), uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
			case *walk.ComboBox:
				dark, _ := windows.UTF16PtrFromString("DarkMode_Explorer")
				setWindowTheme.Call(uintptr(v.Handle()), uintptr(unsafe.Pointer(dark)), 0)
			case *walk.PushButton:
				dark, _ := windows.UTF16PtrFromString("DarkMode_Explorer")
				setWindowTheme.Call(uintptr(v.Handle()), uintptr(unsafe.Pointer(dark)), 0)
			}
			if child, ok := w.(walk.Container); ok {
				visit(child)
			}
		}
	}
	visit(dlg)
	return func() {
		darkWindows.Lock()
		for _, h := range hooked {
			delete(darkWindows.items, h)
		}
		darkWindows.Unlock()
		win.DeleteObject(win.HGDIOBJ(checkboxBrush))
		fields.Dispose()
		background.Dispose()
	}
}
