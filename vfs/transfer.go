package vfs

import (
	"fmt"
	"io"
	"path"
	"strings"
	"time"
)

// CollisionPolicy defines behavior when destination file already exists.
type CollisionPolicy int

const (
	// Overwrite existing destination files.
	Overwrite CollisionPolicy = iota
	// Skip existing files.
	Skip
	// Return an error on collision.
	ErrorOnCollision
	// AutoRename automatically appends numeric suffixes like "file (1).txt" to preserve both.
	AutoRename
	// KeepNewer overwrites only if the source file is newer than the destination file.
	KeepNewer
	// KeepLarger overwrites only if the source file size is larger than the destination file.
	KeepLarger
	// KeepSmaller overwrites only if the source file size is smaller than the destination file.
	KeepSmaller
)

// RenameResolver is a function that generates a new destination path given collision.
type RenameResolver func(srcPath, dstPath string) string

// TransferOptions configures file transfer behavior.
type TransferOptions struct {
	Policy         CollisionPolicy
	CustomResolver RenameResolver
	BufferSize     int
	OnProgress     func(p string, bytesCopied, totalBytes int64)
	OnItemDone     func(p string, err error)
}

// DefaultTransferOptions returns default transfer settings.
func DefaultTransferOptions() TransferOptions {
	return TransferOptions{
		Policy:     Overwrite,
		BufferSize: 64 * 1024,
	}
}

// GenerateAutoRenamePath generates a unique non-colliding path on fs by appending (1), (2), etc.
func GenerateAutoRenamePath(fs FileSystem, dstPath string) string {
	dstPath = NormalizePath(dstPath)
	if !fs.Exists(dstPath) {
		return dstPath
	}

	dir := path.Dir(dstPath)
	base := path.Base(dstPath)
	ext := path.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	counter := 1
	for {
		candidate := NormalizePath(fmt.Sprintf("%s/%s (%d)%s", dir, nameWithoutExt, counter, ext))
		if !fs.Exists(candidate) {
			return candidate
		}
		counter++
	}
}

// TransferEngine provides high-level copy and synchronization between virtual file systems.
type TransferEngine struct {
	opts TransferOptions
}

// NewTransferEngine creates a new transfer engine with options.
func NewTransferEngine(opts TransferOptions) *TransferEngine {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 64 * 1024
	}
	return &TransferEngine{opts: opts}
}

// CopyFile copies a single file from srcFS at srcPath to dstFS at dstPath.
func (e *TransferEngine) CopyFile(srcFS FileSystem, srcPath string, dstFS FileSystem, dstPath string) error {
	srcPath = NormalizePath(srcPath)
	dstPath = NormalizePath(dstPath)

	srcInfo, err := srcFS.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", srcPath, err)
	}
	if srcInfo.IsDir {
		return fmt.Errorf("source %s is a directory, use CopyTree instead", srcPath)
	}

	if dstFS.Exists(dstPath) {
		switch e.opts.Policy {
		case Skip:
			return nil
		case ErrorOnCollision:
			return fmt.Errorf("destination %s: %w", dstPath, ErrAlreadyExists)
		case AutoRename:
			dstPath = GenerateAutoRenamePath(dstFS, dstPath)
		case KeepNewer:
			dstInfo, err := dstFS.Stat(dstPath)
			if err == nil && !srcInfo.ModTime.After(dstInfo.ModTime) {
				return nil // Destination is newer or equal, skip
			}
		case KeepLarger:
			dstInfo, err := dstFS.Stat(dstPath)
			if err == nil && srcInfo.Size <= dstInfo.Size {
				return nil // Destination is larger or equal, skip
			}
		case KeepSmaller:
			dstInfo, err := dstFS.Stat(dstPath)
			if err == nil && srcInfo.Size >= dstInfo.Size {
				return nil // Destination is smaller or equal, skip
			}
		default:
			if e.opts.CustomResolver != nil {
				dstPath = e.opts.CustomResolver(srcPath, dstPath)
			}
		}
	}

	// Ensure destination directory exists
	dstDir := path.Dir(dstPath)
	if err := dstFS.MkdirAll(dstDir); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}

	r, err := srcFS.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source %s: %w", srcPath, err)
	}
	defer r.Close()

	w, err := dstFS.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dstPath, err)
	}
	defer w.Close()

	buf := make([]byte, e.opts.BufferSize)
	var copied int64

	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = io.ErrShortWrite
				}
			}
			copied += int64(nw)
			if e.opts.OnProgress != nil {
				e.opts.OnProgress(srcPath, copied, srcInfo.Size)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}

	if e.opts.OnItemDone != nil {
		e.opts.OnItemDone(srcPath, err)
	}
	return err
}

// CopyTree recursively copies an entire directory tree from srcFS to dstFS.
func (e *TransferEngine) CopyTree(srcFS FileSystem, srcRoot string, dstFS FileSystem, dstRoot string) error {
	srcRoot = NormalizePath(srcRoot)
	dstRoot = NormalizePath(dstRoot)

	return srcFS.Walk(srcRoot, func(p string, fi *FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel := strings.TrimPrefix(p, srcRoot)
		rel = strings.TrimPrefix(rel, "/")
		targetPath := NormalizePath(dstRoot + "/" + rel)

		if fi.IsDir {
			return dstFS.MkdirAll(targetPath)
		}

		return e.CopyFile(srcFS, p, dstFS, targetPath)
	})
}

