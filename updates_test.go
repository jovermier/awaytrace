package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type updateRoundTripper func(*http.Request) (*http.Response, error)

func (f updateRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateVersionAndAssets(t *testing.T) {
	for _, tc := range []struct {
		a, b         string
		newer, valid bool
	}{
		{"v1.2.1", "v1.2.0", true, true}, {"v1.10.0", "v1.9.0", true, true}, {"v1.1.9", "v1.2.0", false, true}, {"v1.2.0", "v1.2.0", false, true},
		{"v1.3.0-rc.1", "v1.2.0", false, false}, {"v1.02.0", "v1.2.0", false, false}, {"v999999999999999.1.0", "v1.2.0", false, false},
	} {
		got, err := isNewerVersion(tc.a, tc.b)
		if (err == nil) != tc.valid || got != tc.newer {
			t.Fatalf("%s %s: %v %v", tc.a, tc.b, got, err)
		}
	}
	a := releaseAsset{Name: "AwayTrace-Setup.exe", Size: 10, Digest: "sha256:" + strings.Repeat("a", 64), DownloadURL: "https://github.com/jovermier/awaytrace/releases/download/v1.2.1/AwayTrace-Setup.exe"}
	r := releaseInfo{Tag: "v1.2.1", Assets: []releaseAsset{a}}
	if _, err := r.findAsset(a.Name); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://github.com/jovermier/awaytrace/releases/download/v1.2.1/AwayTrace-Setup.exe", "https://github.com.evil.test/jovermier/awaytrace/releases/download/v1.2.1/AwayTrace-Setup.exe", "https://github.com/jovermier/other/releases/download/v1.2.1/AwayTrace-Setup.exe"} {
		r.Assets[0].DownloadURL = bad
		if _, err := r.findAsset(a.Name); err == nil {
			t.Fatal("accepted untrusted URL", bad)
		}
	}
	r.Assets[0] = a
	r.Assets[0].Digest = "sha256:not-a-hash"
	if _, err := r.findAsset(a.Name); err == nil {
		t.Fatal("accepted invalid digest")
	}
	r.Assets[0] = a
	r.Draft = true
	if _, err := r.findAsset(a.Name); err == nil {
		t.Fatal("accepted draft")
	}
}

