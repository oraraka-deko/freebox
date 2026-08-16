package local

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"freebox/vfs"
)

// LocalManager provides direct host filesystem file management and cross-server transfers.
type LocalManager struct{}

// NewLocalManager creates a new LocalManager instance.
func NewLocalManager() *LocalManager {
	return &LocalManager{}
}

// Read reads the entire content of a local file.
func (l *LocalManager) Read(localPath string) ([]byte, error) {
	return os.ReadFile(localPath)
}

// Write writes data to a local file, creating parent directories if needed.
func (l *LocalManager) Write(localPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(localPath, data, 0644)
}

// CreateFile creates or overwrites a local file with content.
func (l *LocalManager) CreateFile(localPath string, data []byte) error {
	return l.Write(localPath, data)
}

// CreateDir creates a directory at localPath along with any necessary parents.
func (l *LocalManager) CreateDir(localPath string) error {
	return os.MkdirAll(localPath, 0755)
}

// Delete removes a file or directory at localPath.
func (l *LocalManager) Delete(localPath string) error {
	return os.RemoveAll(localPath)
}

// Rename moves or renames a local file or directory.
func (l *LocalManager) Rename(oldPath, newPath string) error {
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}

// Open opens a local file for reading.
func (l *LocalManager) Open(localPath string) (io.ReadCloser, error) {
	return os.Open(localPath)
}

// Stat returns metadata about the local file.
func (l *LocalManager) Stat(localPath string) (*vfs.FileInfo, error) {
	fi, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}
	return &vfs.FileInfo{
		Path:    filepath.ToSlash(localPath),
		Name:    fi.Name(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		ModTime: fi.ModTime(),
	}, nil
}

// List lists all entries in a local directory.
func (l *LocalManager) List(dirPath string) ([]*vfs.FileInfo, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	var results []*vfs.FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dirPath, entry.Name())
		results = append(results, &vfs.FileInfo{
			Path:    filepath.ToSlash(full),
			Name:    entry.Name(),
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			ModTime: info.ModTime(),
		})
	}
	return results, nil
}

// Copy copies a local file to another local destination.
func (l *LocalManager) Copy(srcPath, dstPath string) error {
	srcFS := vfs.NewOSFS(filepath.Dir(srcPath))
	dstFS := vfs.NewOSFS(filepath.Dir(dstPath))

	srcFile := "/" + filepath.Base(srcPath)
	dstFile := "/" + filepath.Base(dstPath)

	return vfs.CopyFile(srcFS, srcFile, dstFS, dstFile)
}

// Move moves a local file to another local destination.
func (l *LocalManager) Move(srcPath, dstPath string) error {
	if err := l.Rename(srcPath, dstPath); err == nil {
		return nil
	}
	// Fallback across different volumes / drives
	if err := l.Copy(srcPath, dstPath); err != nil {
		return err
	}
	return l.Delete(srcPath)
}

// PushToServer uploads a local file or directory to a remote server FileSystem.
func (l *LocalManager) PushToServer(localPath string, remoteFS vfs.FileSystem, remotePath string) error {
	fi, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("local stat error: %w", err)
	}

	srcFS := vfs.NewOSFS(filepath.Dir(localPath))
	relPath := "/" + filepath.Base(localPath)

	if fi.IsDir() {
		return vfs.CopyTree(srcFS, relPath, remoteFS, remotePath)
	}
	return vfs.CopyFile(srcFS, relPath, remoteFS, remotePath)
}

// PullFromServer downloads a remote file or directory from a remote server FileSystem to the local machine.
func (l *LocalManager) PullFromServer(remoteFS vfs.FileSystem, remotePath string, localDstPath string) error {
	remoteInfo, err := remoteFS.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("remote stat error: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(localDstPath), 0755); err != nil {
		return err
	}

	dstFS := vfs.NewOSFS(filepath.Dir(localDstPath))
	relDstPath := "/" + filepath.Base(localDstPath)

	if remoteInfo.IsDir {
		return vfs.CopyTree(remoteFS, remotePath, dstFS, relDstPath)
	}
	return vfs.CopyFile(remoteFS, remotePath, dstFS, relDstPath)
}

var (
	_ = errors.New
)
