package vfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Common VFS errors.
var (
	ErrNotFound         = errors.New("file or directory not found")
	ErrAlreadyExists    = errors.New("file or directory already exists")
	ErrNotADirectory    = errors.New("not a directory")
	ErrIsADirectory     = errors.New("is a directory")
	ErrPermissionDenied = errors.New("permission denied")
)

// FileInfo provides metadata about a virtual file or directory.
type FileInfo struct {
	Path        string
	Name        string
	Size        int64
	IsDir       bool
	ModTime     time.Time
	ContentType string
	Metadata    map[string]string
}

// FileSystem defines the standard interface for virtual file systems.
type FileSystem interface {
	// OpenFile provides the capability-aware file contract for all new code.
	// Legacy convenience methods below remain temporarily while callers migrate.
	OpenFileSystem
	// Open opens the named file for reading.
	Open(p string) (io.ReadCloser, error)
	// Create creates or truncates the named file for writing.
	Create(p string) (io.WriteCloser, error)
	// Read reads the entire file content.
	Read(p string) ([]byte, error)
	// Write writes the byte content to the named file.
	Write(p string, data []byte) error
	// Stat returns metadata about the file or directory.
	Stat(p string) (*FileInfo, error)
	// ReadDir reads the directory named by path and returns a list of directory entries.
	ReadDir(p string) ([]*FileInfo, error)
	// MkdirAll creates a directory named path, along with any necessary parents.
	MkdirAll(p string) error
	// Remove removes the named file or empty directory.
	Remove(p string) error
	// RemoveAll removes path and any children it contains.
	RemoveAll(p string) error
	// Rename renames (moves) oldPath to newPath.
	Rename(oldPath, newPath string) error
	// Exists checks if the named path exists.
	Exists(p string) bool
	// Walk walks the file tree rooted at root, calling fn for each file or directory in the tree.
	Walk(root string, fn func(p string, info *FileInfo, err error) error) error
}

// NormalizePath cleans and ensures forward slash and leading slash for VFS paths.
func NormalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean("/" + p)
	return p
}

// --- MemFS: In-Memory Thread-Safe Virtual File System ---

type memNode struct {
	isDir   bool
	modTime time.Time
	data    []byte
}

// MemFS is a thread-safe in-memory virtual file system.
type MemFS struct {
	mu    sync.RWMutex
	nodes map[string]*memNode
}

// NewMemFS creates a new in-memory file system.
func NewMemFS() *MemFS {
	m := &MemFS{
		nodes: make(map[string]*memNode),
	}
	m.nodes["/"] = &memNode{
		isDir:   true,
		modTime: time.Now(),
	}
	return m
}

func (m *MemFS) Open(p string) (io.ReadCloser, error) {
	p = NormalizePath(p)
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, ok := m.nodes[p]
	if !ok {
		return nil, ErrNotFound
	}
	if node.isDir {
		return nil, ErrIsADirectory
	}
	return io.NopCloser(bytes.NewReader(node.data)), nil
}

// Capabilities reports that memory files support native random access and mutation.
func (m *MemFS) Capabilities() Capabilities {
	return Capabilities(CapStreamRead | CapRangeRead | CapSeek | CapStreamWrite | CapRandomWrite | CapTruncate | CapAtomicRename | CapResumableRead | CapResumableWrite)
}

