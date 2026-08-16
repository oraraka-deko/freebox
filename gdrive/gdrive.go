package gdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"freebox/vfs"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

const (
	MimeFolder = "application/vnd.google-apps.folder"
)

// GDriveFS implements vfs.FileSystem interface backed by Google Drive v3 API.
type GDriveFS struct {
	srv        *drive.Service
	rootFolder string // Root folder ID (defaults to "root")
	pathCache  map[string]string // Maps normalized path to Drive File ID
	cacheMu    sync.RWMutex
	timeout    time.Duration
}

// NewGDriveFS creates a new virtual filesystem backed by Google Drive.
func NewGDriveFS(srv *drive.Service, rootFolderID string) *GDriveFS {
	if rootFolderID == "" {
		rootFolderID = "root"
	}
	fs := &GDriveFS{
		srv:        srv,
		rootFolder: rootFolderID,
		pathCache:  make(map[string]string),
		timeout:    60 * time.Second,
	}
	fs.pathCache["/"] = rootFolderID
	return fs
}

func (g *GDriveFS) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), g.timeout)
}

// resolvePath traverses directory hierarchy on Drive to resolve a path to its File ID.
func (g *GDriveFS) resolvePath(p string) (string, error) {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return g.rootFolder, nil
	}

	g.cacheMu.RLock()
	if id, ok := g.pathCache[p]; ok {
		g.cacheMu.RUnlock()
		return id, nil
	}
	g.cacheMu.RUnlock()

	parts := strings.Split(strings.Trim(p, "/"), "/")
	currentParentID := g.rootFolder
	currentPath := ""

	ctx, cancel := g.ctx()
	defer cancel()

	for _, part := range parts {
		currentPath += "/" + part

		g.cacheMu.RLock()
		cachedID, ok := g.pathCache[currentPath]
		g.cacheMu.RUnlock()

		if ok {
			currentParentID = cachedID
			continue
		}

		// Escape single quotes in filename query
		safeName := strings.ReplaceAll(part, "'", "\\'")
		query := fmt.Sprintf("'%s' in parents and name = '%s' and trashed = false", currentParentID, safeName)

		r, err := g.srv.Files.List().
			Context(ctx).
			Q(query).
			Fields("files(id, name, mimeType, trashed)").
			PageSize(10).
			Do()
		if err != nil {
			return "", fmt.Errorf("failed searching path %s: %w", currentPath, err)
		}

		if len(r.Files) == 0 {
			return "", vfs.ErrNotFound
		}

		fileID := r.Files[0].Id
		g.cacheMu.Lock()
		g.pathCache[currentPath] = fileID
		g.cacheMu.Unlock()

		currentParentID = fileID
	}

	return currentParentID, nil
}

// Open opens the named file for reading.
func (g *GDriveFS) Open(p string) (io.ReadCloser, error) {
	fileID, err := g.resolvePath(p)
	if err != nil {
		return nil, err
	}

	ctx, cancel := g.ctx()
	defer cancel()

	resp, err := g.srv.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("download error for %s: %w", p, err)
	}

	return resp.Body, nil
}

type gdriveWriteCloser struct {
	fs     *GDriveFS
	path   string
	buffer *bytes.Buffer
}

func (w *gdriveWriteCloser) Write(p []byte) (n int, err error) {
	return w.buffer.Write(p)
}

func (w *gdriveWriteCloser) Close() error {
	return w.fs.Write(w.path, w.buffer.Bytes())
}

// Create creates a write closer that uploads file to Drive upon Close.
func (g *GDriveFS) Create(p string) (io.WriteCloser, error) {
	p = vfs.NormalizePath(p)
	return &gdriveWriteCloser{
		fs:     g,
		path:   p,
		buffer: new(bytes.Buffer),
	}, nil
}

