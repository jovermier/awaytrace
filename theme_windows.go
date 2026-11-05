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
var frameRect = user32.NewProc("FrameRect")
var getDpiForWindow = user32.NewProc("GetDpiForWindow")
var wantsDarkTheme = systemWantsDarkTheme

type darkWindow struct {
	previous uintptr
	brush    win.HBRUSH
	field    win.HBRUSH
	label    bool
	checkbox bool
	combo    bool
	link     bool
}

var darkWindows = struct {
	sync.RWMutex
	items map[win.HWND]darkWindow
}{items: make(map[win.HWND]darkWindow)}
var darkWindowCallback = syscall.NewCallback(darkWindowProc)

func darkWindowProc(h win.HWND, msg uint32, wp uintptr, lp unsafe.Pointer) uintptr {
	darkWindows.RLock()
	entry, ok := darkWindows.items[h]
	darkWindows.RUnlock()
	if !ok {
		return win.DefWindowProc(h, msg, wp, uintptr(lp))
	}
	bg, fg := win.COLORREF(walk.RGB(32, 32, 32)), win.COLORREF(walk.RGB(240, 240, 240))
	if entry.checkbox && msg == win.WM_PAINT {
		result := win.CallWindowProc(entry.previous, h, msg, wp, uintptr(lp))
		paintDarkCheckBox(h, entry.field)
		return result
	}
	if entry.combo && msg == win.WM_PAINT {
		result := win.CallWindowProc(entry.previous, h, msg, wp, uintptr(lp))
		paintDarkCombo(h, entry.field)
		return result
	}
	if msg == win.WM_NOTIFY && lp != nil {
		header := (*win.NMHDR)(lp)
		darkWindows.RLock()
		control, found := darkWindows.items[header.HwndFrom]
		darkWindows.RUnlock()
		if found && control.link && header.Code == win.NM_CUSTOMDRAW {
			draw := (*win.NMCUSTOMDRAW)(lp)
			switch draw.DwDrawStage {
			case win.CDDS_PREPAINT:
				return win.CDRF_NOTIFYITEMDRAW
			case win.CDDS_ITEMPREPAINT:
				win.SetTextColor(draw.Hdc, win.COLORREF(walk.RGB(130, 190, 255)))
				win.SetBkMode(draw.Hdc, win.TRANSPARENT)
				return win.CDRF_NEWFONT
			}
		}
	}
	if msg == win.WM_CTLCOLORLISTBOX && entry.combo {
		win.SetTextColor(win.HDC(wp), fg)
		win.SetBkColor(win.HDC(wp), win.COLORREF(walk.RGB(53, 53, 53)))
		return uintptr(entry.field)
	}
	if !entry.label && msg == win.WM_ERASEBKGND {
		var rect win.RECT
		if win.GetClientRect(h, &rect) {
			fillRect.Call(wp, uintptr(unsafe.Pointer(&rect)), uintptr(entry.brush))
			return 1
		}
	}
	if msg == win.WM_CTLCOLORSTATIC {
		match := entry.label
		if !match && lp != nil {
			var name [32]uint16
			win.GetClassName(win.HWND(uintptr(lp)), &name[0], len(name))
			class := windows.UTF16ToString(name[:])
			match = class == "Button" || class == "Static" || class == "SysLink"
		}
		if match {
			win.SetTextColor(win.HDC(wp), fg)
			win.SetBkColor(win.HDC(wp), bg)
			return uintptr(entry.brush)
		}
	}
	return win.CallWindowProc(entry.previous, h, msg, wp, uintptr(lp))
}

// Let Windows retain ownership of the controls and their input behavior; only
// the small check glyph and the combo-box item surfaces need custom painting.
func paintDarkCheckBox(h win.HWND, brush win.HBRUSH) {
	var r win.RECT
	if !win.GetClientRect(h, &r) {
		return
	}
	var dpi uintptr
	if getDpiForWindow.Find() == nil {
		dpi, _, _ = getDpiForWindow.Call(uintptr(h))
	}
	if dpi == 0 {
		dpi = 96
	}
	size := int32((13*dpi + 48) / 96)
	top := (r.Bottom - r.Top - size) / 2
	box := win.RECT{Left: 0, Top: top, Right: size, Bottom: top + size}
	dc := win.GetDC(h)
	if dc == 0 {
		return
	}
	defer win.ReleaseDC(h, dc)
	fillRect.Call(uintptr(dc), uintptr(unsafe.Pointer(&box)), uintptr(brush))
	frameRect.Call(uintptr(dc), uintptr(unsafe.Pointer(&box)), uintptr(win.GetStockObject(win.WHITE_BRUSH)))
	if win.SendMessage(h, win.BM_GETCHECK, 0, 0) == win.BST_CHECKED {
		old := win.SelectObject(dc, win.GetStockObject(win.WHITE_PEN))
		win.MoveToEx(dc, int(size/4), int(top+size/2), nil)
		win.LineTo(dc, size*2/5, top+size*3/4)
		win.LineTo(dc, size*4/5, top+size/4)
		win.SelectObject(dc, old)
	}
}

