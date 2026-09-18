package web

import (
	"bytes"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeTestClip renders a tiny MPEG-4 video with ffmpeg itself so Preview
// is exercised against real bytes. Assumes ffmpeg exists; the caller
// skips when it does not.
func makeTestClip(t *testing.T, filter string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", filter,
		"-pix_fmt", "yuv420p", "-c:v", "mpeg4", "-q:v", "5", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg clip: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return out
}

func TestFfmpegConverterExtractsPosterFrame(t *testing.T) {
	conv := &ffmpegConverter{}
	if !conv.Available() {
		t.Skip("ffmpeg not installed")
	}
	clips := []string{
		makeTestClip(t, "testsrc=duration=2:size=640x360:rate=25"),
		// Under a second long: the 1s seek finds no frame, so the 0s
		// retry must still produce one.
		makeTestClip(t, "testsrc=duration=0.5:size=320x180:rate=25"),
	}
	for _, clip := range clips {
		dst := filepath.Join(t.TempDir(), "poster.jpg")
		if err := conv.Preview(clip, dst); err != nil {
			t.Fatalf("Preview(%s): %v", clip, err)
		}
		raw, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) < 3 || raw[0] != 0xFF || raw[1] != 0xD8 || raw[2] != 0xFF {
			t.Fatalf("poster is not a JPEG: % x", raw)
		}
		img, err := jpeg.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("decode poster: %v", err)
		}
		if b := img.Bounds(); b.Dx() > 1600 || b.Dy() > 1600 {
			t.Errorf("poster %dx%d exceeds the 1600 bound", b.Dx(), b.Dy())
		}
	}
}
