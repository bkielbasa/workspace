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
	"net/url"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
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
	if !v.requirePhotos(w) {
		return
	}
	if !v.ensurePhotoRoot(w, user) {
		return
	}
	home := files.HomeDir(user.Email)

	entries, err := v.photos.ListDir(home, "")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var months []photoMonth
	for _, e := range entries {
		if !e.IsDir || strings.HasPrefix(e.Name, ".") {
			continue
		}
		month := photoMonth{Name: e.Name, Path: e.Name}
		kids, err := v.photos.ListDir(home, e.Name)
		if err != nil {
			continue
		}
		sort.Slice(kids, func(i, j int) bool { return kids[i].Name < kids[j].Name })
		for _, k := range kids {
			if k.IsDir || strings.HasPrefix(k.Name, ".") {
				continue
			}
			kind := kindOfPhoto(k.Name)
			if kind == "file" {
				continue
			}
			item := photoFileItem{
				Name: k.Name, Path: e.Name + "/" + k.Name,
				Kind: kind, Size: k.Size,
				Modified: formatDetailDate(k.ModTime),
			}
			if kind == "heic" {
				if _, err := v.photos.Stat(home, previewName(item.Path)); err == nil {
					item.HasPreview = true
				}
			}
			month.Files = append(month.Files, item)
		}
		if len(month.Files) > 0 {
			months = append(months, month)
		}
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Name > months[j].Name })

	renderView(w, r, v.galleryT, "layout", viewData{
		Title:       "Photos",
		Section:     "photos",
		User:        user,
		CSRFToken:   csrfTokenFromRequest(r),
		Wide:        true,
		PhotoMonths: months,
		Error:       driveFlash(r),
	})
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
	if name == "" || kindOfPhoto(name) != "heic" {
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
	// Generate on demand and cache on disk.
	src, err := v.photos.LocalPath(home, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dst, err := v.photos.LocalPath(home, preview)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := os.MkdirAll(dirOf(dst), 0o755); err != nil {
		http.Error(w, "cannot render preview", http.StatusInternalServerError)
		return
	}
	conv := v.previewConv
	if conv == nil {
		conv = &execConverter{}
	}
	// The temp file needs a .jpg suffix: ImageMagick picks the output
	// format from the extension, and an extensionless temp file would
	// silently come back as HEIC.
	tmp, err := v.photos.LocalPath(home, preview+".tmp.jpg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := conv.Convert(src, tmp); err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "heic preview failed", "path", name, "error", err)
		http.NotFound(w, r)
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		http.Error(w, "cannot render preview", http.StatusInternalServerError)
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

func (v *views) galleryDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) {
		return
	}
	home := files.HomeDir(user.Email)
	name := cleanDrivePath(r.FormValue("path"))
	if name == "" {
		driveRedirect(w, r, "", "")
		return
	}
	if kindOfPhoto(name) == "heic" {
		_ = v.photos.Remove(home, previewName(name))
	}
	if err := v.photos.Remove(home, name); err != nil {
		http.Redirect(w, r, "/gallery?error=delete", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
}

// photoDetail carries one library item for the detail page.
type photoDetail struct {
	Name        string
	Path        string
	Kind        string // image, heic, video, file
	Size        int64
	Modified    time.Time
	Dims        string // "3024 × 4032", best effort, empty when unknown
	Label       string
	HasPreview  bool
	LabelsReady bool
}

// maxPhotoLabel caps labels at a short caption, not an essay.
const maxPhotoLabel = 140

// photoDetailPage renders one photo large with its metadata, label form and
// the future home of sharing settings.
func (v *views) photoDetailPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
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
	kind := kindOfPhoto(name)
	item := photoDetail{
		Name:        info.Name,
		Path:        name,
		Kind:        kind,
		Size:        info.Size,
		Modified:    info.ModTime,
		Dims:        photoDimensions(v.photos, home, name),
		LabelsReady: v.photoLabels != nil,
	}
	if kind == "heic" {
		if _, err := v.photos.Stat(home, previewName(name)); err == nil {
			item.HasPreview = true
		}
	}
	if v.photoLabels != nil {
		if label, err := v.photoLabels.Get(r.Context(), user.ID, name); err == nil {
			item.Label = label
		}
	}
	renderView(w, r, v.photoT, "layout", viewData{
		Title:       info.Name,
		Section:     "photos",
		User:        user,
		CSRFToken:   csrfTokenFromRequest(r),
		Wide:        true,
		Photo:       item,
		Error:       driveFlash(r),
	})
}

// photoLabel saves (or clears, when empty) the label of one photo.
func (v *views) photoLabel(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requirePhotos(w) {
		return
	}
	home := files.HomeDir(user.Email)
	name := cleanDrivePath(r.FormValue("path"))
	if name == "" || v.photoLabels == nil {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	if info, err := v.photos.Stat(home, name); err != nil || info.IsDir {
		http.Redirect(w, r, "/gallery", http.StatusSeeOther)
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if runes := []rune(label); len(runes) > maxPhotoLabel {
		label = string(runes[:maxPhotoLabel])
	}
	if err := v.photoLabels.Set(r.Context(), user.ID, name, label); err != nil {
		obs.Log(r.Context(), slog.LevelError, "save photo label failed", "path", name, "error", err)
		http.Redirect(w, r, "/gallery/photo?path="+url.QueryEscape(name)+"&error=label", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/gallery/photo?path="+url.QueryEscape(name), http.StatusSeeOther)
}

// photoDimensions reports pixel dimensions for directly decodable images.
// HEIC needs the external converter and stays unknown here; the preview
// pipeline, not this string, is what the gallery depends on.
func photoDimensions(store filesService, home, name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif":
	default:
		return ""
	}
	f, _, err := store.Open(home, name)
	if err != nil {
		return ""
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return ""
	}
	return fmt.Sprintf("%d × %d", cfg.Width, cfg.Height)
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
				target := v.uniquePhotoPath(home, month, name)
				if err := v.photos.Write(home, target, f, fh.Size); err != nil {
					_ = f.Close()
					result.Failed = append(result.Failed, uploadError{Name: name, Error: "cannot store file"})
					continue
				}
				_ = f.Close()
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
		http.Redirect(w, r, "/gallery?error=upload", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/gallery", http.StatusSeeOther)
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

// sanitizeUploadName keeps a safe basename or returns "".
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