// Read reads the entire file content.
func (g *GDriveFS) Read(p string) ([]byte, error) {
	rc, err := g.Open(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Write writes bytes to a file on Google Drive (creating or updating existing).
func (g *GDriveFS) Write(p string, data []byte) error {
	p = vfs.NormalizePath(p)
	dir := path.Dir(p)
	base := path.Base(p)

	// Ensure parent folders exist
	parentID, err := g.resolvePath(dir)
	if err != nil {
		if errors.Is(err, vfs.ErrNotFound) {
			if err := g.MkdirAll(dir); err != nil {
				return err
			}
			parentID, err = g.resolvePath(dir)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	ctx, cancel := g.ctx()
	defer cancel()

	// Check if file already exists
	existingID, err := g.resolvePath(p)
	reader := bytes.NewReader(data)

	if err == nil && existingID != "" {
		// Update existing file content
		_, err = g.srv.Files.Update(existingID, nil).
			Context(ctx).
			Media(reader).
			Do()
		return err
	}

	// Create new file
	fileMeta := &drive.File{
		Name:    base,
		Parents: []string{parentID},
	}

	res, err := g.srv.Files.Create(fileMeta).
		Context(ctx).
		Media(reader).
		Fields("id, name, size").
		Do()
	if err != nil {
		return fmt.Errorf("failed creating file on drive: %w", err)
	}

	g.cacheMu.Lock()
	g.pathCache[p] = res.Id
	g.cacheMu.Unlock()

	return nil
}

// Stat returns metadata about the file or directory.
func (g *GDriveFS) Stat(p string) (*vfs.FileInfo, error) {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return &vfs.FileInfo{
			Path:    "/",
			Name:    "/",
			IsDir:   true,
			ModTime: time.Now(),
		}, nil
	}

	fileID, err := g.resolvePath(p)
	if err != nil {
		return nil, err
	}

	ctx, cancel := g.ctx()
	defer cancel()

	f, err := g.srv.Files.Get(fileID).
		Context(ctx).
		Fields("id, name, size, mimeType, modifiedTime, createdTime").
		Do()
	if err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) && gErr.Code == 404 {
			return nil, vfs.ErrNotFound
		}
		return nil, err
	}

	modTime := time.Now()
	if f.ModifiedTime != "" {
		if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
			modTime = t
		}
	}

	isDir := f.MimeType == MimeFolder

	return &vfs.FileInfo{
		Path:        p,
		Name:        f.Name,
		Size:        f.Size,
		IsDir:       isDir,
		ModTime:     modTime,
		ContentType: f.MimeType,
	}, nil
}

// ReadDir returns a list of entries inside path.
func (g *GDriveFS) ReadDir(p string) ([]*vfs.FileInfo, error) {
	p = vfs.NormalizePath(p)
	folderID, err := g.resolvePath(p)
	if err != nil {
		return nil, err
	}

	ctx, cancel := g.ctx()
	defer cancel()

	query := fmt.Sprintf("'%s' in parents and trashed = false", folderID)
	r, err := g.srv.Files.List().
		Context(ctx).
		Q(query).
		Fields("files(id, name, size, mimeType, modifiedTime)").
		PageSize(1000).
		Do()
	if err != nil {
		return nil, fmt.Errorf("failed listing directory %s: %w", p, err)
	}

	var results []*vfs.FileInfo
	for _, f := range r.Files {
		itemPath := vfs.NormalizePath(p + "/" + f.Name)
		isDir := f.MimeType == MimeFolder

		modTime := time.Now()
		if f.ModifiedTime != "" {
			if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
				modTime = t
			}
		}

		g.cacheMu.Lock()
		g.pathCache[itemPath] = f.Id
		g.cacheMu.Unlock()

		results = append(results, &vfs.FileInfo{
			Path:        itemPath,
			Name:        f.Name,
			Size:        f.Size,
			IsDir:       isDir,
			ModTime:     modTime,
			ContentType: f.MimeType,
		})
	}

	return results, nil
}

