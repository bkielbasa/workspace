package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

// maxPhotoUploadBytes caps a single upload request. The store enforces its
// own per-file and quota limits on top; videos need headroom.
const maxPhotoUploadBytes = 1<<30 + 16<<20

// photoMonth groups one upload-month directory for the gallery.
type photoMonth struct {
	Name  string
	Path  string
	Files []photoFileItem
}

type photoFileItem struct {
	Name       string
	Path       string
	Kind       string // image, heic, video, file
	Size       int64
	Modified   string
	HasPreview bool
	Tags       []string
	Albums     []string // album IDs, for the modal checklist state
}

// photoTagCount carries one sidebar tag row.
type photoTagCount struct {
	Tag   string
	Count int
}

// photoAlbumNav carries one sidebar album row (string ID for templates).
type photoAlbumNav struct {
	ID    string
	Name  string
	Count int
}

func hasPhotoTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func albumIDs(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// kindOfPhoto classifies by extension for gallery rendering.
func kindOfPhoto(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".heic", ".heif":
		return "heic"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp":
		return "image"
	case ".mp4", ".webm", ".mov", ".m4v":
		return "video"
	default:
		return "file"
	}
}

// isMedia decides what the upload endpoint accepts: images and videos by
// extension, with content sniffing as a second opinion for odd names.
func isMedia(name string, head []byte) bool {
	switch kindOfPhoto(name) {
	case "image", "heic", "video":
		return true
	}
	if len(head) == 0 {
		return false
	}
	ct := http.DetectContentType(head)
	return strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/")
}

// previewName maps photo.ext to .previews/photo.jpg inside the same month.
func previewName(photoPath string) string {
	dir := path.Dir(photoPath)
	base := strings.TrimSuffix(path.Base(photoPath), path.Ext(photoPath))
	if dir == "." || dir == "/" {
		return ".previews/" + base + ".jpg"
	}
	return dir + "/.previews/" + base + ".jpg"
}

// heicConverter turns an HEIC file into a JPEG preview.
type heicConverter interface {
	Convert(src, dst string) error
	Available() bool
}

type execConverter struct {
	once sync.Once
	bin  string
}

func (c *execConverter) Available() bool {
	c.once.Do(func() {
		if bin, err := exec.LookPath("heif-convert"); err == nil {
			c.bin = bin
		}
	})
	return c.bin != ""
}

