package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ulikunitz/xz"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/ffmpeg.exe.xz
var compressedFFmpeg []byte

const ffmpegHash = "3256173f3f8bffd7df12227c68adf68025edb1832273a9530688a7bb1ed8edec"

func fileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
func extractFFmpeg(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dataDir, "ffmpeg-"+ffmpegHash[:12]+".exe")
	if fileHash(path) == ffmpegHash {
		return path, nil
	}
	r, err := xz.NewReader(bytes.NewReader(compressedFFmpeg))
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dataDir, "ffmpeg-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != ffmpegHash {
		return "", fmt.Errorf("FFmpeg integrity check failed")
	}
	if err = os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

type Recorder struct {
	cmd                           *exec.Cmd
	input                         io.WriteCloser
	done                          chan error
	path                          string
	width, height, fps            int
	log                           *os.File
	lastFrame                     time.Time
	showTimestamp, showIdleMarker bool
	timeFormat                    string
}

func startRecorder(c Config, ffmpeg, logDir string, w, h int) (*Recorder, error) {
	if err := os.MkdirAll(c.Folder, 0700); err != nil {
		return nil, err
	}
	w, h = dimensions(w, h, c.Resolution)
	now := time.Now()
	path := filepath.Join(c.Folder, fmt.Sprintf("screen-%s_%06d.mkv", now.Format("2006-01-02_15-04-05"), now.Nanosecond()/1000))
	args := []string{"-hide_banner", "-loglevel", "error", "-probesize", "32", "-analyzeduration", "0", "-f", "rawvideo", "-pixel_format", "rgba", "-video_size", fmt.Sprintf("%dx%d", w, h), "-framerate", fmt.Sprint(c.FPS), "-i", "pipe:0", "-an"}
	args = append(args, encoderOptions(c)...)
	args = append(args, "-g", fmt.Sprint(c.FPS*10), "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", "-pix_fmt", "yuv420p", "-flush_packets", "1", "-cluster_time_limit", "1000", "-y", path)
	cmd := exec.Command(ffmpeg, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	log, err := os.OpenFile(filepath.Join(logDir, "ffmpeg.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		log.Close()
		return nil, err
	}
	r := &Recorder{cmd: cmd, input: stdin, done: make(chan error, 1), path: path, width: w, height: h, fps: c.FPS, log: log, showTimestamp: c.ShowTimestamp, showIdleMarker: c.ShowIdleMarker, timeFormat: c.TimeFormat}
	go func() { r.done <- cmd.Wait(); close(r.done) }()
	return r, nil
}
func (r *Recorder) write(img *image.RGBA) error {
	if img.Bounds().Dx() != r.width || img.Bounds().Dy() != r.height {
		dst := image.NewRGBA(image.Rect(0, 0, r.width, r.height))
		xdraw.BiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
		img = dst
	}
	// NRGBA/RGBA channel order is compatible with FFmpeg's rgba input.
	if img.Stride == r.width*4 {
		if _, err := r.input.Write(img.Pix[:r.width*r.height*4]); err != nil {
			return err
		}
	} else {
		for y := 0; y < r.height; y++ {
			row := img.Pix[y*img.Stride : y*img.Stride+r.width*4]
			if _, err := r.input.Write(row); err != nil {
				return err
			}
		}
	}
	r.lastFrame = time.Now()
	return nil
}
func (r *Recorder) stop(timeout time.Duration) error {
	if r == nil {
		return nil
	}
	_ = r.input.Close()
	var err error
	select {
	case err = <-r.done:
	case <-time.After(timeout):
		_ = r.cmd.Process.Kill()
		err = <-r.done
	}
	_ = r.log.Close()
	return err
}

var parsedFont, _ = opentype.Parse(goregular.TTF)

func textFace(size float64) font.Face {
	face, err := opentype.NewFace(parsedFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	return face
}
func drawText(img *image.RGBA, x, y int, text string, size float64) {
	face := textFace(size)
	if face == nil {
		return
	}
	defer face.Close()
	d := font.Drawer{Dst: img, Src: image.White, Face: face, Dot: fixed.P(x, y)}
	d.DrawString(text)
}
func formatTimestamp(now time.Time, format string) string {
	if format == "12-hour" {
		return now.Format("2006-01-02 03:04:05 PM")
	}
	return now.Format("2006-01-02 15:04:05")
}
func addClock(img *image.RGBA, now time.Time, format string) {
	s := formatTimestamp(now, format)
	r := img.Bounds()
	face := textFace(22)
	width := 300
	if face != nil {
		d := font.Drawer{Face: face}
		width = d.MeasureString(s).Ceil() + 20
		face.Close()
	}
	draw.Draw(img, image.Rect(5, max(0, r.Dy()-36), min(r.Dx(), width), r.Dy()-5), image.Black, image.Point{}, draw.Src)
	drawText(img, 10, r.Dy()-13, s, 22)
}
func addCursor(img *image.RGBA, x, y int) {
	// A high-contrast pointer marks cursor position, matching the earlier app.
	for j := 0; j < 22; j++ {
		for i := 0; i <= j/2 && i < 12; i++ {
			p := image.Pt(x+i, y+j)
			if p.In(img.Bounds()) {
				col := color.RGBA{255, 255, 255, 255}
				if i == 0 || i == j/2 {
					col = color.RGBA{0, 0, 0, 255}
				}
				img.SetRGBA(p.X, p.Y, col)
			}
		}
	}
}
func gapFrame(w, h int, elapsed time.Duration) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.Black, image.Point{}, draw.Src)
	total := int(elapsed.Seconds() + 0.5)
	a := fmt.Sprintf("%02d:%02d:%02d", total/3600, (total/60)%60, total%60)
	size := float64(max(24, min(112, min(w, h)/10)))
	face := textFace(size)
	if face == nil {
		return img
	}
	defer face.Close()
	for i, s := range []string{"TIME SKIPPED", a} {
		d := font.Drawer{Dst: img, Src: image.White, Face: face}
		width := d.MeasureString(s).Ceil()
		d.Dot = fixed.P((w-width)/2, h/2+int(float64(i)*size))
		d.DrawString(s)
	}
	return img
}