func paintDarkCombo(h win.HWND, brush win.HBRUSH) {
	var r win.RECT
	if !win.GetClientRect(h, &r) {
		return
	}
	dc := win.GetDC(h)
	if dc == 0 {
		return
	}
	defer win.ReleaseDC(h, dc)
	r.Left++
	r.Top++
	r.Right--
	r.Bottom--
	fillRect.Call(uintptr(dc), uintptr(unsafe.Pointer(&r)), uintptr(brush))
	index := win.SendMessage(h, win.CB_GETCURSEL, 0, 0)
	if index == win.CB_ERR {
		return
	}
	length := win.SendMessage(h, win.CB_GETLBTEXTLEN, index, 0)
	if length == win.CB_ERR || length > 4096 {
		return
	}
	text := make([]uint16, length+1)
	if win.SendMessage(h, win.CB_GETLBTEXT, index, uintptr(unsafe.Pointer(&text[0]))) == win.CB_ERR {
		return
	}
	win.SetTextColor(dc, win.COLORREF(walk.RGB(240, 240, 240)))
	win.SetBkMode(dc, win.TRANSPARENT)
	font := win.SendMessage(h, win.WM_GETFONT, 0, 0)
	oldFont := win.SelectObject(dc, win.HGDIOBJ(font))
	arrowX, arrowY := r.Right-9, (r.Top+r.Bottom)/2-1
	oldPen := win.SelectObject(dc, win.GetStockObject(win.WHITE_PEN))
	win.MoveToEx(dc, int(arrowX-3), int(arrowY-1), nil)
	win.LineTo(dc, arrowX, arrowY+2)
	win.LineTo(dc, arrowX+4, arrowY-2)
	win.SelectObject(dc, oldPen)
	r.Left += 3
	r.Right -= 20
	win.DrawTextEx(dc, &text[0], int32(length), &r, win.DT_SINGLELINE|win.DT_VCENTER|win.DT_END_ELLIPSIS, nil)
	win.SelectObject(dc, oldFont)
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

// Win32 does not automatically recolor classic controls for app dark mode.
// Keep the native controls and their input behavior while adjusting their colors.
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
	fieldBrush := win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(field)})
	var hooked []win.HWND
	hook := func(hwnd win.HWND, label, checkbox, combo, link bool) {
		previous := win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, darkWindowCallback)
		if previous != 0 {
			darkWindows.Lock()
			darkWindows.items[hwnd] = darkWindow{previous: previous, brush: checkboxBrush, field: fieldBrush, label: label, checkbox: checkbox, combo: combo, link: link}
			darkWindows.Unlock()
			hooked = append(hooked, hwnd)
		}
	}
	dlg.SetBackground(background)
	var visit func(walk.Container)
	visit = func(parent walk.Container) {
		parent.SetBackground(background)
		hwnd := parent.AsContainerBase().Handle()
		hook(hwnd, false, false, false, false)
		for i := 0; i < parent.Children().Len(); i++ {
			w := parent.Children().At(i)
			switch v := w.(type) {
			case *walk.Label:
				v.SetBackground(background)
				v.SetTextColor(fg)
				hook(v.Handle(), true, false, false, false)
			case *walk.LineEdit:
				v.SetBackground(fields)
				v.SetTextColor(fg)
			case *walk.CheckBox:
				empty, _ := windows.UTF16PtrFromString("")
				setWindowTheme.Call(uintptr(v.Handle()), uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
				hook(v.Handle(), true, true, false, false)
			case *walk.ComboBox:
				empty, _ := windows.UTF16PtrFromString("")
				setWindowTheme.Call(uintptr(v.Handle()), uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
				hook(v.Handle(), true, false, true, false)
			case *walk.LinkLabel:
				v.SetBackground(background)
				hook(v.Handle(), false, false, false, true)
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
		win.DeleteObject(win.HGDIOBJ(fieldBrush))
		fields.Dispose()
		background.Dispose()
	}
}