// MkdirAll creates a directory and any necessary parents.
func (g *GDriveFS) MkdirAll(p string) error {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return nil
	}

	parts := strings.Split(strings.Trim(p, "/"), "/")
	currentParentID := g.rootFolder
	currentPath := ""

	ctx, cancel := g.ctx()
	defer cancel()

	for _, part := range parts {
		currentPath += "/" + part

		g.cacheMu.RLock()
		cachedID, ok := g.pathCache[currentPath]
		g.cacheMu.RUnlock()

		if ok {
			currentParentID = cachedID
			continue
		}

		safeName := strings.ReplaceAll(part, "'", "\\'")
		query := fmt.Sprintf("'%s' in parents and name = '%s' and mimeType = '%s' and trashed = false", currentParentID, safeName, MimeFolder)

		r, err := g.srv.Files.List().
			Context(ctx).
			Q(query).
			Fields("files(id, name)").
			PageSize(1).
			Do()
		if err == nil && len(r.Files) > 0 {
			folderID := r.Files[0].Id
			g.cacheMu.Lock()
			g.pathCache[currentPath] = folderID
			g.cacheMu.Unlock()
			currentParentID = folderID
			continue
		}

		// Create folder
		folderMeta := &drive.File{
			Name:     part,
			MimeType: MimeFolder,
			Parents:  []string{currentParentID},
		}

		created, err := g.srv.Files.Create(folderMeta).
			Context(ctx).
			Fields("id").
			Do()
		if err != nil {
			return fmt.Errorf("failed creating folder %s: %w", currentPath, err)
		}

		g.cacheMu.Lock()
		g.pathCache[currentPath] = created.Id
		g.cacheMu.Unlock()

		currentParentID = created.Id
	}

	return nil
}

// Remove deletes the file or folder from Google Drive.
func (g *GDriveFS) Remove(p string) error {
	p = vfs.NormalizePath(p)
	fileID, err := g.resolvePath(p)
	if err != nil {
		return err
	}

	ctx, cancel := g.ctx()
	defer cancel()

	if err := g.srv.Files.Delete(fileID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed deleting %s: %w", p, err)
	}

	g.cacheMu.Lock()
	delete(g.pathCache, p)
	g.cacheMu.Unlock()

	return nil
}

// RemoveAll removes path and all its children.
func (g *GDriveFS) RemoveAll(p string) error {
	return g.Remove(p)
}

// Rename moves or renames a file/folder on Google Drive.
func (g *GDriveFS) Rename(oldPath, newPath string) error {
	oldPath = vfs.NormalizePath(oldPath)
	newPath = vfs.NormalizePath(newPath)

	fileID, err := g.resolvePath(oldPath)
	if err != nil {
		return err
	}

	oldDir := path.Dir(oldPath)
	newDir := path.Dir(newPath)
	newName := path.Base(newPath)

	ctx, cancel := g.ctx()
	defer cancel()

	call := g.srv.Files.Update(fileID, &drive.File{Name: newName}).Context(ctx)

	if oldDir != newDir {
		oldParentID, err := g.resolvePath(oldDir)
		if err != nil {
			return err
		}
		newParentID, err := g.resolvePath(newDir)
		if err != nil {
			return err
		}
		call = call.RemoveParents(oldParentID).AddParents(newParentID)
	}

	_, err = call.Do()
	if err != nil {
		return fmt.Errorf("failed renaming from %s to %s: %w", oldPath, newPath, err)
	}

	g.cacheMu.Lock()
	delete(g.pathCache, oldPath)
	g.pathCache[newPath] = fileID
	g.cacheMu.Unlock()

	return nil
}

// Exists checks if path exists on Drive.
func (g *GDriveFS) Exists(p string) bool {
	_, err := g.Stat(p)
	return err == nil
}

// Walk recursively walks the directory tree rooted at root.
func (g *GDriveFS) Walk(root string, fn func(p string, info *vfs.FileInfo, err error) error) error {
	root = vfs.NormalizePath(root)
	info, err := g.Stat(root)
	if err != nil {
		return fn(root, nil, err)
	}

	if err := fn(root, info, nil); err != nil {
		return err
	}

	if !info.IsDir {
		return nil
	}

	entries, err := g.ReadDir(root)
	if err != nil {
		return fn(root, nil, err)
	}

	for _, entry := range entries {
		if err := g.Walk(entry.Path, fn); err != nil {
			return err
		}
	}
	return nil
}

// Ensure interface compliance
var _ vfs.FileSystem = (*GDriveFS)(nil)
