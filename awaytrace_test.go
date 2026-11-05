package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lxn/win"
)

func TestConfigMigrationAndDimensions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(`{"grace_seconds":30,"idle_seconds":17,"fps":2,"folder":"C:\\Existing","startup":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Idle != 17 || c.Codec != "H.264" || c.Speed != "Balanced" || c.Quality != 28 || c.Resolution != "Original" || !c.ShowTimestamp || !c.ShowIdleMarker || c.TimeFormat != "24-hour" {
		t.Fatalf("migration: %+v", c)
	}
	c.Quality = 31
	c.Speed = "Smaller files"
	c.ShowTimestamp = false
	c.ShowIdleMarker = false
	c.TimeFormat = "12-hour"
	if err = saveConfig(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(p)
	if err != nil || got != c {
		t.Fatalf("save: %+v %v", got, err)
	}
	w, h := dimensions(3440, 1440, "Fit 1920 × 1080")
	if w != 1920 || h != 804 {
		t.Fatalf("dimensions: %dx%d", w, h)
	}
	w, h = dimensions(800, 600, "Fit 1920 × 1080")
	if w != 800 || h != 600 {
		t.Fatal("upscaled small input")
	}
	c.Codec = "H.264"
	c.Quality = 52
	if c.validate() == nil {
		t.Fatal("invalid quality accepted")
	}
}
func testFrame(w, h, n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{40, 80, 130, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(n*4, 10, n*4+30, 40), image.White, image.Point{}, draw.Src)
	addClock(img, time.Now(), "24-hour")
	return img
}
func ffmpegForTest(t *testing.T) string {
	t.Helper()
	p := os.Getenv("AWAYTRACE_TEST_FFMPEG")
	if p == "" {
		t.Fatal("AWAYTRACE_TEST_FFMPEG must name the test FFmpeg binary")
	}
	return p
}
func decodedFrames(t *testing.T, ffmpeg, path string) int {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-i", path, "-vf", "showinfo", "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("decode: %v: %s", err, out)
	}
	return len(regexp.MustCompile(`\bn:\s*\d+\s+pts:`).FindAll(out, -1))
}
func TestCodecSessionsAndShutdown(t *testing.T) {
	ffmpeg := ffmpegForTest(t)
	for _, codec := range codecs {
		for _, speed := range speeds {
			t.Run(codec+"/"+speed, func(t *testing.T) {
				dir := t.TempDir()
				c := defaultConfig()
				c.Folder = dir
				c.Codec = codec
				c.Speed = speed
				r, err := startRecorder(c, ffmpeg, dir, 640, 360)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 12; i++ {
					if err = r.write(testFrame(640, 360, i)); err != nil {
						t.Fatal(err)
					}
				}
				g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpeg, false)
				g.rec = r
				start := time.Now()
				if !g.shutdownQuery() {
					t.Fatal("shutdown rejected")
				}
				elapsed := time.Since(start)
				if elapsed > 1300*time.Millisecond {
					t.Fatalf("shutdown response %v", elapsed)
				}
				g.mu.Lock()
				stopped := g.rec == nil
				g.mu.Unlock()
				if !stopped {
					t.Fatal("recorder did not close")
				}
				if n := decodedFrames(t, ffmpeg, r.path); n != 12 {
					t.Fatalf("got %d of 12 frames", n)
				}
				t.Logf("saved 12 frames in %v", elapsed)
			})
		}
	}
}
func TestIdleGapRemainsOneSession(t *testing.T) {
	oldIdle, oldCapture, oldBounds := getIdle, captureDesktop, desktopBounds
	defer func() { getIdle = oldIdle; captureDesktop = oldCapture; desktopBounds = oldBounds }()
	var idle time.Duration
	getIdle = func() time.Duration { return idle }
	desktopBounds = func() (int, int, int, int) { return 0, 0, 640, 360 }
	captureDesktop = func(x, y, w, h int) (*image.RGBA, error) { return testFrame(w, h, 0), nil }
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	c.Grace = 0
	c.FPS = 3
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpegForTest(t), false)
	g.tick()
	if g.rec == nil || g.rec.lastFrame.IsZero() {
		t.Fatal("recording did not start")
	}
	path := g.rec.path
	idle = 20 * time.Second
	g.tick()
	if !g.idleGap {
		t.Fatal("idle pause not detected")
	}
	idle = 0
	g.nextFrame = time.Time{}
	g.tick()
	if g.rec == nil || g.rec.path != path || g.idleGap {
		t.Fatal("did not resume same recording")
	}
	g.toggle()
	if g.rec != nil || g.enabled {
		t.Fatal("stop did not end session")
	}
	if n := decodedFrames(t, g.ffmpeg, path); n != 4 {
		t.Fatalf("expected frame, two marker frames, frame; got %d", n)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.mkv"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files %v %v", files, err)
	}
}
func TestDisabledOverlaysAndTimeFormats(t *testing.T) {
	now := time.Date(2026, time.November, 4, 13, 5, 6, 0, time.Local)
	if got := formatTimestamp(now, "24-hour"); got != "2026-11-04 13:05:06" {
		t.Fatal(got)
	}
	if got := formatTimestamp(now, "12-hour"); got != "2026-11-04 01:05:06 PM" {
		t.Fatal(got)
	}
	oldIdle, oldCapture, oldBounds, oldClock := getIdle, captureDesktop, desktopBounds, drawTimestamp
	defer func() {
		getIdle = oldIdle
		captureDesktop = oldCapture
		desktopBounds = oldBounds
		drawTimestamp = oldClock
	}()
	var idle time.Duration
	getIdle = func() time.Duration { return idle }
	desktopBounds = func() (int, int, int, int) { return 0, 0, 640, 360 }
	captureDesktop = func(x, y, w, h int) (*image.RGBA, error) { return image.NewRGBA(image.Rect(0, 0, w, h)), nil }
	clocks := 0
	drawTimestamp = func(*image.RGBA, time.Time, string) { clocks++ }
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	c.Grace = 0
	c.ShowTimestamp = false
	c.ShowIdleMarker = false
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpegForTest(t), false)
	g.tick()
	path := g.rec.path
	idle = 20 * time.Second
	g.tick()
	idle = 0
	g.nextFrame = time.Time{}
	g.tick()
	g.toggle()
	if clocks != 0 {
		t.Fatal("disabled timestamp was drawn")
	}
	if n := decodedFrames(t, g.ffmpeg, path); n != 2 {
		t.Fatalf("disabled marker: got %d frames", n)
	}
}
func TestBlockedShutdownReturnsPromptly(t *testing.T) {
	c := defaultConfig()
	g := newGuard(c, "", "", "", false)
	g.mu.Lock()
	start := time.Now()
	done := make(chan bool, 1)
	go func() { done <- g.shutdownQuery() }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("rejected")
		}
	case <-time.After(1300 * time.Millisecond):
		g.mu.Unlock()
		t.Fatal("blocked shutdown")
	}
	g.mu.Unlock()
	if time.Since(start) > 1300*time.Millisecond {
		t.Fatal("slow callback")
	}
}
func TestGapMarkerAndEncoderOptions(t *testing.T) {
	img := gapFrame(640, 360, 3661*time.Second)
	if img.RGBAAt(1, 1).R != 0 {
		t.Fatal("marker background not black")
	}
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 220 {
			n++
		}
	}
	if n < 100 {
		t.Fatal("marker text missing")
	}
	for _, codec := range codecs {
		c := defaultConfig()
		c.Codec = codec
		opts := strings.Join(encoderOptions(c), " ")
		if !strings.Contains(opts, "-crf 28") {
			t.Fatal(codec, opts)
		}
	}
}

func TestFullResolutionShutdown(t *testing.T) {
	ffmpeg := ffmpegForTest(t)
	for _, codec := range codecs {
		t.Run(codec, func(t *testing.T) {
			dir := t.TempDir()
			c := defaultConfig()
			c.Folder = dir
			c.Codec = codec
			r, err := startRecorder(c, ffmpeg, dir, 3440, 1440)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 18; i++ {
				if err = r.write(testFrame(3440, 1440, i)); err != nil {
					t.Fatal(err)
				}
			}
			g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpeg, false)
			g.rec = r
			start := time.Now()
			g.shutdownQuery()
			elapsed := time.Since(start)
			g.mu.Lock()
			closed := g.rec == nil
			g.mu.Unlock()
			if !closed {
				t.Fatal("session did not close")
			}
			if n := decodedFrames(t, ffmpeg, r.path); n != 18 {
				t.Fatalf("%d of 18 frames survived", n)
			}
			t.Logf("3440x1440 shutdown: %v", elapsed)
		})
	}
}

func TestInterruptedRecordingRetainsEarlierFrames(t *testing.T) {
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	ffmpeg := ffmpegForTest(t)
	r, err := startRecorder(c, ffmpeg, dir, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 18; i++ {
		if err = r.write(testFrame(640, 360, i)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info, e := os.Stat(r.path); e == nil && info.Size() > 1000 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = r.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-r.done
	_ = r.input.Close()
	_ = r.log.Close()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-i", r.path, "-vf", "showinfo", "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	if n := len(regexp.MustCompile(`\bn:\s*\d+\s+pts:`).FindAll(out, -1)); n == 0 {
		t.Fatalf("no decodable frames after interruption: %s", out)
	}
}

func TestFFmpegExtraction(t *testing.T) {
	dir := t.TempDir()
	path, err := extractFFmpeg(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fileHash(path) != ffmpegHash {
		t.Fatal("extracted FFmpeg hash mismatch")
	}
	again, err := extractFFmpeg(dir)
	if err != nil || again != path {
		t.Fatalf("cached encoder: %s %v", again, err)
	}
}

// This opt-in smoke test opens the settings dialog with an isolated config.
// Select 12-hour, uncheck both overlays and save; the isolated config and
// stubbed startup handler prevent changes to the installed application.
func TestManualSettingsDialog(t *testing.T) {
	if os.Getenv("AWAYTRACE_MANUAL_UI") != "1" {
		t.Skip("manual UI check")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Getenv("AWAYTRACE_FORCE_DARK") == "1" {
		old := wantsDarkTheme
		wantsDarkTheme = func() bool { return true }
		defer func() { wantsDarkTheme = old }()
	}
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpegForTest(t), true)
	oldStartup := applyStartup
	applyStartup = func(bool) error { return nil }
	defer func() { applyStartup = oldStartup }()
	settingsDialog(nil, g)
	darkWindows.RLock()
	remaining := len(darkWindows.items)
	darkWindows.RUnlock()
	if remaining != 0 {
		t.Fatalf("dark theme hooks were not cleaned up: %d", remaining)
	}
	updated, err := loadConfig(g.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TimeFormat != "12-hour" || updated.ShowTimestamp || updated.ShowIdleMarker || !updated.CheckUpdates || !updated.AutoUpdate {
		t.Fatalf("settings were not saved: %+v", updated)
	}
}

func TestManualAboutDialog(t *testing.T) {
	if os.Getenv("AWAYTRACE_MANUAL_ABOUT") != "1" {
		t.Skip("manual About dialog check")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Getenv("AWAYTRACE_FORCE_DARK") == "1" {
		old := wantsDarkTheme
		wantsDarkTheme = func() bool { return true }
		defer func() { wantsDarkTheme = old }()
	}
	oldOpen := openRepository
	called := false
	openRepository = func() error { called = true; return nil }
	defer func() { openRepository = oldOpen }()
	aboutDialog(nil)
	if !called {
		t.Fatal("repository link was not activated")
	}
	darkWindows.RLock()
	remaining := len(darkWindows.items)
	darkWindows.RUnlock()
	if remaining != 0 {
		t.Fatalf("dark theme hooks remain: %d", remaining)
	}
}

// Sends Windows messages to an isolated test window; does not lock or shut
// down the computer and does not read the desktop.
func TestNativeSessionMessages(t *testing.T) {
	if os.Getenv("AWAYTRACE_NATIVE_UI") != "1" {
		t.Skip("native UI integration")
	}
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	c.Grace = 30
	ffmpeg := ffmpegForTest(t)
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpeg, true)
	r, err := startRecorder(c, ffmpeg, dir, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err = r.write(testFrame(640, 360, i)); err != nil {
			t.Fatal(err)
		}
	}
	g.rec = r
	errors := make(chan error, 1)
	onUIReady = func(h win.HWND) {
		go func() {
			win.SendMessage(h, 0x02B1, 7, 0)
			g.mu.Lock()
			locked, closed := g.locked, g.rec == nil
			g.mu.Unlock()
			if !locked || !closed {
				errors <- fmt.Errorf("lock did not close session")
				win.PostMessage(h, win.WM_CLOSE, 0, 0)
				return
			}
			win.SendMessage(h, 0x02B1, 8, 0)
			g.mu.Lock()
			unlocked, enabled := !g.locked, g.enabled
			g.mu.Unlock()
			if !unlocked || !enabled {
				errors <- fmt.Errorf("unlock did not re-arm")
				win.PostMessage(h, win.WM_CLOSE, 0, 0)
				return
			}
			start := time.Now()
			accepted := win.SendMessage(h, 0x0011, 0, 0)
			elapsed := time.Since(start)
			if accepted != 1 || elapsed > 1300*time.Millisecond {
				errors <- fmt.Errorf("shutdown callback: %d %v", accepted, elapsed)
				win.PostMessage(h, win.WM_CLOSE, 0, 0)
				return
			}
			win.SendMessage(h, 0x0016, 0, 0)
			errors <- nil
			win.PostMessage(h, win.WM_CLOSE, 0, 0)
		}()
	}
	defer func() { onUIReady = nil }()
	if err = runUI(g); err != nil {
		t.Fatal(err)
	}
	if err = <-errors; err != nil {
		t.Fatal(err)
	}
	if n := decodedFrames(t, ffmpeg, r.path); n != 6 {
		t.Fatalf("lock saved %d frames", n)
	}
}

func TestDesktopCaptureSmoke(t *testing.T) {
	if os.Getenv("AWAYTRACE_DESKTOP_TEST") != "1" {
		t.Skip("desktop capture smoke test")
	}
	x, y, w, h := desktopBounds()
	if w <= 0 || h <= 0 {
		t.Fatal("no active desktop")
	}
	img, err := captureDesktop(x, y, w, h)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("unexpected desktop bounds: %v, want %dx%d", img.Bounds(), w, h)
	}
	t.Logf("captured %dx%d desktop in memory; no image was saved", w, h)
}

// Writes a short test recording to a temporary directory and removes it when
// the test finishes. Run only as an explicitly enabled integration test.
func TestDesktopRecordingSmoke(t *testing.T) {
	if os.Getenv("AWAYTRACE_DESKTOP_TEST") != "1" {
		t.Skip("desktop recording smoke test")
	}
	oldIdle := getIdle
	getIdle = func() time.Duration { return 0 }
	defer func() { getIdle = oldIdle }()
	dir := t.TempDir()
	c := defaultConfig()
	c.Folder = dir
	c.Grace = 0
	g := newGuard(c, filepath.Join(dir, "settings.json"), dir, ffmpegForTest(t), false)
	for i := 0; i < 4; i++ {
		g.nextFrame = time.Time{}
		g.tick()
		if g.rec == nil {
			t.Fatalf("capture failed: %s", g.status)
		}
		time.Sleep(80 * time.Millisecond)
	}
	path := g.rec.path
	g.toggle()
	if n := decodedFrames(t, g.ffmpeg, path); n != 4 {
		t.Fatalf("decoded %d of 4 captured frames", n)
	}
	t.Log("captured, finalized, and decoded four desktop frames; temporary recording removed")
}
