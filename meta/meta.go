package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"freebox/storage"
	"freebox/vfs"
)

// FilePermissions holds human-readable and numeric POSIX permission representation.
type FilePermissions struct {
	ModeNumeric int    `json:"mode_numeric"` // e.g. 0755, 0644
	ModeOctal   string `json:"mode_octal"`   // e.g. "0755"
	ModeString  string `json:"mode_string"`  // e.g. "-rwxr-xr-x"
	UserRead    bool   `json:"user_read"`
	UserWrite   bool   `json:"user_write"`
	UserExec    bool   `json:"user_exec"`
	GroupRead   bool   `json:"group_read"`
	GroupWrite  bool   `json:"group_write"`
	GroupExec   bool   `json:"group_exec"`
	OtherRead   bool   `json:"other_read"`
	OtherWrite  bool   `json:"other_write"`
	OtherExec   bool   `json:"other_exec"`
}

// DetailedFileInfo contains all standard and extended file attributes.
type DetailedFileInfo struct {
	Path         string            `json:"path"`
	Name         string            `json:"name"`
	Extension    string            `json:"extension"`
	Size         int64             `json:"size"`
	HumanSize    string            `json:"human_size"`
	IsDir        bool              `json:"is_dir"`
	MimeType     string            `json:"mime_type"`
	ModTime      time.Time         `json:"mod_time"`
	AccessTime   time.Time         `json:"access_time,omitempty"`
	ChangeTime   time.Time         `json:"change_time,omitempty"`
	Permissions  FilePermissions   `json:"permissions"`
	Owner        string            `json:"owner,omitempty"`
	Group        string            `json:"group,omitempty"`
	CustomMeta   map[string]string `json:"custom_metadata,omitempty"`
}

// Manager coordinates metadata extraction, permission modification, and custom tag persistence.
type Manager struct {
	db *storage.DB
	mu sync.RWMutex
}

// NewManager creates a new metadata manager.
func NewManager(db *storage.DB) *Manager {
	return &Manager{
		db: db,
	}
}

// ParsePermissions converts an os.FileMode or integer mode into FilePermissions.
func ParsePermissions(mode fs.FileMode) FilePermissions {
	perm := mode.Perm()
	m := int(perm)
	octal := fmt.Sprintf("%04o", m)
	str := mode.String()

	return FilePermissions{
		ModeNumeric: m,
		ModeOctal:   octal,
		ModeString:  str,
		UserRead:    m&0400 != 0,
		UserWrite:   m&0200 != 0,
		UserExec:    m&0100 != 0,
		GroupRead:   m&0040 != 0,
		GroupWrite:  m&0020 != 0,
		GroupExec:   m&0010 != 0,
		OtherRead:   m&0004 != 0,
		OtherWrite:  m&0002 != 0,
		OtherExec:   m&0001 != 0,
	}
}

// ParseOctalMode parses an octal string like "0755" or "755" to fs.FileMode.
func ParseOctalMode(octalStr string) (fs.FileMode, error) {
	octalStr = strings.TrimPrefix(octalStr, "0")
	val, err := strconv.ParseUint(octalStr, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid octal permissions '%s': %w", octalStr, err)
	}
	return fs.FileMode(val), nil
}

// FormatHumanSize formats bytes into readable units (KB, MB, GB, TB).
func FormatHumanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// DetectMimeType guesses MIME type from file extension or content header.
func DetectMimeType(filePath string, sample []byte) string {
	ext := path.Ext(filePath)
	if ext != "" {
		m := mime.TypeByExtension(ext)
		if m != "" {
			return m
		}
	}

	if len(sample) > 0 {
		return httpDetectContentType(sample)
	}

	return "application/octet-stream"
}