// CopyFile copies a single file from srcFS to dstFS using default options.
func CopyFile(srcFS FileSystem, srcPath string, dstFS FileSystem, dstPath string) error {
	return NewTransferEngine(DefaultTransferOptions()).CopyFile(srcFS, srcPath, dstFS, dstPath)
}

// CopyTree recursively copies a directory tree using default options.
func CopyTree(srcFS FileSystem, srcRoot string, dstFS FileSystem, dstRoot string) error {
	return NewTransferEngine(DefaultTransferOptions()).CopyTree(srcFS, srcRoot, dstFS, dstRoot)
}

// TransferBatch transfers a list of specific files from srcFS into dstDir on dstFS.
func TransferBatch(srcFS FileSystem, paths []string, dstFS FileSystem, dstDir string) error {
	engine := NewTransferEngine(DefaultTransferOptions())
	dstDir = NormalizePath(dstDir)

	for _, p := range paths {
		baseName := path.Base(p)
		target := NormalizePath(dstDir + "/" + baseName)
		if err := engine.CopyFile(srcFS, p, dstFS, target); err != nil {
			return fmt.Errorf("failed transferring %s: %w", p, err)
		}
	}
	return nil
}

// TransferMatching walks srcDir on srcFS, selects files matching the predicate filter, and transfers them to dstDir on dstFS.
func TransferMatching(srcFS FileSystem, dstFS FileSystem, srcDir, dstDir string, filter func(fi *FileInfo) bool) ([]string, error) {
	engine := NewTransferEngine(DefaultTransferOptions())
	srcDir = NormalizePath(srcDir)
	dstDir = NormalizePath(dstDir)

	var transferred []string

	err := srcFS.Walk(srcDir, func(p string, fi *FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir {
			return nil
		}

		if filter != nil && !filter(fi) {
			return nil
		}

		rel := strings.TrimPrefix(p, srcDir)
		rel = strings.TrimPrefix(rel, "/")
		target := NormalizePath(dstDir + "/" + rel)

		if err := engine.CopyFile(srcFS, p, dstFS, target); err != nil {
			return err
		}

		transferred = append(transferred, p)
		return nil
	})

	return transferred, err
}

// MoveFile moves a single file from srcFS to dstFS with collision policy handling.
func (e *TransferEngine) MoveFile(srcFS FileSystem, srcPath string, dstFS FileSystem, dstPath string) error {
	srcPath = NormalizePath(srcPath)
	dstPath = NormalizePath(dstPath)

	// If same filesystem instance, try fast-path Rename
	if srcFS == dstFS {
		if dstFS.Exists(dstPath) {
			switch e.opts.Policy {
			case Skip:
				return nil
			case ErrorOnCollision:
				return fmt.Errorf("destination %s: %w", dstPath, ErrAlreadyExists)
			case AutoRename:
				dstPath = GenerateAutoRenamePath(dstFS, dstPath)
			case KeepNewer:
				srcInfo, _ := srcFS.Stat(srcPath)
				dstInfo, _ := dstFS.Stat(dstPath)
				if srcInfo != nil && dstInfo != nil && !srcInfo.ModTime.After(dstInfo.ModTime) {
					return nil
				}
			}
		}
		return srcFS.Rename(srcPath, dstPath)
	}

	// Cross-filesystem move: copy then remove
	if err := e.CopyFile(srcFS, srcPath, dstFS, dstPath); err != nil {
		return err
	}
	return srcFS.Remove(srcPath)
}

// MoveTree moves an entire directory tree from srcFS to dstFS.
func (e *TransferEngine) MoveTree(srcFS FileSystem, srcRoot string, dstFS FileSystem, dstRoot string) error {
	srcRoot = NormalizePath(srcRoot)
	dstRoot = NormalizePath(dstRoot)

	if srcFS == dstFS && !dstFS.Exists(dstRoot) {
		return srcFS.Rename(srcRoot, dstRoot)
	}

	if err := e.CopyTree(srcFS, srcRoot, dstFS, dstRoot); err != nil {
		return err
	}
	return srcFS.RemoveAll(srcRoot)
}

// MoveFile moves a single file using default options.
func MoveFile(srcFS FileSystem, srcPath string, dstFS FileSystem, dstPath string) error {
	return NewTransferEngine(DefaultTransferOptions()).MoveFile(srcFS, srcPath, dstFS, dstPath)
}

// MoveTree moves an entire directory tree using default options.
func MoveTree(srcFS FileSystem, srcRoot string, dstFS FileSystem, dstRoot string) error {
	return NewTransferEngine(DefaultTransferOptions()).MoveTree(srcFS, srcRoot, dstFS, dstRoot)
}

// TransferStats holds report data about a transfer operation.
type TransferStats struct {
	FilesCopied int64
	BytesCopied int64
	Duration    time.Duration
}