// Convert renders the first embedded thumbnail (or full frame) bounded to
// 1600px as a fresh JPEG (no metadata survives: no location leaks).
// Decoding is done by heif-convert (libheif); downscale+encode are pure Go
// so previews don't depend on ImageMagick delegates.
func (c *execConverter) Convert(src, dst string) error {
	if !c.Available() {
		return fmt.Errorf("photos: no HEIC converter installed")
	}
	tmp, err := os.CreateTemp("", "heic-*.jpg")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpName)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, "-q", "90", src, tmpName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("photos: heif-convert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	raw, err := os.ReadFile(tmpName)
	if err != nil {
		return err
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		// heif-convert may have written PNG (odd colorspace): try generic.
		img2, _, err2 := image.Decode(bytes.NewReader(raw))
		if err2 != nil {
			return fmt.Errorf("photos: decode converted: %w", err)
		}
		img = img2
	}
	img = boundImage(img, 1600)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	err = jpeg.Encode(out, img, &jpeg.Options{Quality: 82})
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

// videoPreviewer extracts a JPEG poster frame from a video file.
type videoPreviewer interface {
	Preview(src, dst string) error
	Available() bool
}

type ffmpegConverter struct {
	once sync.Once
	bin  string
}

func (c *ffmpegConverter) Available() bool {
	c.once.Do(func() {
		if bin, err := exec.LookPath("ffmpeg"); err == nil {
			c.bin = bin
		}
	})
	return c.bin != ""
}

// Preview grabs one frame about a second into the clip (past the fade-in
// black) and downscales it to at most 1600px wide. Seeking happens before
// decode, so ffmpeg jumps to the nearest keyframe instead of chewing the
// whole GOP: cheap even on long videos. Rotation metadata is honoured.
// Video too short for a 1s frame retries at the very start.
func (c *ffmpegConverter) Preview(src, dst string) error {
	if !c.Available() {
		return fmt.Errorf("photos: no ffmpeg installed")
	}
	// Create the scratch file beside dst, not in $TMPDIR: the caller may
	// stage it on a different mount (PVC) where rename would cross devices.
	tmp, err := os.CreateTemp(filepath.Dir(dst), "video-*.jpg")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpName)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var lastErr error
	for _, seek := range []string{"00:00:01", "00:00:00"} {
		cmd := exec.CommandContext(ctx, c.bin,
			"-y", "-ss", seek,
			"-i", src,
			"-frames:v", "1",
			"-vf", "scale='min(1600,iw)':-2",
			"-q:v", "3",
			tmpName,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			lastErr = fmt.Errorf("photos: ffmpeg frame at %s: %w: %s", seek, err, strings.TrimSpace(string(out)))
			continue
		}
		if err := os.Rename(tmpName, dst); err != nil {
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("photos: no video frame extracted")
	}
	return lastErr
}

// boundImage downscales img to fit within maxDim (never upscales).
func boundImage(img image.Image, maxDim int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}
	var nw, nh int
	if w >= h {
		nw = maxDim
		nh = h * maxDim / w
	} else {
		nh = maxDim
		nw = w * maxDim / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xRatio := float64(w) / float64(nw)
	yRatio := float64(h) / float64(nh)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			dst.Set(x, y, img.At(b.Min.X+int(float64(x)*xRatio), b.Min.Y+int(float64(y)*yRatio)))
		}
	}
	return dst
}

func (v *views) requirePhotos(w http.ResponseWriter) bool {
	if v.photos == nil {
		http.Error(w, "photo storage is not configured", http.StatusNotImplemented)
		return false
	}
	return true
}

func (v *views) ensurePhotoRoot(w http.ResponseWriter, user *identity.User) bool {
	if err := v.photos.EnsureUserRoot(files.HomeDir(user.Email)); err != nil {
		http.Error(w, "photo storage unavailable", http.StatusInternalServerError)
		return false
	}
	return true
}

func (v *views) galleryPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	data, ok := v.galleryViewData(w, r, user, driveFlash(r),
		strings.TrimSpace(r.URL.Query().Get("album")),
		strings.TrimSpace(r.URL.Query().Get("tag")))
	if !ok {
		return
	}
	renderView(w, r, v.galleryT, "layout", data)
}

// galleryContent serves the filterable region (banner + sidebar + grid) as a
// fragment for HTMX swaps: filtering, upload, album create/delete all refresh
// in place without a full page load.
func (v *views) galleryContent(w http.ResponseWriter, r *http.Request, user *identity.User) {
	data, ok := v.galleryViewData(w, r, user, driveFlash(r),
		strings.TrimSpace(r.URL.Query().Get("album")),
		strings.TrimSpace(r.URL.Query().Get("tag")))
	if !ok {
		return
	}
	renderView(w, r, v.galleryT, "galleryContent", data)
}

// isHX reports HTMX fragment requests, which get HTML partials instead of
// redirects so the page updates without reloading.
func isHX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// galleryFragment renders the filterable gallery region for HTMX swaps.
func (v *views) galleryFragment(w http.ResponseWriter, r *http.Request, user *identity.User, errMsg, albumParam, tagParam string) {
	data, ok := v.galleryViewData(w, r, user, errMsg, albumParam, tagParam)
	if !ok {
		return
	}
	renderView(w, r, v.galleryT, "galleryContent", data)
}

