package main

import (
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const appVersion = "v1.0.1"
const latestReleaseURL = "https://api.github.com/repos/jovermier/awaytrace/releases/latest"
const maxUpdateBytes = 150 * 1024 * 1024

type releaseAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
	DownloadURL string `json:"browser_download_url"`
}
type releaseInfo struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func parseVersion(s string) ([3]uint64, error) {
	var result [3]uint64
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return result, fmt.Errorf("invalid stable version %q", s)
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseUint(m[i+1], 10, 32)
		if err != nil {
			return result, err
		}
		result[i] = n
	}
	return result, nil
}
func isNewerVersion(candidate, current string) (bool, error) {
	a, err := parseVersion(candidate)
	if err != nil {
		return false, err
	}
	b, err := parseVersion(current)
	if err != nil {
		return false, err
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i], nil
		}
	}
	return false, nil
}
func (r releaseInfo) findAsset(name string) (releaseAsset, error) {
	if r.Draft || r.Prerelease {
		return releaseAsset{}, fmt.Errorf("release is not stable")
	}
	if _, err := parseVersion(r.Tag); err != nil {
		return releaseAsset{}, err
	}
	for _, a := range r.Assets {
		if a.Name == name {
			if a.Size <= 0 || a.Size > maxUpdateBytes {
				return releaseAsset{}, fmt.Errorf("invalid update size")
			}
			if !strings.HasPrefix(a.Digest, "sha256:") || len(a.Digest) != 71 {
				return releaseAsset{}, fmt.Errorf("release has no SHA-256 digest")
			}
			if _, err := hex.DecodeString(a.Digest[7:]); err != nil {
				return releaseAsset{}, fmt.Errorf("invalid update digest")
			}
			u, err := url.Parse(a.DownloadURL)
			expected := "/jovermier/awaytrace/releases/download/" + r.Tag + "/" + name
			if err != nil || u.Scheme != "https" || u.User != nil || u.Host != "github.com" || u.Path != expected || u.RawQuery != "" || u.Fragment != "" {
				return releaseAsset{}, fmt.Errorf("untrusted update URL")
			}
			return a, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("release is missing %s", name)
}
func updateHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != "https" {
			return fmt.Errorf("unsafe update redirect")
		}
		h := strings.ToLower(req.URL.Hostname())
		if h != "github.com" && h != "api.github.com" && !strings.HasSuffix(h, ".githubusercontent.com") {
			return fmt.Errorf("untrusted update redirect")
		}
		return nil
	}}
}
func getLatestRelease(ctx context.Context, client *http.Client, endpoint string) (releaseInfo, error) {
	var result releaseInfo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AwayTrace/"+appVersion)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("release check returned HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}
func downloadVerified(ctx context.Context, client *http.Client, asset releaseAsset, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "AwayTrace/"+appVersion)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update download returned HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, "awaytrace-update-*.exe")
	if err != nil {
		return "", err
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, asset.Size+1))
	if err != nil {
		return "", err
	}
	if n != asset.Size || fmt.Sprintf("sha256:%x", h.Sum(nil)) != strings.ToLower(asset.Digest) {
		return "", fmt.Errorf("update failed size or SHA-256 verification")
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = validateWindowsExecutable(name, asset.Name == "AwayTrace-Setup.exe"); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}
func validateWindowsExecutable(path string, installer bool) error {
	f, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("update is not a valid Windows executable: %w", err)
	}
	defer f.Close()
	if (f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 && !(installer && f.Machine == pe.IMAGE_FILE_MACHINE_I386)) || f.Characteristics&0x2000 != 0 {
		return fmt.Errorf("update is not a supported Windows application")
	}
	return nil
}
func installedExecutable(path string) bool {
	want := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "AwayTrace", "AwayTrace.exe")
	return strings.EqualFold(filepath.Clean(path), filepath.Clean(want))
}