// Uses a harmless generated executable in temporary directories in place of
// both the application and installer; no installed application is touched.
func TestUpdateHelperEndToEnd(t *testing.T) {
	goExe := os.Getenv("AWAYTRACE_TEST_GO")
	if goExe == "" {
		t.Skip("set AWAYTRACE_TEST_GO to run helper integration")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "fixture.go")
	fixture := filepath.Join(dir, "fixture.exe")
	marker := filepath.Join(dir, "runs.txt")
	program := `package main
import ("fmt";"os")
func main(){f,e:=os.OpenFile(os.Getenv("AWAYTRACE_FIXTURE_MARKER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e==nil{fmt.Fprintln(f,os.Args[1:]);f.Close()}}`
	if err := os.WriteFile(src, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command(goExe, "build", "-buildvcs=false", "-o", fixture, src)
	build.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, out)
	}
	payload, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := fmt.Sprintf("sha256:%x", sum)
	t.Setenv("AWAYTRACE_FIXTURE_MARKER", marker)
	t.Setenv("LOCALAPPDATA", dir)
	for _, installed := range []bool{false, true} {
		t.Run(strconv.FormatBool(installed), func(t *testing.T) {
			_ = os.Remove(marker)
			target := filepath.Join(dir, "standalone", "AwayTrace.exe")
			if installed {
				target = filepath.Join(dir, "Programs", "AwayTrace", "AwayTrace.exe")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, payload, 0700); err != nil {
				t.Fatal(err)
			}
			update := filepath.Join(dir, fmt.Sprintf("update-%v.exe", installed))
			if err := os.WriteFile(update, payload, 0700); err != nil {
				t.Fatal(err)
			}
			parent := exec.Command(goExe, "version")
			parent.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			if err := parent.Run(); err != nil {
				t.Fatal(err)
			}
			args := []string{update, target, strconv.Itoa(parent.Process.Pid), digest, strconv.FormatBool(installed), "false"}
			if err := runUpdateHelper(args); err != nil {
				t.Fatal(err)
			}
			want := 1
			if installed {
				want = 2
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				b, _ := os.ReadFile(marker)
				if strings.Count(string(b), "\n") >= want {
					if !strings.Contains(string(b), "--start-stopped") {
						t.Fatalf("capture state not preserved: %s", b)
					}
					return
				}
				time.Sleep(25 * time.Millisecond)
			}
			b, _ := os.ReadFile(marker)
			t.Fatalf("helper did not complete: %s", b)
		})
	}
}
func TestDownloadVerifiesDigestSizeAndExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(binary) }))
	defer server.Close()
	asset := releaseAsset{Name: "AwayTrace.exe", Size: int64(len(binary)), Digest: fmt.Sprintf("sha256:%x", sum), DownloadURL: server.URL}
	dir := t.TempDir()
	p, err := downloadVerified(context.Background(), server.Client(), asset, dir)
	if err != nil {
		t.Fatal(err)
	}
	if fileHash(p) != fmt.Sprintf("%x", sum) {
		t.Fatal("wrong output")
	}
	asset.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, err = downloadVerified(context.Background(), server.Client(), asset, dir); err == nil {
		t.Fatal("accepted invalid digest")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.exe"))
	if len(files) != 1 {
		t.Fatalf("failed download left files: %v", files)
	}
	bad := filepath.Join(dir, "bad.exe")
	if err = os.WriteFile(bad, []byte("not an exe"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateWindowsExecutable(bad, false) == nil {
		t.Fatal("accepted invalid PE")
	}
}
func TestInstallerBootstrapperFormat(t *testing.T) {
	path := os.Getenv("AWAYTRACE_TEST_INSTALLER")
	if path == "" {
		t.Skip("set AWAYTRACE_TEST_INSTALLER to validate the setup bootstrapper")
	}
	if err := validateWindowsExecutable(path, true); err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsExecutable(path, false); err == nil {
		t.Fatal("32-bit installer should not be accepted as standalone app")
	}
}
func TestStandaloneReplacementRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	payload := filepath.Join(dir, "update.exe")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceStandalone(payload, target); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "new" {
		t.Fatal("replacement failed")
	}
	if err := replaceStandalone(filepath.Join(dir, "missing"), target); err == nil {
		t.Fatal("missing payload accepted")
	}
	b, _ = os.ReadFile(target)
	if string(b) != "new" {
		t.Fatal("old application not preserved")
	}
}
func TestUpdateWaitsForSafeSession(t *testing.T) {
	g := newGuard(defaultConfig(), "", "", "", false)
	u := &updateManager{guard: g, pending: &stagedUpdate{manual: true}}
	g.rec = &Recorder{}
	if u.applyIfReady() {
		t.Fatal("update interrupted a recording")
	}
	g.rec = nil
	g.locked = true
	if u.applyIfReady() {
		t.Fatal("update ran while locked")
	}
	g.locked = false
	g.shutting = true
	if u.applyIfReady() {
		t.Fatal("update ran during shutdown")
	}
	g.shutting = false
	u.settingsOpen = true
	if u.applyIfReady() {
		t.Fatal("update ran while settings open")
	}
	u.settingsOpen = false
	original := launchStagedUpdate
	defer func() { launchStagedUpdate = original }()
	called := false
	launchStagedUpdate = func(s stagedUpdate, enabled bool) error {
		called = true
		if !enabled {
			t.Fatal("capture state was not preserved")
		}
		return nil
	}
	if !u.applyIfReady() || !called || !g.shutting || u.pending != nil {
		t.Fatal("safe update was not launched")
	}
}

func TestAutomaticUpdateStagesAndWaitsForRecording(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	metadata := fmt.Sprintf(`{"tag_name":"v9.0.0","assets":[{"name":"AwayTrace.exe","size":%d,"digest":"sha256:%x","browser_download_url":"https://github.com/jovermier/awaytrace/releases/download/v9.0.0/AwayTrace.exe"}]}`, len(binary), sum)
	oldClient, oldLaunch := newUpdateClient, launchStagedUpdate
	defer func() { newUpdateClient = oldClient; launchStagedUpdate = oldLaunch }()
	newUpdateClient = func() *http.Client {
		return &http.Client{Transport: updateRoundTripper(func(req *http.Request) (*http.Response, error) {
			var data []byte
			if req.URL.String() == latestReleaseURL {
				data = []byte(metadata)
			} else if req.URL.Host == "github.com" {
				data = binary
			} else {
				return nil, fmt.Errorf("unexpected request")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: req}, nil
		})}
	}
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.AutoUpdate = true
	g := newGuard(cfg, filepath.Join(dir, "settings.json"), dir, "", false)
	g.rec = &Recorder{}
	u := &updateManager{guard: g}
	u.check(false)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		u.mu.Lock()
		ready := u.pending != nil
		u.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	u.mu.Lock()
	ready := u.pending != nil
	u.mu.Unlock()
	if !ready {
		t.Fatal("update did not stage")
	}
	if u.applyIfReady() {
		t.Fatal("update interrupted a recording")
	}
	g.mu.Lock()
	g.rec = nil
	g.mu.Unlock()
	called := false
	launchStagedUpdate = func(s stagedUpdate, enabled bool) error {
		called = true
		if s.version != "v9.0.0" || !enabled {
			t.Fatal("incorrect staged update")
		}
		return nil
	}
	if !u.applyIfReady() || !called {
		t.Fatal("update was not started after recording stopped")
	}
}
