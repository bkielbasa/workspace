package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrExists        = errors.New("already exists")
	ErrConflict      = errors.New("parent missing")
	ErrTooLarge      = errors.New("file too large")
	ErrQuotaExceeded = errors.New("quota exceeded")
	ErrOutsideRoot   = errors.New("path escapes user root")
	ErrIsDir         = errors.New("is a directory")
)

// Defaults for quota enforcement. A zero Store quota means unlimited;
// per-file cap of zero means unlimited.
const (
	DefaultQuotaBytes   = 10 << 30
	DefaultMaxFileBytes = 1 << 30
)

// HomeDir maps a login email to its on-disk home directory name.
// Lowercase alphanumeric plus ._%@+- survive; everything else becomes _.
// The names double as Samba usernames and share paths, so they must stay
// stable and portable across WebDAV, the web UI, and SMB.
func HomeDir(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	var b strings.Builder
	for _, r := range email {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '%', r == '@', r == '+', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := strings.Trim(b.String(), "._")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		name = "user"
	}
	return name
}

// File describes one entry for PROPFIND responses.
type File struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// ETag is a weak entity tag derived from mtime and size.
func (f File) ETag() string {
	return fmt.Sprintf(`"%x-%x"`, f.ModTime.UnixNano(), f.Size)
}

// ContentType guesses the MIME type from the extension.
func (f File) ContentType() string {
	if f.IsDir {
		return "httpd/unix-directory"
	}
	if ct := mime.TypeByExtension(path.Ext(f.Name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// Store is a sandboxed filesystem rooted at root/<userID>/.
type Store struct {
	root        string
	quotaBytes  int64
	maxFileSize int64
}

// NewStore creates the root directory. Non-positive quota/max mean unlimited.
func NewStore(root string, quotaBytes, maxFileBytes int64) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("files: empty root")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("files: root: %w", err)
	}
	return &Store{root: root, quotaBytes: quotaBytes, maxFileSize: maxFileBytes}, nil
}

// resolve maps a virtual path to the filesystem, rejecting escapes.
// Virtual paths are slash-separated, absolute or relative; "" means root.
func (s *Store) resolve(home string, name string) (string, error) {
	clean := path.Clean("/" + strings.TrimSpace(name))
	rel := strings.TrimPrefix(clean, "/")
	full := filepath.Join(s.root, home, filepath.FromSlash(rel))
	base := filepath.Join(s.root, home)
	if full != base && !strings.HasPrefix(full, base+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	return full, nil
}

func (s *Store) userRoot(home string) string {
	return filepath.Join(s.root, home)
}

// EnsureUserRoot creates the user's tree on first authenticated use.
func (s *Store) EnsureUserRoot(home string) error {
	if err := os.MkdirAll(s.userRoot(home), 0o755); err != nil {
		return fmt.Errorf("files: user root: %w", err)
	}
	return nil
}

func statFile(full, name string, info fs.FileInfo) File {
	return File{Name: name, IsDir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime().UTC()}
}

// Stat returns one entry, following the WebDAV notion that "" is the root.
func (s *Store) Stat(home string, name string) (File, error) {
	full, err := s.resolve(home, name)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, ErrNotFound
		}
		return File{}, err
	}
	base := path.Base(strings.TrimSuffix(path.Clean("/"+name), "/"))
	if base == "/" || base == "." {
		base = ""
	}
	return statFile(full, base, info), nil
}

// ListDir lists a collection's children sorted by name.
func (s *Store) ListDir(home string, name string) ([]File, error) {
	full, err := s.resolve(home, name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	out := make([]File, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, statFile("", e.Name(), info))
	}
	return out, nil
}

// Open returns a file for reading.
func (s *Store) Open(home string, name string) (io.ReadSeekCloser, File, error) {
	full, err := s.resolve(home, name)
	if err != nil {
		return nil, File{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, File{}, ErrNotFound
		}
		return nil, File{}, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, File{}, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, File{}, ErrIsDir
	}
	return f, statFile(full, path.Base(name), info), nil
}

// usage sums bytes under the user's root.
func (s *Store) usage(home string) (int64, error) {
	var total int64
	root := s.userRoot(home)
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}

// Quota reports bytes used and the configured limit (limited=false means
// unlimited, in which case callers should omit quota properties).
func (s *Store) Quota(home string) (used, total int64, limited bool, err error) {
	if s.quotaBytes <= 0 {
		used, err = s.usage(home)
		return used, 0, false, err
	}
	used, err = s.usage(home)
	return used, s.quotaBytes, true, err
}

// Write stores data atomically (temp file + rename), creating parents.
// A negative size means unknown length (chunked uploads): limits still
// apply, and quota is enforced after the write.
func (s *Store) Write(home string, name string, data io.Reader, size int64) error {
	if s.maxFileSize > 0 && size > s.maxFileSize {
		return ErrTooLarge
	}
	full, err := s.resolve(home, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if s.quotaBytes > 0 && size >= 0 {
		used, err := s.usage(home)
		if err != nil {
			return err
		}
		var old int64
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			old = info.Size()
		}
		if used-old+size > s.quotaBytes {
			return ErrQuotaExceeded
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	written, copyErr := io.Copy(tmp, data)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpName)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return closeErr
	}
	if s.maxFileSize > 0 && written > s.maxFileSize {
		_ = os.Remove(tmpName)
		return ErrTooLarge
	}
	if err := os.Rename(tmpName, full); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if s.quotaBytes > 0 && size < 0 {
		if used, err := s.usage(home); err != nil {
			return err
		} else if used > s.quotaBytes {
			_ = os.Remove(full)
			return ErrQuotaExceeded
		}
	}
	return nil
}

// Mkdir creates one collection level; missing parents are a conflict.
func (s *Store) Mkdir(home string, name string) error {
	full, err := s.resolve(home, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err == nil {
		return ErrExists
	}
	if err := os.Mkdir(full, 0o755); err != nil {
		if os.IsNotExist(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

// Remove deletes a file or a whole collection tree.
func (s *Store) Remove(home string, name string) error {
	full, err := s.resolve(home, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return os.RemoveAll(full)
}

// Move renames within the user's root.
func (s *Store) Move(home string, from, to string, overwrite bool) error {
	src, err := s.resolve(home, from)
	if err != nil {
		return err
	}
	dst, err := s.resolve(home, to)
	if err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		if !overwrite {
			return ErrExists
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Copy duplicates a file or tree within the user's root.
func (s *Store) Copy(home string, from, to string, overwrite bool) error {
	src, err := s.resolve(home, from)
	if err != nil {
		return err
	}
	dst, err := s.resolve(home, to)
	if err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		if !overwrite {
			return ErrExists
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	return copyDir(src, dst)
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if err := copyFile(s, d, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}