func (v *views) galleryViewData(w http.ResponseWriter, r *http.Request, user *identity.User, errMsg, albumParam, tagParam string) (viewData, bool) {
	if !v.requirePhotos(w) {
		return viewData{}, false
	}
	if !v.ensurePhotoRoot(w, user) {
		return viewData{}, false
	}
	home := files.HomeDir(user.Email)

	entries, err := v.photos.ListDir(home, "")
	if err != nil {
		http.NotFound(w, r)
		return viewData{}, false
	}
	var months []photoMonth
	tagsByPhoto := map[string][]string{}
	var allTags []string
	if v.tagStore != nil {
		if got, err := v.tagStore.ByPhoto(r.Context(), user.ID); err == nil {
			tagsByPhoto = got
		}
		allTags, _ = v.tagStore.All(r.Context(), user.ID)
	}
	var albums []identity.PhotoAlbum
	members := map[string][]uuid.UUID{}
	if v.albumStore != nil {
		if got, err := v.albumStore.List(r.Context(), user.ID); err == nil {
			albums = got
		}
		members, _ = v.albumStore.Memberships(r.Context(), user.ID)
	}

	// Sidebar filters: one album and/or one tag. Both narrow the same
	// month-grouped grid; empty months drop out.
	activeTag := tagParam
	var inAlbum map[string]bool
	var activeAlbumID, activeAlbumName string
	if v.albumStore != nil {
		if albumID, err := uuid.Parse(albumParam); err == nil {
			if paths, err := v.albumStore.Paths(r.Context(), user.ID, albumID); err == nil {
				inAlbum = map[string]bool{}
				for _, p := range paths {
					inAlbum[p] = true
				}
				activeAlbumID = albumID.String()
				for _, a := range albums {
					if a.ID == albumID {
						activeAlbumName = a.Name
					}
				}
				if activeAlbumName == "" {
					// Deleted or foreign album: drop the filter, don't
					// strand the user on an empty unnamed view.
					inAlbum = nil
					activeAlbumID = ""
				}
			}
		}
	}
	tagCounts := map[string]int{}
	for _, tags := range tagsByPhoto {
		for _, t := range tags {
			tagCounts[t]++
		}
	}
	for _, e := range entries {
		if !e.IsDir || strings.HasPrefix(e.Name, ".") {
			continue
		}
		month := photoMonth{Name: e.Name, Path: e.Name}
		kids := v.walkPhotos(home, e.Name, 0)
		sort.Slice(kids, func(i, j int) bool { return kids[i].Path < kids[j].Path })
		for _, k := range kids {
			kind := kindOfPhoto(k.Name)
			if kind == "file" {
				continue
			}
			item := photoFileItem{
				Name: k.Name, Path: k.Path,
				Kind: kind, Size: k.Size,
				Modified: formatDetailDate(k.ModTime),
				Tags:     tagsByPhoto[k.Path],
			}
			if activeTag != "" && !hasPhotoTag(item.Tags, activeTag) {
				continue
			}
			if inAlbum != nil && !inAlbum[item.Path] {
				continue
			}
			if m, ok := members[item.Path]; ok {
				item.Albums = albumIDs(m)
			}
			if kind == "heic" || kind == "video" {
				if _, err := v.photos.Stat(home, previewName(item.Path)); err == nil {
					item.HasPreview = true
				} else {
					// Poster missing: seed one in the background so the
					// next render shows a thumbnail. Deduped inside.
					v.schedulePreviewIfNeeded(home, item.Path, kind)
				}
			}
			month.Files = append(month.Files, item)
		}
		if len(month.Files) > 0 {
			months = append(months, month)
		}
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Name > months[j].Name })

	var tagNav []photoTagCount
	for tag, n := range tagCounts {
		tagNav = append(tagNav, photoTagCount{Tag: tag, Count: n})
	}
	sort.Slice(tagNav, func(i, j int) bool { return tagNav[i].Tag < tagNav[j].Tag })

	var albumNav []photoAlbumNav
	for _, a := range albums {
		albumNav = append(albumNav, photoAlbumNav{ID: a.ID.String(), Name: a.Name, Count: a.Count})
	}

	return viewData{
		Title:           "Photos",
		Section:         "photos",
		User:            user,
		CSRFToken:       csrfTokenFromRequest(r),
		Wide:            true,
		PhotoMonths:     months,
		AllPhotoTags:    allTags,
		TagsReady:       v.tagStore != nil,
		Albums:          albumNav,
		TagCounts:       tagNav,
		ActiveAlbumID:   activeAlbumID,
		ActiveAlbumName: activeAlbumName,
		ActiveTag:       activeTag,
		AlbumsReady:     v.albumStore != nil,
		Error:           errMsg,
	}, true
}

