package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func settingsDialog(owner walk.Form, g *Guard) {
	c, _, _ := g.snapshot()
	var dlg *walk.Dialog
	var grace, idle, fps, folder, quality *walk.LineEdit
	var codec, speed, res, timeFormat *walk.ComboBox
	var startup, showTimestamp, showIdleMarker, checkUpdates, autoUpdate *walk.CheckBox
	save := func() {
		var err error
		c.Grace, err = strconv.Atoi(grace.Text())
		if err != nil {
			walk.MsgBox(dlg, "AwayTrace", "Enter a whole number for grace period.", walk.MsgBoxIconError)
			return
		}
		c.Idle, err = strconv.Atoi(idle.Text())
		if err != nil {
			walk.MsgBox(dlg, "AwayTrace", "Enter a whole number for idle timeout.", walk.MsgBoxIconError)
			return
		}
		c.FPS, err = strconv.Atoi(fps.Text())
		if err != nil {
			walk.MsgBox(dlg, "AwayTrace", "Enter a whole number for frames per second.", walk.MsgBoxIconError)
			return
		}
		c.Quality, err = strconv.Atoi(quality.Text())
		if err != nil {
			walk.MsgBox(dlg, "AwayTrace", "Enter a whole number for constant quality.", walk.MsgBoxIconError)
			return
		}
		c.Folder = strings.TrimSpace(folder.Text())
		c.Codec = codec.Text()
		c.Speed = speed.Text()
		c.Resolution = res.Text()
		c.Startup = startup.Checked()
		c.ShowTimestamp = showTimestamp.Checked()
		c.ShowIdleMarker = showIdleMarker.Checked()
		c.TimeFormat = timeFormat.Text()
		c.CheckUpdates = checkUpdates.Checked()
		c.AutoUpdate = autoUpdate.Checked()
		if err = g.updateConfig(c); err != nil {
			walk.MsgBox(dlg, "AwayTrace", err.Error(), walk.MsgBoxIconError)
			return
		}
		dlg.Accept()
	}
	browse := func() {
		f := walk.FileDialog{Title: "Recordings folder", FilePath: folder.Text()}
		if ok, err := f.ShowBrowseFolder(dlg); err == nil && ok {
			folder.SetText(f.FilePath)
		}
	}
	spec := Dialog{AssignTo: &dlg, Title: "AwayTrace settings", FixedSize: true, MinSize: Size{Width: 590, Height: 550}, Layout: VBox{}, Children: []Widget{
		Composite{Layout: Grid{Columns: 2}, Children: []Widget{
			Label{Text: "Grace period (seconds)"}, LineEdit{AssignTo: &grace, Text: fmt.Sprint(c.Grace)},
			Label{Text: "Idle timeout (seconds)"}, LineEdit{AssignTo: &idle, Text: fmt.Sprint(c.Idle)},
			Label{Text: "Frames per second"}, LineEdit{AssignTo: &fps, Text: fmt.Sprint(c.FPS)},
			Label{Text: "Recordings folder"}, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{LineEdit{AssignTo: &folder, Text: c.Folder}, PushButton{Text: "Browse...", OnClicked: browse}}},
			Label{Text: "Video codec"}, ComboBox{AssignTo: &codec, Model: codecs, CurrentIndex: indexOf(codecs, c.Codec)},
			Label{Text: "Resolution limit"}, ComboBox{AssignTo: &res, Model: resolutions, CurrentIndex: indexOf(resolutions, c.Resolution)},
			Label{Text: "Constant quality (0–51; AV1 0–63)"}, LineEdit{AssignTo: &quality, Text: fmt.Sprint(c.Quality)},
			Label{Text: "Encoding speed"}, ComboBox{AssignTo: &speed, Model: speeds, CurrentIndex: indexOf(speeds, c.Speed)},
			Label{Text: "Timestamp format"}, ComboBox{AssignTo: &timeFormat, Model: timeFormats, CurrentIndex: indexOf(timeFormats, c.TimeFormat)},
			Label{Text: ""}, CheckBox{Alignment: AlignHNearVCenter, AssignTo: &startup, Text: "Start automatically when I sign in", Checked: c.Startup},
			Label{Text: ""}, CheckBox{Alignment: AlignHNearVCenter, AssignTo: &showTimestamp, Text: "Show timestamp on recordings", Checked: c.ShowTimestamp},
			Label{Text: ""}, CheckBox{Alignment: AlignHNearVCenter, AssignTo: &showIdleMarker, Text: "Show time skipped after idle periods", Checked: c.ShowIdleMarker},
			Label{Text: ""}, CheckBox{Alignment: AlignHNearVCenter, AssignTo: &checkUpdates, Text: "Check for updates automatically", Checked: c.CheckUpdates},
			Label{Text: ""}, CheckBox{Alignment: AlignHNearVCenter, AssignTo: &autoUpdate, Text: "Install updates when not recording", Checked: c.AutoUpdate},
		}},
		Label{Text: "Lower quality number = more detail and a larger file.\r\nCodec quality numbers are not directly comparable.\r\nVideo and overlay changes apply to the next recording session."},
		Label{Text: "Left-click the tray icon to stop or start capture.\r\nCapture re-arms after you lock and unlock Windows."},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{Text: "Save", OnClicked: save}, PushButton{Text: "Cancel", OnClicked: func() { dlg.Cancel() }}}},
	}}
	if err := spec.Create(owner); err != nil {
		walk.MsgBox(owner, "AwayTrace", err.Error(), walk.MsgBoxIconError)
		return
	}
	cleanupTheme := applyDarkTheme(dlg)
	defer cleanupTheme()
	defer dlg.Dispose()
	dlg.Run()
}
func indexOf(v []string, s string) int {
	for i, x := range v {
		if x == s {
			return i
		}
	}
	return 0
}

