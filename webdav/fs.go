package webdav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"freebox/vfs"
)

// WebDAVFS implements vfs.FileSystem interface backed by a WebDAV client.
type WebDAVFS struct {
	client   *Client
	basePath string
	timeout  time.Duration
}

// NewWebDAVFS creates a new virtual filesystem backed by WebDAV.
func NewWebDAVFS(client *Client, basePath string) *WebDAVFS {
	if basePath == "" {
		basePath = "/"
	}
	return &WebDAVFS{
		client:   client,
		basePath: vfs.NormalizePath(basePath),
		timeout:  60 * time.Second,
	}
}

func (w *WebDAVFS) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), w.timeout)
}

func (w *WebDAVFS) resolvePath(p string) string {
	p = vfs.NormalizePath(p)
	if w.basePath == "" || w.basePath == "/" {
		return p
	}
	return path.Join(w.basePath, p)
}

// Capabilities reports supported operations for WebDAV.
func (w *WebDAVFS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities(vfs.CapStreamRead | vfs.CapStreamWrite | vfs.CapAtomicRename)
}

// OpenFile opens a file for reading.
func (w *WebDAVFS) OpenFile(ctx context.Context, p string, options vfs.OpenOptions) (vfs.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Write {
		return nil, vfs.ErrUnsupported
	}
	if !options.Read {
		return nil, errors.New("open requires read access")
	}
	rc, _, err := w.client.ReadFile(ctx, w.resolvePath(p))
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return nil, vfs.ErrNotFound
		}
		return nil, err
	}
	return rc, nil
}

// Open opens the named file for reading.
func (w *WebDAVFS) Open(p string) (io.ReadCloser, error) {
	ctx, cancel := w.ctx()
	defer cancel()
	rc, _, err := w.client.ReadFile(ctx, w.resolvePath(p))
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return nil, vfs.ErrNotFound
		}
		return nil, err
	}
	return rc, nil
}

type webdavWriteCloser struct {
	fs     *WebDAVFS
	path   string
	buffer *bytes.Buffer
}

func (c *webdavWriteCloser) Write(p []byte) (n int, err error) {
	return c.buffer.Write(p)
}

func (c *webdavWriteCloser) Close() error {
	return c.fs.Write(c.path, c.buffer.Bytes())
}

// Create creates a write closer that writes the file to WebDAV upon Close.
func (w *WebDAVFS) Create(p string) (io.WriteCloser, error) {
	return &webdavWriteCloser{
		fs:     w,
		path:   vfs.NormalizePath(p),
		buffer: new(bytes.Buffer),
	}, nil
}

// Read reads the entire file content.
func (w *WebDAVFS) Read(p string) ([]byte, error) {
	rc, err := w.Open(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Write writes bytes to a file on WebDAV (creating any missing parent directories).
func (w *WebDAVFS) Write(p string, data []byte) error {
	p = vfs.NormalizePath(p)
	resolved := w.resolvePath(p)
	dir := path.Dir(resolved)
	if dir != "" && dir != "/" && dir != "." {
		ctx, cancel := w.ctx()
		_ = w.client.MkDir(ctx, dir)
		cancel()
	}

	ctx, cancel := w.ctx()
	defer cancel()
	return w.client.WriteFile(ctx, resolved, bytes.NewReader(data))
}

// Stat returns metadata about the file or directory.
func (w *WebDAVFS) Stat(p string) (*vfs.FileInfo, error) {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return &vfs.FileInfo{
			Path:    "/",
			Name:    "/",
			IsDir:   true,
			ModTime: time.Now(),
		}, nil
	}

	ctx, cancel := w.ctx()
	defer cancel()

	resolved := w.resolvePath(p)
	info, err := w.client.Stat(ctx, resolved)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return nil, vfs.ErrNotFound
		}
		return nil, err
	}

	return &vfs.FileInfo{
		Path:    p,
		Name:    path.Base(p),
		Size:    info.Size,
		IsDir:   info.IsDir,
		ModTime: info.ModTime,
	}, nil
}