// photoWalkItem is one media file found by walkPhotos, with its
// library-relative path.
type photoWalkItem struct {
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
}

// walkPhotos collects media files under dir recursively (PhotoSync nests
// uploads several levels deep). Preview caches and dotfiles never surface;
// depth is capped so a weird tree can't run away.
func (v *views) walkPhotos(home, dir string, depth int) []photoWalkItem {
	if depth > 5 {
		return nil
	}
	entries, err := v.photos.ListDir(home, dir)
	if err != nil {
		return nil
	}
	var out []photoWalkItem
	for _, e := range entries {
		if strings.HasPrefix(e.Name, ".") {
			continue
		}
		sub := dir + "/" + e.Name
		if e.IsDir {
			out = append(out, v.walkPhotos(home, sub, depth+1)...)
			continue
		}
		out = append(out, photoWalkItem{Name: e.Name, Path: sub, Size: e.Size, ModTime: e.ModTime})
	}
	return out
}

func (v *views) galleryFile(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) {
		return
	}
	home := files.HomeDir(user.Email)
	name := cleanDrivePath(r.URL.Query().Get("path"))
	if name == "" {
		http.NotFound(w, r)
		return
	}
	info, err := v.photos.Stat(home, name)
	if err != nil || info.IsDir {
		http.NotFound(w, r)
		return
	}
	f, _, err := v.photos.Open(home, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("ETag", info.ETag())
	http.ServeContent(w, r, info.Name, info.ModTime, f)
}

func (v *views) galleryPreview(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) {
		return
	}
	home := files.HomeDir(user.Email)
	name := cleanDrivePath(r.URL.Query().Get("path"))
	kind := kindOfPhoto(name)
	if name == "" || (kind != "heic" && kind != "video") {
		http.NotFound(w, r)
		return
	}
	preview := previewName(name)
	if info, err := v.photos.Stat(home, preview); err == nil && !info.IsDir {
		if v.validPreview(home, preview) {
			v.servePhotoFile(w, r, home, preview, info)
			return
		}
		_ = v.photos.Remove(home, preview)
	}
	// Generate on demand and cache on disk (HEIC covers display order in
	// the iPhone camera roll; video covers everything else).
	if err := v.createPreview(home, name, kind); err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "media preview failed", "path", name, "kind", kind, "error", err)
		http.NotFound(w, r)
		return
	}
	info, err := v.photos.Stat(home, preview)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v.servePhotoFile(w, r, home, preview, info)
}

