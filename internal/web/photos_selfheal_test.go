package web

import (
	"bytes"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

// writeJPEG is a stand-in for the ffmpeg/heif converters: it always mints a
// tiny JPEG, so tests don't need ffmpeg present.
type writeJPEG struct{}

func (writeJPEG) Preview(_, dst string) error {
	return os.WriteFile(dst, []byte{0xFF, 0xD8, 0xFF, 0xDB}, 0o644)
}
func (writeJPEG) Available() bool { return true }

// Videos that predate the poster feature (or whose async job was dropped)
// must heal themselves: rendering the gallery seeds a poster job for any
// media file without a cached one, deduped so page reloads never stack up.
func TestGallerySelfHealsMissingVideoPosters(t *testing.T) {
	store, err := files.NewStore(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	home := files.HomeDir("user@example.com")
	_ = store.EnsureUserRoot(home)
	// mov1 has no poster yet -> should be scheduled by the render.
	_ = store.Write(home, "2026-09/mov1.mov", strings.NewReader("video"), 5)
	// mov2 already has a cached poster -> must not be re-queued.
	_ = store.Write(home, "2026-09/mov2.mov", strings.NewReader("video"), 5)
	_ = store.Write(home, "2026-09/.previews/mov2.jpg", bytes.NewReader([]byte{0xFF, 0xD8, 0xFF, 0xDB}), 4)

	v := &views{
		photos:       store,
		videoConv:    writeJPEG{},
		previewSlots: make(chan struct{}, 2),
		previewing:   map[string]bool{},
	}
	user := &identity.User{ID: uuid.New(), Email: "user@example.com"}

	first, ok := v.galleryViewData(httptest.NewRecorder(), httptest.NewRequest("GET", "/gallery", nil), user, "", "", "")
	if !ok {
		t.Fatal("galleryViewData failed")
	}
	ones, both := monthFiles(first)
	_, okMov1 := ones["2026-09/mov1.mov"]
	_, okMov2 := ones["2026-09/mov2.mov"]
	if !okMov1 || !okMov2 || !both {
		t.Fatalf("seeded files not rendered: months=%d files=%v", len(first.PhotoMonths), ones)
	}
	for path, item := range ones {
		want := path == "2026-09/mov2.mov" // only the pre-cached poster
		if item.HasPreview != want {
			t.Errorf("%s HasPreview=%v, want %v", path, item.HasPreview, want)
		}
	}

	// The background job should land a poster within a few seconds.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := store.Stat(home, "2026-09/.previews/mov1.jpg"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("self-heal never created the mov1 poster")
		}
		time.Sleep(25 * time.Millisecond)
	}
	// mov2's cached poster must be untouched.
	if info, err := store.Stat(home, "2026-09/.previews/mov2.jpg"); err != nil || info.Size != 4 {
		t.Errorf("mov2 cached poster disturbed: err=%v size=%d", err, info.Size)
	}

	// A later render now shows mov1 with a preview.
	second, ok := v.galleryViewData(httptest.NewRecorder(), httptest.NewRequest("GET", "/gallery", nil), user, "", "", "")
	if !ok {
		t.Fatal("second galleryViewData failed")
	}
	seen, _ := monthFiles(second)
	if !seen["2026-09/mov1.mov"].HasPreview {
		t.Errorf("mov1 still without a poster after self-heal")
	}
	if !seen["2026-09/mov2.mov"].HasPreview {
		t.Errorf("mov2 lost its poster flag")
	}
}

// monthFiles flattens the first month into path->item plus whether the
// gallery grouper produced any months at all.
func monthFiles(data viewData) (map[string]photoFileItem, bool) {
	out := map[string]photoFileItem{}
	if len(data.PhotoMonths) == 0 {
		return out, false
	}
	for _, item := range data.PhotoMonths[0].Files {
		out[item.Path] = item
	}
	return out, true
}
