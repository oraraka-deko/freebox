package vfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	fbHttp "freebox/http"
)

// WebDAVAdapter wraps an HTTP/WebDAV client to satisfy the FileSystem interface.
type WebDAVAdapter struct {
	client  *fbHttp.WebDAVClient
	timeout time.Duration
}

// NewWebDAVAdapter creates a new WebDAV-backed FileSystem.
func NewWebDAVAdapter(client *fbHttp.WebDAVClient) *WebDAVAdapter {
	return &WebDAVAdapter{
		client:  client,
		timeout: 30 * time.Second,
	}
}

func (a *WebDAVAdapter) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), a.timeout)
}

func (a *WebDAVAdapter) Open(p string) (io.ReadCloser, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	return a.client.Get(ctx, NormalizePath(p))
}

// Capabilities reports only operations WebDAV can currently perform without
// emulation. Range-read support is added only after a server validates 206
// responses; a plain WebDAV GET is a streaming reader.
func (a *WebDAVAdapter) Capabilities() Capabilities {
	return Capabilities(CapStreamRead | CapStreamWrite | CapAtomicRename)
}

// OpenFile is the context-aware streaming entry point. Random access is not
// exposed until the server has demonstrated RFC 7233 range support.
func (a *WebDAVAdapter) OpenFile(ctx context.Context, p string, options OpenOptions) (File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Write {
		return nil, ErrUnsupported
	}
	if !options.Read {
		return nil, errors.New("open requires read access")
	}
	return a.client.Get(ctx, NormalizePath(p))
}

type webdavWriteCloser struct {
	buf     *bytes.Buffer
	path    string
	adapter *WebDAVAdapter
}

func (w *webdavWriteCloser) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

func (w *webdavWriteCloser) Close() error {
	ctx, cancel := w.adapter.ctx()
	defer cancel()
	return w.adapter.client.Put(ctx, w.path, w.buf)
}

func (a *WebDAVAdapter) Create(p string) (io.WriteCloser, error) {
	return &webdavWriteCloser{
		buf:     new(bytes.Buffer),
		path:    NormalizePath(p),
		adapter: a,
	}, nil
}

func (a *WebDAVAdapter) Read(p string) ([]byte, error) {
	rc, err := a.Open(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (a *WebDAVAdapter) Write(p string, data []byte) error {
	ctx, cancel := a.ctx()
	defer cancel()
	return a.client.Put(ctx, NormalizePath(p), bytes.NewReader(data))
}

func (a *WebDAVAdapter) Stat(p string) (*FileInfo, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	p = NormalizePath(p)
	resources, err := a.client.Propfind(ctx, p, "0")
	if err != nil {
		return nil, fmt.Errorf("webdav stat failed: %w", err)
	}
	if len(resources) == 0 {
		return nil, ErrNotFound
	}
	res := resources[0]
	return &FileInfo{
		Path:    p,
		Name:    path.Base(p),
		Size:    res.Size,
		IsDir:   res.IsDir,
		ModTime: time.Now(),
	}, nil
}

func (a *WebDAVAdapter) ReadDir(p string) ([]*FileInfo, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	p = NormalizePath(p)
	resources, err := a.client.Propfind(ctx, p, "1")
	if err != nil {
		return nil, fmt.Errorf("webdav readdir failed: %w", err)
	}

	var results []*FileInfo
	for _, res := range resources {
		cleanHref := NormalizePath(res.Href)
		if cleanHref == p || cleanHref == p+"/" {
			continue
		}
		name := res.DisplayName
		if name == "" {
			name = path.Base(cleanHref)
		}
		results = append(results, &FileInfo{
			Path:    cleanHref,
			Name:    name,
			Size:    res.Size,
			IsDir:   res.IsDir,
			ModTime: time.Now(),
		})
	}
	return results, nil
}

func (a *WebDAVAdapter) MkdirAll(p string) error {
	ctx, cancel := a.ctx()
	defer cancel()
	p = NormalizePath(p)
	if p == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	curr := ""
	for _, part := range parts {
		curr += "/" + part
		_ = a.client.Mkcol(ctx, curr)
	}
	return nil
}

func (a *WebDAVAdapter) Remove(p string) error {
	ctx, cancel := a.ctx()
	defer cancel()
	return a.client.Delete(ctx, NormalizePath(p))
}

func (a *WebDAVAdapter) RemoveAll(p string) error {
	return a.Remove(p)
}

func (a *WebDAVAdapter) Rename(oldPath, newPath string) error {
	ctx, cancel := a.ctx()
	defer cancel()
	return a.client.Move(ctx, NormalizePath(oldPath), NormalizePath(newPath))
}

func (a *WebDAVAdapter) Exists(p string) bool {
	_, err := a.Stat(p)
	return err == nil
}

func (a *WebDAVAdapter) Walk(root string, fn func(p string, info *FileInfo, err error) error) error {
	root = NormalizePath(root)
	info, err := a.Stat(root)
	if err != nil {
		return fn(root, nil, err)
	}

	if err := fn(root, info, nil); err != nil {
		return err
	}

	if !info.IsDir {
		return nil
	}

	entries, err := a.ReadDir(root)
	if err != nil {
		return fn(root, nil, err)
	}

	for _, entry := range entries {
		if err := a.Walk(entry.Path, fn); err != nil {
			return err
		}
	}
	return nil
}

// Ensure interface compliance
var (
	_ FileSystem = (*MemFS)(nil)
	_ FileSystem = (*OSFS)(nil)
	_ FileSystem = (*WebDAVAdapter)(nil)
)

var (
	_ = errors.New
)