const repositoryURL = "https://github.com/jovermier/awaytrace"

var openRepository = func() error {
	target, err := windows.UTF16PtrFromString(repositoryURL)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, target, nil, nil, 1)
}

func aboutDialog(owner walk.Form) {
	var dlg *walk.Dialog
	spec := Dialog{AssignTo: &dlg, Title: "About AwayTrace", FixedSize: true, MinSize: Size{Width: 450, Height: 220}, Layout: VBox{}, Children: []Widget{
		Label{Text: "AwayTrace " + appVersion},
		Label{Text: "A Windows system-tray screen recorder with idle-aware session controls."},
		LinkLabel{Alignment: AlignHNearVCenter, Text: `<a href="https://github.com/jovermier/awaytrace">github.com/jovermier/awaytrace</a>`, OnLinkActivated: func(link *walk.LinkLabelLink) {
			if link.URL() != repositoryURL {
				return
			}
			if err := openRepository(); err != nil {
				walk.MsgBox(dlg, "AwayTrace", err.Error(), walk.MsgBoxIconError)
			}
		}},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{Text: "Close", OnClicked: func() { dlg.Accept() }}}},
	}}
	if err := spec.Create(owner); err != nil {
		walk.MsgBox(owner, "AwayTrace", err.Error(), walk.MsgBoxIconError)
		return
	}
	cleanup := applyDarkTheme(dlg)
	defer cleanup()
	defer dlg.Dispose()
	dlg.Run()
}
func trayIcon(col color.RGBA) (*walk.Icon, error) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(img, image.Rect(5, 8, 59, 49), image.NewUniform(color.RGBA{52, 70, 90, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(25, 50, 39, 56), image.NewUniform(color.RGBA{174, 191, 203, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(18, 56, 46, 60), image.NewUniform(color.RGBA{174, 191, 203, 255}), image.Point{}, draw.Src)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if (x-32)*(x-32)+(y-28)*(y-28) <= 64 {
				img.SetRGBA(x, y, col)
			}
		}
	}
	return walk.NewIconFromImage(img)
}

var onUIReady func(win.HWND)

func runUI(g *Guard) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mw, err := walk.NewMainWindow()
	if err != nil {
		return err
	}
	defer mw.Dispose()
	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		return err
	}
	defer ni.Dispose()
	updater := &updateManager{guard: g, window: mw}
	green, _ := trayIcon(color.RGBA{154, 164, 175, 255})
	yellow, _ := trayIcon(color.RGBA{236, 189, 69, 255})
	red, _ := trayIcon(color.RGBA{220, 85, 75, 255})
	defer green.Dispose()
	defer yellow.Dispose()
	defer red.Dispose()
	if err = ni.SetIcon(yellow); err != nil {
		return err
	}
	if err = ni.SetVisible(true); err != nil {
		return err
	}
	toggle := walk.NewAction()
	toggle.SetText("Stop capture and save")
	toggle.Triggered().Attach(func() { go g.toggle() })
	ni.ContextMenu().Actions().Add(toggle)
	status := walk.NewAction()
	status.SetText("Status: starting")
	status.SetEnabled(false)
	ni.ContextMenu().Actions().Add(status)
	settings := walk.NewAction()
	settings.SetText("Settings...")
	settings.Triggered().Attach(func() {
		updater.mu.Lock()
		updater.settingsOpen = true
		updater.mu.Unlock()
		settingsDialog(mw, g)
		updater.mu.Lock()
		updater.settingsOpen = false
		updater.mu.Unlock()
	})
	ni.ContextMenu().Actions().Add(settings)
	updates := walk.NewAction()
	updates.SetText("Check for updates...")
	updates.Triggered().Attach(func() { updater.check(true) })
	ni.ContextMenu().Actions().Add(updates)
	about := walk.NewAction()
	about.SetText("About AwayTrace...")
	about.Triggered().Attach(func() {
		updater.mu.Lock()
		updater.settingsOpen = true
		updater.mu.Unlock()
		aboutDialog(mw)
		updater.mu.Lock()
		updater.settingsOpen = false
		updater.mu.Unlock()
	})
	ni.ContextMenu().Actions().Add(about)
	folder := walk.NewAction()
	folder.SetText("Open recordings folder")
	folder.Triggered().Attach(func() { c, _, _ := g.snapshot(); _ = os.MkdirAll(c.Folder, 0700); _ = launchFolder(c.Folder) })
	ni.ContextMenu().Actions().Add(folder)
	quit := walk.NewAction()
	quit.SetText("Quit")
	quit.Triggered().Attach(func() { g.quit(); walk.App().Exit(0) })
	ni.ContextMenu().Actions().Add(quit)
	ni.MouseDown().Attach(func(x, y int, b walk.MouseButton) {
		if b == walk.LeftButton {
			go g.toggle()
		}
	})
	hwnd := mw.Handle()
	var prev uintptr
	lastStatus := ""
	callback := syscall.NewCallback(func(h win.HWND, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x02B1:
			if wp == 7 {
				g.lockSession()
			} else if wp == 8 {
				g.unlockSession()
			}
			return 0
		case 0x0011:
			if g.shutdownQuery() {
				return 1
			}
			return 0
		case 0x0016:
			if wp == 0 {
				g.cancelShutdown()
			} else {
				g.stopOnce.Do(func() { close(g.done) })
			}
			return 0
		case win.WM_TIMER:
			updater.mu.Lock()
			available := updater.available
			updater.mu.Unlock()
			if available != "" {
				updates.SetText("Update available (" + available + ")...")
			} else {
				updates.SetText("Check for updates...")
			}
			if updater.applyIfReady() {
				walk.App().Exit(0)
				return 0
			}
			if g.mu.TryLock() {
				s, e := g.status, g.enabled
				g.mu.Unlock()
				if s != lastStatus {
					lastStatus = s
					status.SetText("Status: " + s)
					ni.SetToolTip("AwayTrace — " + s)
					switch s {
					case "Recording":
						ni.SetIcon(red)
					case "Capture stopped", "Locked":
						ni.SetIcon(green)
					default:
						ni.SetIcon(yellow)
					}
				}
				if e {
					toggle.SetText("Stop capture and save")
				} else {
					toggle.SetText("Start capture")
				}
			}
		}
		return win.CallWindowProc(prev, h, msg, wp, lp)
	})
	prev = win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, callback)
	if prev == 0 {
		return fmt.Errorf("could not register window message handler")
	}
	registered, _, regErr := wtsRegister.Call(uintptr(hwnd), 0)
	if registered == 0 {
		g.mu.Lock()
		g.sessionError = true
		g.mu.Unlock()
		log.Printf("could not register lock notifications: %v", regErr)
	} else {
		defer wtsUnregister.Call(uintptr(hwnd))
	}
	win.SetTimer(hwnd, 1, 300, 0)
	go g.run()
	go updater.schedule()
	if onUIReady != nil {
		onUIReady(hwnd)
	}
	mw.Run()
	g.quit()
	return nil
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--apply-update" {
		logUpdateHelperError(runUpdateHelper(os.Args[2:]))
		return
	}
	_, _, _ = dpiProc.Call(^uintptr(3))
	name, _ := windows.UTF16PtrFromString(`Local\AwayTrace.SingleInstance`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		return
	}
	if err != nil {
		return
	}
	defer windows.CloseHandle(mutex)
	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "AwayTrace")
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return
	}
	cleanUpdateFiles(dataDir)
	logFile, err := os.OpenFile(filepath.Join(dataDir, "awaytrace.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
	}
	cfgPath := filepath.Join(dataDir, "settings.json")
	if _, err = os.Stat(cfgPath); os.IsNotExist(err) {
		old := filepath.Join(os.Getenv("LOCALAPPDATA"), "ScreenGuard", "settings.json")
		if b, e := os.ReadFile(old); e == nil {
			_ = os.WriteFile(cfgPath, b, 0600)
		}
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Printf("settings error: %v", err)
		walk.MsgBox(nil, "AwayTrace", "Settings could not be loaded: "+err.Error(), walk.MsgBoxIconError)
		return
	}
	ffmpeg, err := extractFFmpeg(dataDir)
	if err != nil {
		log.Printf("FFmpeg extraction error: %v", err)
		walk.MsgBox(nil, "AwayTrace", "Could not prepare video encoder: "+err.Error(), walk.MsgBoxIconError)
		return
	}
	stopped := false
	for _, arg := range os.Args[1:] {
		if arg == "--start-stopped" {
			stopped = true
		}
	}
	if err = setStartup(cfg.Startup); err != nil {
		log.Printf("startup registration: %v", err)
	}
	g := newGuard(cfg, cfgPath, dataDir, ffmpeg, stopped)
	if err = runUI(g); err != nil {
		log.Printf("UI error: %v", err)
		walk.MsgBox(nil, "AwayTrace", err.Error(), walk.MsgBoxIconError)
	}
	_ = time.Second
}