// OpenFile opens a context-aware memory file handle without buffering the whole
// object for partial writes.
func (m *MemFS) OpenFile(ctx context.Context, p string, options OpenOptions) (File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p = NormalizePath(p)
	if !options.Read && !options.Write {
		return nil, errors.New("open requires read or write access")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	node, exists := m.nodes[p]
	if options.Exclusive && options.Create && exists {
		return nil, ErrAlreadyExists
	}
	if !exists {
		if !options.Create {
			return nil, ErrNotFound
		}
		if err := m.mkdirAllInternal(path.Dir(p)); err != nil {
			return nil, err
		}
		node = &memNode{modTime: time.Now()}
		m.nodes[p] = node
	}
	if node.isDir {
		return nil, ErrIsADirectory
	}
	if options.Truncate {
		node.data = nil
		node.modTime = time.Now()
	}
	file := &memFile{fs: m, path: p, readable: options.Read, writable: options.Write}
	if options.Append {
		file.offset = int64(len(node.data))
	}
	return file, nil
}

type memWriteCloser struct {
	buf  *bytes.Buffer
	path string
	m    *MemFS
}

func (w *memWriteCloser) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

func (w *memWriteCloser) Close() error {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	w.m.nodes[w.path] = &memNode{
		isDir:   false,
		modTime: time.Now(),
		data:    w.buf.Bytes(),
	}
	return nil
}

func (m *MemFS) Create(p string) (io.WriteCloser, error) {
	p = NormalizePath(p)
	parent := NormalizePath(path.Dir(p))

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.mkdirAllInternal(parent); err != nil {
		return nil, err
	}

	return &memWriteCloser{
		buf:  new(bytes.Buffer),
		path: p,
		m:    m,
	}, nil
}

func (m *MemFS) Read(p string) ([]byte, error) {
	rc, err := m.Open(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (m *MemFS) Write(p string, data []byte) error {
	wc, err := m.Create(p)
	if err != nil {
		return err
	}
	if _, err := wc.Write(data); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

func (m *MemFS) Stat(p string) (*FileInfo, error) {
	p = NormalizePath(p)
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, ok := m.nodes[p]
	if !ok {
		return nil, ErrNotFound
	}

	return &FileInfo{
		Path:    p,
		Name:    path.Base(p),
		Size:    int64(len(node.data)),
		IsDir:   node.isDir,
		ModTime: node.modTime,
	}, nil
}

func (m *MemFS) ReadDir(p string) ([]*FileInfo, error) {
	p = NormalizePath(p)
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, ok := m.nodes[p]
	if !ok {
		return nil, ErrNotFound
	}
	if !node.isDir {
		return nil, ErrNotADirectory
	}

	var results []*FileInfo
	prefix := p
	if prefix != "/" {
		prefix += "/"
	}

	for k, n := range m.nodes {
		if k == p || !strings.HasPrefix(k, prefix) {
			continue
		}
		rel := strings.TrimPrefix(k, prefix)
		if !strings.Contains(rel, "/") {
			results = append(results, &FileInfo{
				Path:    k,
				Name:    path.Base(k),
				Size:    int64(len(n.data)),
				IsDir:   n.isDir,
				ModTime: n.modTime,
			})
		}
	}
	return results, nil
}

func (m *MemFS) mkdirAllInternal(p string) error {
	p = NormalizePath(p)
	if p == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	current := ""
	for _, part := range parts {
		current += "/" + part
		node, ok := m.nodes[current]
		if ok && !node.isDir {
			return ErrNotADirectory
		}
		if !ok {
			m.nodes[current] = &memNode{
				isDir:   true,
				modTime: time.Now(),
			}
		}
	}
	return nil
}

func (m *MemFS) MkdirAll(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mkdirAllInternal(p)
}

func (m *MemFS) Remove(p string) error {
	p = NormalizePath(p)
	if p == "/" {
		return ErrPermissionDenied
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[p]
	if !ok {
		return ErrNotFound
	}
	if node.isDir {
		prefix := p + "/"
		for k := range m.nodes {
			if strings.HasPrefix(k, prefix) {
				return errors.New("directory not empty")
			}
		}
	}
	delete(m.nodes, p)
	return nil
}

func (m *MemFS) RemoveAll(p string) error {
	p = NormalizePath(p)
	if p == "/" {
		return ErrPermissionDenied
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := p + "/"
	for k := range m.nodes {
		if k == p || strings.HasPrefix(k, prefix) {
			delete(m.nodes, k)
		}
	}
	return nil
}

func (m *MemFS) Rename(oldPath, newPath string) error {
	oldPath = NormalizePath(oldPath)
	newPath = NormalizePath(newPath)
	if oldPath == "/" || newPath == "/" {
		return ErrPermissionDenied
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[oldPath]
	if !ok {
		return ErrNotFound
	}

	if node.isDir {
		oldPrefix := oldPath + "/"
		newPrefix := newPath + "/"
		updates := make(map[string]*memNode)
		for k, v := range m.nodes {
			if strings.HasPrefix(k, oldPrefix) {
				newKey := newPrefix + strings.TrimPrefix(k, oldPrefix)
				updates[newKey] = v
				delete(m.nodes, k)
			}
		}
		for k, v := range updates {
			m.nodes[k] = v
		}
	}

	delete(m.nodes, oldPath)
	m.nodes[newPath] = node
	return nil
}

func (m *MemFS) Exists(p string) bool {
	p = NormalizePath(p)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.nodes[p]
	return ok
}

func (m *MemFS) Walk(root string, fn func(p string, info *FileInfo, err error) error) error {
	root = NormalizePath(root)
	info, err := m.Stat(root)
	if err != nil {
		return fn(root, nil, err)
	}

	if err := fn(root, info, nil); err != nil {
		if errors.Is(err, fs.SkipDir) {
			return nil
		}
		return err
	}

	if !info.IsDir {
		return nil
	}

	entries, err := m.ReadDir(root)
	if err != nil {
		return fn(root, nil, err)
	}

	for _, entry := range entries {
		if err := m.Walk(entry.Path, fn); err != nil {
			return err
		}
	}
	return nil
}

// --- OSFS: Local Disk Virtual File System ---

// OSFS represents an OS filesystem bounded to a root directory.
type OSFS struct {
	rootDir string
}

// NewOSFS creates a new OS filesystem instance at rootDir.
func NewOSFS(rootDir string) *OSFS {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		abs = rootDir
	}
	return &OSFS{rootDir: abs}
}

func (o *OSFS) resolve(p string) string {
	clean := path.Clean("/" + NormalizePath(p))
	return filepath.Join(o.rootDir, filepath.FromSlash(clean))
}

func (o *OSFS) Open(p string) (io.ReadCloser, error) {
	return os.Open(o.resolve(p))
}

// Capabilities reports native local filesystem support.
func (o *OSFS) Capabilities() Capabilities {
	return Capabilities(CapStreamRead | CapRangeRead | CapSeek | CapStreamWrite | CapRandomWrite | CapTruncate | CapAtomicRename | CapResumableRead | CapResumableWrite)
}

// OpenFile opens a native OS handle with the requested semantics.
func (o *OSFS) OpenFile(ctx context.Context, p string, options OpenOptions) (File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !options.Read && !options.Write {
		return nil, errors.New("open requires read or write access")
	}
	flags := 0
	switch {
	case options.Read && options.Write:
		flags = os.O_RDWR
	case options.Write:
		flags = os.O_WRONLY
	default:
		flags = os.O_RDONLY
	}
	if options.Create {
		flags |= os.O_CREATE
	}
	if options.Truncate {
		flags |= os.O_TRUNC
	}
	if options.Append {
		flags |= os.O_APPEND
	}
	if options.Exclusive {
		flags |= os.O_EXCL
	}
	if options.Create {
		if err := os.MkdirAll(filepath.Dir(o.resolve(p)), 0755); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(o.resolve(p), flags, 0644)
}

func (o *OSFS) Create(p string) (io.WriteCloser, error) {
	full := o.resolve(p)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return nil, err
	}
	return os.Create(full)
}

func (o *OSFS) Read(p string) ([]byte, error) {
	return os.ReadFile(o.resolve(p))
}

func (o *OSFS) Write(p string, data []byte) error {
	full := o.resolve(p)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0644)
}

func (o *OSFS) Stat(p string) (*FileInfo, error) {
	full := o.resolve(p)
	fi, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &FileInfo{
		Path:    NormalizePath(p),
		Name:    fi.Name(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		ModTime: fi.ModTime(),
	}, nil
}

func (o *OSFS) ReadDir(p string) ([]*FileInfo, error) {
	full := o.resolve(p)
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}

	normPath := NormalizePath(p)
	var results []*FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		itemPath := normPath
		if itemPath != "/" {
			itemPath += "/" + entry.Name()
		} else {
			itemPath += entry.Name()
		}
		results = append(results, &FileInfo{
			Path:    itemPath,
			Name:    entry.Name(),
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			ModTime: info.ModTime(),
		})
	}
	return results, nil
}

func (o *OSFS) MkdirAll(p string) error {
	return os.MkdirAll(o.resolve(p), 0755)
}

func (o *OSFS) Remove(p string) error {
	return os.Remove(o.resolve(p))
}

func (o *OSFS) RemoveAll(p string) error {
	return os.RemoveAll(o.resolve(p))
}

func (o *OSFS) Rename(oldPath, newPath string) error {
	return os.Rename(o.resolve(oldPath), o.resolve(newPath))
}

func (o *OSFS) Exists(p string) bool {
	_, err := os.Stat(o.resolve(p))
	return err == nil
}

func (o *OSFS) Walk(root string, fn func(p string, info *FileInfo, err error) error) error {
	root = NormalizePath(root)
	fullRoot := o.resolve(root)
	return filepath.Walk(fullRoot, func(osPath string, info fs.FileInfo, err error) error {
		if err != nil {
			return fn(root, nil, err)
		}
		rel, relErr := filepath.Rel(o.rootDir, osPath)
		if relErr != nil {
			return relErr
		}
		vPath := NormalizePath(rel)
		fi := &FileInfo{
			Path:    vPath,
			Name:    info.Name(),
			Size:    info.Size(),
			IsDir:   info.IsDir(),
			ModTime: info.ModTime(),
		}
		return fn(vPath, fi, nil)
	})
}
