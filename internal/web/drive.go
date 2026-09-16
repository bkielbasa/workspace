package web

import (
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
)

// maxDriveUploadBytes caps a single browser upload request. The store
// enforces its own per-file and quota limits on top.
const maxDriveUploadBytes = 1<<30 + 16<<20

type driveFileItem struct {
	Name     string
	Path     string
	IsDir    bool
	Icon     string
	Size     int64
	HasSize  bool
	Modified string
}

type driveCrumb struct {
	Name string
	Path string
}

// cleanDrivePath normalizes the ?path= parameter to a virtual path.
// "" and "/" both mean the root; anything escaping stays inside anyway
// because the store jails every access.
func cleanDrivePath(raw string) string {
	clean := path.Clean("/" + strings.TrimSpace(raw))
	if clean == "/" {
		return ""
	}
	return strings.TrimPrefix(clean, "/")
}

func fileIcon(name string, isDir bool) string {
	if isDir {
		return "📁"
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".heic", ".avif", ".bmp":
		return "🖼"
	case ".mp3", ".ogg", ".wav", ".flac", ".m4a", ".opus":
		return "🎵"
	case ".mp4", ".mkv", ".webm", ".mov", ".avi":
		return "🎬"
	case ".pdf":
		return "📕"
	case ".zip", ".tar", ".gz", ".rar", ".7z", ".xz":
		return "📦"
	case ".md", ".txt", ".log", ".csv":
		return "📝"
	case ".ics":
		return "📅"
	case ".vcf":
		return "👤"
	default:
		return "📄"
	}
}

func driveCrumbs(current string) []driveCrumb {
	crumbs := []driveCrumb{{Name: "Files", Path: ""}}
	if current == "" {
		return crumbs
	}
	parts := strings.Split(current, "/")
	for i, p := range parts {
		crumbs = append(crumbs, driveCrumb{
			Name: p,
			Path: strings.Join(parts[:i+1], "/"),
		})
	}
	return crumbs
}

func (v *views) requireFiles(w http.ResponseWriter) bool {
	if v.files == nil {
		http.Error(w, "file storage is not configured", http.StatusNotImplemented)
		return false
	}
	return true
}

// ensureDriveRoot creates the user's tree on first use so empty states
// render instead of 404ing.
func (v *views) ensureDriveRoot(w http.ResponseWriter, user *identity.User) bool {
	if err := v.files.EnsureUserRoot(files.HomeDir(user.Email)); err != nil {
		http.Error(w, "file storage unavailable", http.StatusInternalServerError)
		return false
	}
	return true
}

func (v *views) drivePage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	if !v.requireFiles(w) {
		return
	}
	if !v.ensureDriveRoot(w, user) {
		return
	}
	current := cleanDrivePath(r.URL.Query().Get("path"))

	var info files.File
	if current == "" {
		info = files.File{IsDir: true}
	} else {
		var err error
		if info, err = v.files.Stat(files.HomeDir(user.Email), current); err != nil {
			http.NotFound(w, r)
			return
		}
		if !info.IsDir {
			v.serveDriveFile(w, r, user, current, info)
			return
		}
	}

	entries, err := v.files.ListDir(files.HomeDir(user.Email), current)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	items := make([]driveFileItem, 0, len(entries))
	for _, e := range entries {
		p := e.Name
		if current != "" {
			p = current + "/" + e.Name
		}
		items = append(items, driveFileItem{
			Name:     e.Name,
			Path:     p,
			IsDir:    e.IsDir,
			Icon:     fileIcon(e.Name, e.IsDir),
			Size:     e.Size,
			HasSize:  !e.IsDir,
			Modified: formatDetailDate(e.ModTime),
		})
	}

	renderView(w, r, v.driveT, "layout", viewData{
		Title:       "Files",
		Section:     "drive",
		User:        user,
		CSRFToken:   csrfTokenFromRequest(r),
		Wide:        true,
		DrivePath:   current,
		DriveCrumbs: driveCrumbs(current),
		DriveFiles:  items,
		Error:       driveFlash(r),
	})
}

// driveFlash carries one-shot upload errors via redirect query.
func driveFlash(r *http.Request) string {
	switch r.URL.Query().Get("error") {
	case "upload":
		return "Upload failed. Files are limited to 1 GB each within your 10 GB quota."
	case "mkdir":
		return "Could not create that folder."
	case "delete":
		return "Could not delete."
	case "rename":
		return "Could not rename. Names must stay inside the current folder."
	}
	return ""
}

