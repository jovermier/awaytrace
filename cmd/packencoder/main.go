// packencoder prepares the explicitly pinned FFmpeg executable for embedding.
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"github.com/ulikunitz/xz"
	"io"
	"log"
	"os"
	"path/filepath"
)

const expected = "3256173f3f8bffd7df12227c68adf68025edb1832273a9530688a7bb1ed8edec"

func main() {
	input := flag.String("input", "", "path to pinned FFmpeg executable")
	output := flag.String("output", "assets/ffmpeg.exe.xz", "output XZ archive")
	flag.Parse()
	if *input == "" {
		log.Fatal("-input is required")
	}
	f, err := os.Open(*input)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		log.Fatal(err)
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != expected {
		log.Fatal("FFmpeg checksum does not match pinned build")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		log.Fatal(err)
	}
	out, err := os.CreateTemp(filepath.Dir(*output), "ffmpeg-*.xz.tmp")
	if err != nil {
		log.Fatal(err)
	}
	temp := out.Name()
	defer os.Remove(temp)
	w, err := xz.NewWriter(out)
	if err != nil {
		log.Fatal(err)
	}
	if _, err = io.Copy(w, f); err != nil {
		log.Fatal(err)
	}
	if err = w.Close(); err != nil {
		log.Fatal(err)
	}
	if err = out.Close(); err != nil {
		log.Fatal(err)
	}
	if err = os.Rename(temp, *output); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Prepared", *output)
}
