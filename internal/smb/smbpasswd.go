// Package smb maintains the Samba user database (smbpasswd file) that the
// standalone file server authenticates against. Passwords arrive as NTLM
// hashes computed by the identity layer, which owns the plaintext; this
// package never sees one. The file lives on the shared data volume so the
// app (writer) and Samba (reader) share nothing else.
package smb

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// lmDisabled is the LANMAN hash placeholder: LANMAN auth stays off.
const lmDisabled = "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"

// Manager owns path/smbpasswd. All mutations are atomic (temp + rename)
// under an exclusive flock on path+".lock".
type Manager struct {
	path string
}

// NewManager ensures the parent directory exists. The file itself is
// created lazily on first write with mode 0600.
func NewManager(path string) (*Manager, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("smb: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("smb: mkdir: %w", err)
	}
	return &Manager{path: path}, nil
}

// uidFor derives a stable smbpasswd uid from the login name.
func uidFor(name string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(name)))
	return 10000 + h.Sum32()%50000
}

// daysSinceEpoch renders the smbpasswd LCT field.
func daysSinceEpoch(t time.Time) string {
	return fmt.Sprintf("LCT-%08X", uint32(t.Unix()/86400))
}

func withLock(path string, fn func() error) error {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("smb: lock open: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("smb: lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(string(data), "\n")
}

// mutate rewrites the file, replacing (or appending) name's record.
func (m *Manager) mutate(name, line string) error {
	return withLock(m.path, func() error {
		data, err := os.ReadFile(m.path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var out []string
		replaced := line == ""
		for _, l := range splitLines(data) {
			if l == "" {
				continue
			}
			if before, _, _ := strings.Cut(l, ":"); strings.EqualFold(before, name) {
				if line != "" {
					out = append(out, line)
				}
				replaced = true
				continue
			}
			out = append(out, l)
		}
		if !replaced {
			out = append(out, line)
		}
		tmp, err := os.CreateTemp(filepath.Dir(m.path), ".smbpasswd-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		_, werr := io.WriteString(tmp, strings.Join(out, "\n")+"\n")
		cerr := tmp.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(tmpName)
			if werr != nil {
				return werr
			}
			return cerr
		}
		if err := os.Chmod(tmpName, 0o600); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, m.path); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		return nil
	})
}

// SetPassword creates or updates a login, preserving an existing
// enabled/disabled flag and enabling fresh logins.
func (m *Manager) SetPassword(name, ntHash string) error {
	name = strings.TrimSpace(name)
	if name == "" || ntHash == "" {
		return fmt.Errorf("smb: name and hash required")
	}
	flags := "[U          ]"
	if data, err := os.ReadFile(m.path); err == nil {
		for _, l := range splitLines(data) {
			parts := strings.Split(l, ":")
			if len(parts) >= 5 && strings.EqualFold(parts[0], name) {
				flags = parts[4]
				break
			}
		}
	}
	line := fmt.Sprintf("%s:%d:%s:%s:%s:%s", name, uidFor(name), lmDisabled, ntHash, flags, daysSinceEpoch(time.Now()))
	return m.mutate(name, line)
}

// SetEnabled flips the account flag without touching the hash.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("smb: name required")
	}
	flags := "[U          ]"
	if !enabled {
		flags = "[D          ]"
	}
	found := false
	var ntHash string
	if data, err := os.ReadFile(m.path); err == nil {
		for _, l := range splitLines(data) {
			parts := strings.Split(l, ":")
			if len(parts) >= 5 && strings.EqualFold(parts[0], name) {
				found = true
				ntHash = parts[3]
				break
			}
		}
	}
	if !found {
		return fmt.Errorf("smb: unknown login %q", name)
	}
	_ = ntHash
	return withLock(m.path, func() error {
		data, err := os.ReadFile(m.path)
		if err != nil {
			return err
		}
		var out []string
		for _, l := range splitLines(data) {
			if l == "" {
				continue
			}
			parts := strings.SplitN(l, ":", 6)
			if len(parts) == 6 && strings.EqualFold(parts[0], name) {
				parts[4] = flags
				l = strings.Join(parts, ":")
			}
			out = append(out, l)
		}
		tmp, err := os.CreateTemp(filepath.Dir(m.path), ".smbpasswd-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		_, werr := io.WriteString(tmp, strings.Join(out, "\n")+"\n")
		cerr := tmp.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(tmpName)
			if werr != nil {
				return werr
			}
			return cerr
		}
		if err := os.Chmod(tmpName, 0o600); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, m.path); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		return nil
	})
}

// RemoveUser deletes the login.
func (m *Manager) RemoveUser(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("smb: name required")
	}
	return m.mutate(name, "")
}