func driveRedirect(w http.ResponseWriter, r *http.Request, current, errKind string) {
	target := "/drive"
	if current != "" {
		target += "?path=" + current
	}
	if errKind != "" {
		if strings.Contains(target, "?") {
			target += "&error=" + errKind
		} else {
			target += "?error=" + errKind
		}
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (v *views) serveDriveFile(w http.ResponseWriter, r *http.Request, user *identity.User, name string, info files.File) {
	f, _, err := v.files.Open(files.HomeDir(user.Email), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("ETag", info.ETag())
	http.ServeContent(w, r, info.Name, info.ModTime, f)
}

func (v *views) driveDownload(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requireFiles(w) {
		return
	}
	name := cleanDrivePath(r.URL.Query().Get("path"))
	if name == "" {
		http.NotFound(w, r)
		return
	}
	info, err := v.files.Stat(files.HomeDir(user.Email), name)
	if err != nil || info.IsDir {
		http.NotFound(w, r)
		return
	}
	v.serveDriveFile(w, r, user, name, info)
}

func (v *views) driveUpload(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requireFiles(w) {
		return
	}
	current := cleanDrivePath(r.FormValue("path"))
	if !v.ensureDriveRoot(w, user) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDriveUploadBytes)
	if err := r.ParseMultipartForm(maxDriveUploadBytes); err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "drive upload too large", "error", err)
		driveRedirect(w, r, current, "upload")
		return
	}
	if r.MultipartForm == nil {
		driveRedirect(w, r, current, "upload")
		return
	}
	failed := false
	for _, fh := range r.MultipartForm.File["files"] {
		if strings.TrimSpace(fh.Filename) == "" {
			continue
		}
		f, err := fh.Open()
		if err != nil {
			failed = true
			continue
		}
		name := path.Base(strings.TrimSpace(fh.Filename))
		if name == "" || name == "." || name == "/" {
			_ = f.Close()
			continue
		}
		target := name
		if current != "" {
			target = current + "/" + name
		}
		err = v.files.Write(files.HomeDir(user.Email), target, f, fh.Size)
		_ = f.Close()
		if err != nil {
			obs.Log(r.Context(), slog.LevelWarn, "drive upload failed", "file", name, "error", err)
			failed = true
		}
	}
	if failed {
		driveRedirect(w, r, current, "upload")
		return
	}
	driveRedirect(w, r, current, "")
}

func (v *views) driveMkdir(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requireFiles(w) {
		return
	}
	if !v.ensureDriveRoot(w, user) {
		return
	}
	current := cleanDrivePath(r.FormValue("path"))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || strings.ContainsAny(name, "/\\") {
		driveRedirect(w, r, current, "mkdir")
		return
	}
	target := name
	if current != "" {
		target = current + "/" + name
	}
	if err := v.files.Mkdir(files.HomeDir(user.Email), target); err != nil {
		driveRedirect(w, r, current, "mkdir")
		return
	}
	driveRedirect(w, r, current, "")
}

func (v *views) driveDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requireFiles(w) {
		return
	}
	name := cleanDrivePath(r.FormValue("path"))
	parent := path.Dir(name)
	if parent == "." {
		parent = ""
	}
	if name == "" {
		driveRedirect(w, r, "", "delete")
		return
	}
	if err := v.files.Remove(files.HomeDir(user.Email), name); err != nil {
		driveRedirect(w, r, parent, "delete")
		return
	}
	driveRedirect(w, r, parent, "")
}

func (v *views) driveRename(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !v.requireFiles(w) {
		return
	}
	name := cleanDrivePath(r.FormValue("path"))
	parent := path.Dir(name)
	if parent == "." {
		parent = ""
	}
	newName := strings.TrimSpace(r.FormValue("new_name"))
	if name == "" || newName == "" || strings.ContainsAny(newName, "/\\") {
		driveRedirect(w, r, parent, "rename")
		return
	}
	target := newName
	if parent != "" {
		target = parent + "/" + newName
	}
	// Stay in the same folder: no moves via rename.
	if path.Dir(target) != path.Dir(name) && !(parent == "" && path.Dir(target) == ".") {
		driveRedirect(w, r, parent, "rename")
		return
	}
	if err := v.files.Move(files.HomeDir(user.Email), name, target, false); err != nil {
		driveRedirect(w, r, parent, "rename")
		return
	}
	driveRedirect(w, r, parent, "")
}