// validPreview reports whether a cached preview is really a JPEG
// (a past bug cached unconverted bytes under .jpg names).
func (v *views) validPreview(home, preview string) bool {
	f, _, err := v.photos.Open(home, preview)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [3]byte
	n, _ := io.ReadFull(f, magic[:])
	return n == 3 && magic[0] == 0xFF && magic[1] == 0xD8 && magic[2] == 0xFF
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// createPreview generates a JPEG poster for photoPath if one isn't already
// cached on disk. It is safe to call concurrently — the worst case is a
// harmless double-generation.
func (v *views) createPreview(home, photoPath, kind string) error {
	preview := previewName(photoPath)
	src, err := v.photos.LocalPath(home, photoPath)
	if err != nil {
		return err
	}
	dst, err := v.photos.LocalPath(home, preview)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirOf(dst), 0o755); err != nil {
		return err
	}
	tmp, err := v.photos.LocalPath(home, preview+".tmp.jpg")
	if err != nil {
		return err
	}
	var renderErr error
	switch kind {
	case "video":
		vid := v.videoConv
		if vid == nil {
			vid = &ffmpegConverter{}
		}
		renderErr = vid.Preview(src, tmp)
	default:
		conv := v.previewConv
		if conv == nil {
			conv = &execConverter{}
		}
		renderErr = conv.Convert(src, tmp)
	}
	if renderErr != nil {
		return renderErr
	}
	return os.Rename(tmp, dst)
}

// schedulePreviewIfNeeded mints a gallery poster for photoPath if one isn't
// cached yet, in the background. Post-upload this makes the thumbnail appear
// on the next visit; the same call on every gallery render self-heals older
// files (videos synced before the feature, or jobs dropped under load).
//
// Jobs are deduped by home+path so page reloads can't stack up work, and
// serialized through previewSlots so a phone burst of fifty clips doesn't
// spawn fifty ffmpeg processes at once. A job that outlives the request
// still finishes; it only logs.
func (v *views) schedulePreviewIfNeeded(home, photoPath, kind string) {
	if kind != "video" && kind != "heic" {
		return
	}
	if _, err := v.photos.Stat(home, previewName(photoPath)); err == nil {
		return
	}
	key := home + "\x00" + photoPath
	v.previewMu.Lock()
	if v.previewing == nil {
		v.previewing = map[string]bool{}
	}
	if v.previewing[key] {
		v.previewMu.Unlock()
		return
	}
	v.previewing[key] = true
	v.previewMu.Unlock()
	go func() {
		defer func() {
			v.previewMu.Lock()
			delete(v.previewing, key)
			v.previewMu.Unlock()
		}()
		v.previewSlots <- struct{}{}
		defer func() { <-v.previewSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := v.createPreview(home, photoPath, kind); err != nil {
			obs.Log(ctx, slog.LevelInfo, "async photo preview failed", "path", photoPath, "kind", kind, "error", err)
		}
	}()
}

func (v *views) servePhotoFile(w http.ResponseWriter, r *http.Request, home, name string, info files.File) {
	f, _, err := v.photos.Open(home, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("ETag", info.ETag())
	http.ServeContent(w, r, info.Name, info.ModTime, f)
}

// maxBulkDelete caps one delete request. Selecting a whole month is fine;
// a runaway client can't walk the entire library in a single call.
const maxBulkDelete = 200

// galleryDelete removes one or more photos. The form may repeat `path`
// (multi-select); tag rows and album memberships for each removed file go
// with it, otherwise the sidebar keeps counting photos that are gone.
func (v *views) galleryDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) {
		return
	}
	home := files.HomeDir(user.Email)
	if err := r.ParseForm(); err != nil {
		driveRedirect(w, r, "", "")
		return
	}
	seen := map[string]bool{}
	var names []string
	for _, raw := range r.Form["path"] {
		name := cleanDrivePath(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) >= maxBulkDelete {
			break
		}
	}
	if len(names) == 0 {
		if wantsJSON(r) {
			writeJSONError(w, http.StatusBadRequest, "nothing to delete")
			return
		}
		driveRedirect(w, r, "", "")
		return
	}

	var memberships map[string][]uuid.UUID
	if v.albumStore != nil {
		memberships, _ = v.albumStore.Memberships(r.Context(), user.ID)
	}
	deleted, failed := 0, 0
	for _, name := range names {
		if k := kindOfPhoto(name); k == "heic" || k == "video" {
			_ = v.photos.Remove(home, previewName(name))
		}
		if err := v.photos.Remove(home, name); err != nil {
			obs.Log(r.Context(), slog.LevelError, "delete photo failed", "path", name, "error", err)
			failed++
			continue
		}
		deleted++
		// Best effort: the file is already gone, so a failed cleanup must
		// not fail the request. Worst case a stale row inflates a count.
		if v.tagStore != nil {
			_ = v.tagStore.Set(r.Context(), user.ID, name, nil)
		}
		if v.albumStore != nil {
			for _, albumID := range memberships[name] {
				_ = v.albumStore.Remove(r.Context(), user.ID, albumID, name)
			}
		}
	}

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"deleted": deleted, "failed": failed})
		return
	}
	if deleted == 0 {
		http.Redirect(w, r, "/gallery?error=delete", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// maxAlbumName caps album names at a short title.
const maxAlbumName = 60

// albumCreate makes an album and lands on its (empty) filtered view.
// HTMX posts get the content fragment with the new filter pushed to the URL.
func (v *views) albumCreate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) || v.albumStore == nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if runes := []rune(name); len(runes) > maxAlbumName {
		name = string(runes[:maxAlbumName])
	}
	if name == "" {
		if isHX(r) {
			v.galleryFragment(w, r, user, "Could not manage that album.", "", "")
			return
		}
		http.Redirect(w, r, "/gallery?error=album", http.StatusSeeOther)
		return
	}
	album, err := v.albumStore.Create(r.Context(), user.ID, name)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "create album failed", "error", err)
		if isHX(r) {
			v.galleryFragment(w, r, user, "Could not manage that album.", "", "")
			return
		}
		http.Redirect(w, r, "/gallery?error=album", http.StatusSeeOther)
		return
	}
	if isHX(r) {
		w.Header().Set("HX-Push-Url", "/gallery?album="+album.ID.String())
		v.galleryFragment(w, r, user, "", album.ID.String(), "")
		return
	}
	http.Redirect(w, r, "/gallery?album="+album.ID.String(), http.StatusSeeOther)
}

