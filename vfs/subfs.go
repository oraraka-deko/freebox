package vfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
)

// SubFS creates a scoped sub-tree FileSystem rooted at rootPath within parent.
// Operations on "/" in SubFS correspond to rootPath on the parent filesystem.
type SubFS struct {
	parent   FileSystem
	rootPath string
}

// NewSubFS wraps parent filesystem so that rootPath acts as the virtual root ("/").
func NewSubFS(parent FileSystem, rootPath string) *SubFS {
	rootPath = NormalizePath(rootPath)
	return &SubFS{
		parent:   parent,
		rootPath: rootPath,
	}
}

// Root returns the base sub-path on the parent filesystem.
func (s *SubFS) Root() string {
	return s.rootPath
}

// Parent returns the underlying parent filesystem.
func (s *SubFS) Parent() FileSystem {
	return s.parent
}

// toParent converts a SubFS path into a parent filesystem path.
func (s *SubFS) toParent(p string) string {
	clean := NormalizePath(p)
	if clean == "/" {
		return s.rootPath
	}
	if s.rootPath == "/" {
		return clean
	}
	return NormalizePath(s.rootPath + "/" + clean)
}

// fromParent converts a parent filesystem path into a SubFS-relative path.
func (s *SubFS) fromParent(p string) string {
	clean := NormalizePath(p)
	if clean == s.rootPath {
		return "/"
	}
	rel := strings.TrimPrefix(clean, s.rootPath)
	return NormalizePath(rel)
}

func (s *SubFS) Open(p string) (io.ReadCloser, error) {
	return s.parent.Open(s.toParent(p))
}

func (s *SubFS) Create(p string) (io.WriteCloser, error) {
	return s.parent.Create(s.toParent(p))
}

func (s *SubFS) Read(p string) ([]byte, error) {
	return s.parent.Read(s.toParent(p))
}

func (s *SubFS) Write(p string, data []byte) error {
	return s.parent.Write(s.toParent(p), data)
}

func (s *SubFS) Stat(p string) (*FileInfo, error) {
	info, err := s.parent.Stat(s.toParent(p))
	if err != nil {
		return nil, err
	}
	infoCopy := *info
	infoCopy.Path = NormalizePath(p)
	if infoCopy.Path == "/" {
		infoCopy.Name = "/"
	}
	return &infoCopy, nil
}

func (s *SubFS) ReadDir(p string) ([]*FileInfo, error) {
	entries, err := s.parent.ReadDir(s.toParent(p))
	if err != nil {
		return nil, err
	}
	results := make([]*FileInfo, len(entries))
	for i, e := range entries {
		eCopy := *e
		eCopy.Path = s.fromParent(e.Path)
		results[i] = &eCopy
	}
	return results, nil
}

func (s *SubFS) MkdirAll(p string) error {
	return s.parent.MkdirAll(s.toParent(p))
}

func (s *SubFS) Remove(p string) error {
	pClean := NormalizePath(p)
	if pClean == "/" {
		return ErrPermissionDenied
	}
	return s.parent.Remove(s.toParent(p))
}

func (s *SubFS) RemoveAll(p string) error {
	pClean := NormalizePath(p)
	if pClean == "/" {
		return ErrPermissionDenied
	}
	return s.parent.RemoveAll(s.toParent(p))
}

func (s *SubFS) Rename(oldPath, newPath string) error {
	return s.parent.Rename(s.toParent(oldPath), s.toParent(newPath))
}

func (s *SubFS) Exists(p string) bool {
	return s.parent.Exists(s.toParent(p))
}

func (s *SubFS) Walk(root string, fn func(p string, info *FileInfo, err error) error) error {
	parentRoot := s.toParent(root)
	return s.parent.Walk(parentRoot, func(p string, info *FileInfo, err error) error {
		if err != nil {
			return fn(s.fromParent(p), nil, err)
		}
		infoCopy := *info
		infoCopy.Path = s.fromParent(p)
		if infoCopy.Path == "/" {
			infoCopy.Name = "/"
		}
		err = fn(infoCopy.Path, &infoCopy, nil)
		if errors.Is(err, fs.SkipDir) {
			return fs.SkipDir
		}
		return err
	})
}

// Capabilities reports parent filesystem capabilities.
func (s *SubFS) Capabilities() Capabilities {
	return s.parent.Capabilities()
}

// OpenFile opens a capability-aware file handle mapped through the scoped root.
func (s *SubFS) OpenFile(ctx context.Context, p string, options OpenOptions) (File, error) {
	return s.parent.OpenFile(ctx, s.toParent(p), options)
}

// Ensure interface compliance
var (
	_ FileSystem     = (*SubFS)(nil)
	_ OpenFileSystem = (*SubFS)(nil)
	_                = path.Base
)