func httpDetectContentType(data []byte) string {
	if len(data) >= 4 && string(data[0:4]) == "%PDF" {
		return "application/pdf"
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		return "image/jpeg"
	}
	if len(data) >= 8 && string(data[0:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 4 && string(data[0:4]) == "GIF8" {
		return "image/gif"
	}
	if len(data) >= 4 && string(data[0:4]) == "PK\x03\x04" {
		return "application/zip"
	}
	return "application/octet-stream"
}

// GetInfo retrieves detailed metadata for a file across local or virtual file systems.
func (m *Manager) GetInfo(fsys vfs.FileSystem, filePath string) (*DetailedFileInfo, error) {
	filePath = vfs.NormalizePath(filePath)
	stat, err := fsys.Stat(filePath)
	if err != nil {
		return nil, err
	}

	info := &DetailedFileInfo{
		Path:        filePath,
		Name:        stat.Name,
		Extension:   path.Ext(stat.Name),
		Size:        stat.Size,
		HumanSize:   FormatHumanSize(stat.Size),
		IsDir:       stat.IsDir,
		ModTime:     stat.ModTime,
		MimeType:    stat.ContentType,
		Permissions: ParsePermissions(0644),
	}

	if info.MimeType == "" {
		info.MimeType = DetectMimeType(filePath, nil)
	}

	if stat.IsDir {
		info.Permissions = ParsePermissions(0755)
		info.MimeType = "inode/directory"
	}

	// Try reading custom metadata tags from database
	if m.db != nil {
		custom, _ := m.GetCustomMeta(filePath)
		info.CustomMeta = custom
	}

	return info, nil
}

// GetLocalDetailedInfo retrieves OS-specific permissions, times, and attributes for local files.
func (m *Manager) GetLocalDetailedInfo(localPath string) (*DetailedFileInfo, error) {
	fi, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}

	info := &DetailedFileInfo{
		Path:        localPath,
		Name:        fi.Name(),
		Extension:   filepath.Ext(fi.Name()),
		Size:        fi.Size(),
		HumanSize:   FormatHumanSize(fi.Size()),
		IsDir:       fi.IsDir(),
		ModTime:     fi.ModTime(),
		Permissions: ParsePermissions(fi.Mode()),
	}

	if fi.IsDir() {
		info.MimeType = "inode/directory"
	} else {
		sample := make([]byte, 512)
		if f, err := os.Open(localPath); err == nil {
			n, _ := f.Read(sample)
			f.Close()
			info.MimeType = DetectMimeType(localPath, sample[:n])
		} else {
			info.MimeType = DetectMimeType(localPath, nil)
		}
	}

	if m.db != nil {
		custom, _ := m.GetCustomMeta(localPath)
		info.CustomMeta = custom
	}

	return info, nil
}

// Chmod updates file permissions on local disk or virtual file system.
func (m *Manager) Chmod(localPath string, mode fs.FileMode) error {
	return os.Chmod(localPath, mode)
}

// Touch updates ModTime and AccessTime to now or given times.
func (m *Manager) Touch(localPath string, atime, mtime time.Time) error {
	now := time.Now()
	if atime.IsZero() {
		atime = now
	}
	if mtime.IsZero() {
		mtime = now
	}
	return os.Chtimes(localPath, atime, mtime)
}

// SetCustomMeta saves arbitrary key-value metadata for a file path.
func (m *Manager) SetCustomMeta(filePath, key, value string) error {
	if m.db == nil {
		return errors.New("database not available")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	filePath = vfs.NormalizePath(filePath)
	metaMap, _ := m.GetCustomMeta(filePath)
	if metaMap == nil {
		metaMap = make(map[string]string)
	}
	metaMap[key] = value

	data, err := json.Marshal(metaMap)
	if err != nil {
		return err
	}

	return m.db.PutEncrypted(storage.BucketMeta, filePath, data)
}

// GetCustomMeta retrieves all custom key-value tags for a file path.
func (m *Manager) GetCustomMeta(filePath string) (map[string]string, error) {
	if m.db == nil {
		return nil, errors.New("database not available")
	}

	filePath = vfs.NormalizePath(filePath)
	data, err := m.db.GetDecrypted(storage.BucketMeta, filePath)
	if err != nil {
		return nil, err
	}

	var metaMap map[string]string
	if err := json.Unmarshal(data, &metaMap); err != nil {
		return nil, err
	}

	return metaMap, nil
}

// DeleteCustomMeta removes a specific custom metadata key.
func (m *Manager) DeleteCustomMeta(filePath, key string) error {
	if m.db == nil {
		return errors.New("database not available")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	filePath = vfs.NormalizePath(filePath)
	metaMap, err := m.GetCustomMeta(filePath)
	if err != nil || metaMap == nil {
		return nil
	}

	delete(metaMap, key)
	if len(metaMap) == 0 {
		return m.db.Delete(storage.BucketMeta, filePath)
	}

	data, err := json.Marshal(metaMap)
	if err != nil {
		return err
	}

	return m.db.PutEncrypted(storage.BucketMeta, filePath, data)
}