// albumDelete removes an album (photos stay in the library).
func (v *views) albumDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) || v.albumStore == nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	if id, err := uuid.Parse(strings.TrimSpace(r.FormValue("id"))); err == nil {
		_ = v.albumStore.Delete(r.Context(), user.ID, id)
	}
	if isHX(r) {
		w.Header().Set("HX-Push-Url", "/gallery")
		v.galleryFragment(w, r, user, "", "", "")
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// albumToggle adds or removes one photo from one album.
// The modal posts JSON; plain form posts fall back to the gallery.
func (v *views) albumToggle(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) || v.albumStore == nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	albumID, err := uuid.Parse(strings.TrimSpace(r.FormValue("album_id")))
	if err != nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	home := files.HomeDir(user.Email)
	// `path` may repeat: dragging a multi-selection files them in one call.
	seen := map[string]bool{}
	var names []string
	for _, raw := range r.Form["path"] {
		name := cleanDrivePath(raw)
		if name == "" || seen[name] {
			continue
		}
		if info, err := v.photos.Stat(home, name); err != nil || info.IsDir {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) >= maxBulkDelete {
			break
		}
	}
	if len(names) == 0 {
		if wantsJSON(r) {
			writeJSONError(w, http.StatusBadRequest, "no photos to file")
			return
		}
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	add := r.FormValue("add") == "1"
	for _, name := range names {
		if add {
			err = v.albumStore.Add(r.Context(), user.ID, albumID, name)
		} else {
			err = v.albumStore.Remove(r.Context(), user.ID, albumID, name)
		}
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "toggle album failed", "path", name, "error", err)
			if wantsJSON(r) {
				writeJSONError(w, http.StatusInternalServerError, "could not update the album")
				return
			}
			http.Redirect(w, r, "/gallery?error=album", http.StatusSeeOther)
			return
		}
	}
	if wantsJSON(r) {
		paths, _ := v.albumStore.Paths(r.Context(), user.ID, albumID)
		inAlbum := map[string]bool{}
		for _, p := range paths {
			if seen[p] {
				inAlbum[p] = true
			}
		}
		w.Header().Set("Content-Type", "application/json")
		// `in_album` keeps the single-photo modal checkbox contract.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"in_album": len(names) == 1 && inAlbum[names[0]],
			"updated":  len(names),
		})
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// maxPhotoTags caps the tag editor: enough to organize, too few to spam.
const maxPhotoTags = 10