// ReadDir returns entries inside path.
func (w *WebDAVFS) ReadDir(p string) ([]*vfs.FileInfo, error) {
	p = vfs.NormalizePath(p)
	ctx, cancel := w.ctx()
	defer cancel()

	resolved := w.resolvePath(p)
	responses, err := w.client.ListDir(ctx, resolved)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return nil, vfs.ErrNotFound
		}
		return nil, fmt.Errorf("failed to read dir %s: %w", p, err)
	}

	var results []*vfs.FileInfo
	for _, resp := range responses {
		decodedHref, err := url.PathUnescape(resp.Href)
		if err != nil {
			decodedHref = resp.Href
		}
		name := path.Base(strings.TrimSuffix(decodedHref, "/"))
		if name == "" || name == "." || name == "/" {
			continue
		}

		isDir := resp.Propstat.Prop.ResourceType.IsCollection()
		var modTime time.Time
		if resp.Propstat.Prop.GetLastModified != "" {
			if pt, err := time.Parse(time.RFC1123, resp.Propstat.Prop.GetLastModified); err == nil {
				modTime = pt
			} else if pt, err := time.Parse(time.RFC1123Z, resp.Propstat.Prop.GetLastModified); err == nil {
				modTime = pt
			}
		}

		entryPath := vfs.NormalizePath(p + "/" + name)
		results = append(results, &vfs.FileInfo{
			Path:    entryPath,
			Name:    name,
			Size:    resp.Propstat.Prop.GetContentLength,
			IsDir:   isDir,
			ModTime: modTime,
		})
	}
	return results, nil
}

// MkdirAll creates a directory and any necessary parents.
func (w *WebDAVFS) MkdirAll(p string) error {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return nil
	}
	ctx, cancel := w.ctx()
	defer cancel()
	return w.client.MkDir(ctx, w.resolvePath(p))
}

// Remove deletes the file or folder.
func (w *WebDAVFS) Remove(p string) error {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return errors.New("cannot remove root directory")
	}
	ctx, cancel := w.ctx()
	defer cancel()
	return w.client.Delete(ctx, w.resolvePath(p))
}

// RemoveAll removes path and all its children.
func (w *WebDAVFS) RemoveAll(p string) error {
	return w.Remove(p)
}

// Rename moves or renames a file or directory.
func (w *WebDAVFS) Rename(oldPath, newPath string) error {
	oldPath = vfs.NormalizePath(oldPath)
	newPath = vfs.NormalizePath(newPath)
	resolvedOld := w.resolvePath(oldPath)
	resolvedNew := w.resolvePath(newPath)

	dirNew := path.Dir(resolvedNew)
	if dirNew != "" && dirNew != "/" && dirNew != "." {
		ctx, cancel := w.ctx()
		_ = w.client.MkDir(ctx, dirNew)
		cancel()
	}

	ctx, cancel := w.ctx()
	defer cancel()
	return w.client.Move(ctx, resolvedOld, resolvedNew)
}

// Exists checks if path exists on WebDAV.
func (w *WebDAVFS) Exists(p string) bool {
	_, err := w.Stat(p)
	return err == nil
}

// Walk recursively walks the directory tree rooted at root.
func (w *WebDAVFS) Walk(root string, fn func(p string, info *vfs.FileInfo, err error) error) error {
	root = vfs.NormalizePath(root)
	info, err := w.Stat(root)
	if err != nil {
		return fn(root, nil, err)
	}

	if err := fn(root, info, nil); err != nil {
		return err
	}

	if !info.IsDir {
		return nil
	}

	entries, err := w.ReadDir(root)
	if err != nil {
		return fn(root, nil, err)
	}

	for _, entry := range entries {
		if err := w.Walk(entry.Path, fn); err != nil {
			return err
		}
	}
	return nil
}

// Ensure interface compliance
var _ vfs.FileSystem = (*WebDAVFS)(nil)
var _ vfs.OpenFileSystem = (*WebDAVFS)(nil)
