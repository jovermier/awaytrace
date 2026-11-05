package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Grace          int    `json:"grace_seconds"`
	Idle           int    `json:"idle_seconds"`
	FPS            int    `json:"fps"`
	Folder         string `json:"folder"`
	Startup        bool   `json:"startup"`
	Codec          string `json:"codec"`
	Quality        int    `json:"quality"`
	Speed          string `json:"encoding_speed"`
	Resolution     string `json:"resolution"`
	ShowTimestamp  bool   `json:"show_timestamp"`
	ShowIdleMarker bool   `json:"show_idle_marker"`
	TimeFormat     string `json:"time_format"`
	CheckUpdates   bool   `json:"check_updates"`
	AutoUpdate     bool   `json:"auto_update"`
}

var codecs = []string{"H.264", "H.265", "AV1"}
var speeds = []string{"Fastest", "Balanced", "Smaller files"}
var resolutions = []string{"Original", "Fit 1920 × 1080", "Fit 1280 × 720"}
var timeFormats = []string{"24-hour", "12-hour"}

func defaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{Grace: 30, Idle: 10, FPS: 3, Folder: filepath.Join(home, "Videos", "AwayTrace"), Startup: true,
		Codec: "H.264", Quality: 28, Speed: "Balanced", Resolution: "Original", ShowTimestamp: true, ShowIdleMarker: true, TimeFormat: "24-hour", CheckUpdates: true, AutoUpdate: false}
}
func has(xs []string, s string) bool {
	for _, v := range xs {
		if v == s {
			return true
		}
	}
	return false
}
func (c Config) validate() error {
	limit := 51
	if c.Codec == "AV1" {
		limit = 63
	}
	if c.Grace < 0 || c.Grace > 3600 || c.Idle < 5 || c.Idle > 86400 || c.FPS < 1 || c.FPS > 15 || c.Folder == "" {
		return fmt.Errorf("Use grace 0–3600 seconds, idle 5–86400 seconds, and FPS 1–15; select a folder")
	}
	if !has(codecs, c.Codec) || !has(speeds, c.Speed) || !has(resolutions, c.Resolution) || !has(timeFormats, c.TimeFormat) || c.Quality < 0 || c.Quality > limit {
		return fmt.Errorf("Invalid codec, quality, encoding speed, or resolution")
	}
	if c.AutoUpdate && !c.CheckUpdates {
		return fmt.Errorf("Automatic updates require automatic update checks")
	}
	return nil
}
func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if err = c.validate(); err != nil {
		return c, err
	}
	return c, nil
}
func saveConfig(path string, c Config) error {
	if err := c.validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "settings-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func dimensions(w, h int, res string) (int, int) {
	mw, mh := 0, 0
	switch res {
	case "Fit 1920 × 1080":
		mw, mh = 1920, 1080
	case "Fit 1280 × 720":
		mw, mh = 1280, 720
	default:
		return w, h
	}
	ratio := min(1.0, min(float64(mw)/float64(w), float64(mh)/float64(h)))
	if ratio >= 1 {
		return w, h
	}
	return max(2, min(mw, 2*int(float64(w)*ratio/2+0.5))), max(2, min(mh, 2*int(float64(h)*ratio/2+0.5)))
}
func encoderOptions(c Config) []string {
	idx := 0
	for i, v := range speeds {
		if c.Speed == v {
			idx = i
		}
	}
	if c.Codec == "AV1" {
		return []string{"-c:v", "libaom-av1", "-usage", "realtime", "-cpu-used", []string{"8", "7", "5"}[idx], "-lag-in-frames", "0", "-row-mt", "1", "-threads", "4", "-crf", fmt.Sprint(c.Quality), "-b:v", "0"}
	}
	codec := "libx264"
	if c.Codec == "H.265" {
		codec = "libx265"
	}
	args := []string{"-c:v", codec, "-preset", []string{"ultrafast", "fast", "medium"}[idx], "-tune", "zerolatency", "-threads", "4", "-crf", fmt.Sprint(c.Quality)}
	if c.Codec == "H.265" {
		args = append(args, "-x265-params", "log-level=error:pools=4:frame-threads=1")
	}
	return args
}