// maxPhotoTag caps a single tag at a short word, not a sentence.
const maxPhotoTag = 40

// photoTags replaces one photo's whole tag set. The gallery modal posts
// ?format=json; plain form posts fall back to the gallery itself.
func (v *views) photoTags(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) {
		return
	}
	home := files.HomeDir(user.Email)
	name := cleanDrivePath(r.FormValue("path"))
	if name == "" || v.tagStore == nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	if info, err := v.photos.Stat(home, name); err != nil || info.IsDir {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	tags := normalizeTags(r.FormValue("tags"))
	if err := v.tagStore.Set(r.Context(), user.ID, name, tags); err != nil {
		obs.Log(r.Context(), slog.LevelError, "save photo tags failed", "path", name, "error", err)
		if wantsJSON(r) {
			writeJSONError(w, http.StatusInternalServerError, "could not save those tags")
			return
		}
		http.Redirect(w, r, "/gallery?error=tags", http.StatusSeeOther)
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]string{"tags": tags})
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// normalizeTags splits on commas, trims, drops empties and dupes
// (case-insensitive, first spelling wins), and enforces the caps.
func normalizeTags(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if runes := []rune(t); len(runes) > maxPhotoTag {
			t = string(runes[:maxPhotoTag])
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
		if len(out) >= maxPhotoTags {
			break
		}
	}
	return out
}

type uploadResult struct {
	Uploaded []uploadEntry `json:"uploaded"`
	Failed   []uploadError `json:"failed"`
}

type uploadEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type uploadError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// photoUpload ingests camera uploads from Shortcuts and friends, and doubles
// as the gallery form target. Auth is Basic (app password friendly) when an
// Authorization header is present, otherwise the session plus CSRF token.
func (v *views) photoUpload(w http.ResponseWriter, r *http.Request) {
	if !v.requirePhotos(w) {
		return
	}
	var user *identity.User
	usingBasic := false
	if login, pass, ok := r.BasicAuth(); ok {
		usingBasic = true
		if v.photoAuth == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		u, err := v.photoAuth.Authenticate(r.Context(), login, pass)
		if err != nil || u == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="photos"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		user = u
	} else {
		user = v.sessionUser(r)
		if user == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !validCSRF(r) {
			writeJSONError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
	}
	home := files.HomeDir(user.Email)
	if err := v.photos.EnsureUserRoot(home); err != nil {
		http.Error(w, "photo storage unavailable", http.StatusInternalServerError)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoUploadBytes)
	if err := r.ParseMultipartForm(maxPhotoUploadBytes); err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "photo upload too large", "error", err)
		v.uploadFailed(w, r, usingBasic, "request too large")
		return
	}
	month := time.Now().UTC().Format("2006-01")
	var result uploadResult
	if r.MultipartForm != nil {
		for _, headers := range r.MultipartForm.File {
			for _, fh := range headers {
				name := sanitizeUploadName(fh.Filename)
				if name == "" {
					continue
				}
				f, err := fh.Open()
				if err != nil {
					result.Failed = append(result.Failed, uploadError{Name: fh.Filename, Error: "cannot read file"})
					continue
				}
				head := make([]byte, 512)
				n, _ := io.ReadFull(f, head)
				head = head[:n]
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					_ = f.Close()
					result.Failed = append(result.Failed, uploadError{Name: name, Error: "cannot read file"})
					continue
				}
				if !isMedia(name, head) {
					_ = f.Close()
					result.Failed = append(result.Failed, uploadError{Name: name, Error: "not a photo or video"})
					continue
				}
				target := v.uniquePhotoPath(home, month, friendlyUploadName(name))
				if err := v.photos.Write(home, target, f, fh.Size); err != nil {
					_ = f.Close()
					result.Failed = append(result.Failed, uploadError{Name: name, Error: "cannot store file"})
					continue
				}
				_ = f.Close()
				if kindOfPhoto(target) == "video" {
					v.schedulePreviewIfNeeded(home, target, "video")
				}
				result.Uploaded = append(result.Uploaded, uploadEntry{Name: path.Base(target), Path: target, Size: fh.Size})
			}
		}
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if len(result.Failed) > 0 || len(result.Uploaded) == 0 {
		if usingBasic || wantsJSON(r) {
			v.uploadFailed(w, r, usingBasic, "request failed")
			return
		}
		if isHX(r) {
			v.galleryFragment(w, r, user, "Upload failed. Files are limited to 1 GB each within your 10 GB quota.", hxFilter(r, "album"), hxFilter(r, "tag"))
			return
		}
		http.Redirect(w, r, "/gallery?error=upload", http.StatusSeeOther)
		return
	}
	if usingBasic || wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if isHX(r) {
		v.galleryFragment(w, r, user, "", hxFilter(r, "album"), hxFilter(r, "tag"))
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// hxFilter carries the sidebar filter through HTMX posts: gallery.js injects
// the current album/tag from the URL so a refresh keeps the view.
func hxFilter(r *http.Request, key string) string {
	return strings.TrimSpace(r.FormValue(key))
}

// uniquePhotoPath avoids overwriting: photo.jpg, photo-1.jpg, ...
func (v *views) uniquePhotoPath(home, month, name string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		target := month + "/" + candidate
		if _, err := v.photos.Stat(home, target); err != nil {
			return target
		}
		if i > 999 {
			return target
		}
	}
}

func (v *views) uploadFailed(w http.ResponseWriter, r *http.Request, usingBasic bool, msg string) {
	if usingBasic || wantsJSON(r) {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	http.Redirect(w, r, "/gallery?error=upload", http.StatusSeeOther)
}

func wantsJSON(r *http.Request) bool {
	if r.URL.Query().Get("format") == "json" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// sessionUser resolves the session cookie without middleware, for the
// dual-auth upload endpoint.
func (v *views) sessionUser(r *http.Request) *identity.User {
	if v.sessions == nil || v.users == nil {
		return nil
	}
	token := sessionToken(r)
	if token == "" {
		return nil
	}
	sess, err := v.sessions.GetByToken(r.Context(), token)
	if err != nil || sess == nil {
		return nil
	}
	user, err := v.users.Get(r.Context(), sess.UserID)
	if err != nil {
		return nil
	}
	return user
}

// friendlyUploadName replaces bare UUID filenames with timestamped ones.
// iOS shares converted photos under their asset UUID (c97b1595-…jpeg) and
// the original IMG_ name never reaches us, so photo-20260917-120530.jpeg is
// the kindest name we can mint. Real filenames pass through untouched.
func friendlyUploadName(name string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if !isUUID(stem) {
		return name
	}
	return "photo-" + time.Now().UTC().Format("20060102-150405") + strings.ToLower(ext)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
func sanitizeUploadName(name string) string {
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	if len(name) > 200 {
		ext := path.Ext(name)
		name = name[:200-len(ext)] + ext
	}
	return name
}

// validCSRF mirrors RequireCSRF as a boolean for handlers that branch on
// auth method (Basic clients carry no cookies, so no token is needed).
func validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.FormValue("_csrf")
	}
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1
}
