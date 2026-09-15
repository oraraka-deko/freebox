package s3

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"freebox/vfs"
	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/s3"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

type S3 struct {
	config storconfig.S3StorageConfig
	client *s3.Client
	logger *log.Logger
	fs     *S3FS
}

func (m *S3) Init(ctx context.Context, cfg storconfig.StorageConfig) error {
	s3cfg, ok := cfg.(*storconfig.S3StorageConfig)
	if !ok {
		return fmt.Errorf("failed to cast s3 config")
	}
	if err := s3cfg.Validate(); err != nil {
		return err
	}
	m.config = *s3cfg
	m.logger = log.FromContext(ctx).WithPrefix(fmt.Sprintf("s3[%s]", m.config.Name))
	client, err := s3.NewClient(&s3.Config{
		Endpoint:        m.config.Endpoint,
		Region:          m.config.Region,
		AccessKeyID:     m.config.AccessKeyID,
		SecretAccessKey: m.config.SecretAccessKey,
		BucketName:      m.config.BucketName,
		PathStyle:       !m.config.VirtualHost,
	})
	if err != nil {
		return fmt.Errorf("failed to create s3 client: %w", err)
	}
	m.client = client

	// Check if bucket exists
	if err := m.client.HeadBucket(ctx); err != nil {
		return fmt.Errorf("bucket %s not accessible: %w", m.config.BucketName, err)
	}
	return nil
}

func (m *S3) Type() storenum.StorageType {
	return storenum.S3
}

func (m *S3) Name() string {
	return m.config.Name
}

func (m *S3) JoinStoragePath(p string) string {
	return strings.TrimPrefix(path.Join(m.config.BasePath, p), "/")
}

func (m *S3) Save(ctx context.Context, r io.Reader, storagePath string) error {
	m.logger.Infof("Saving file from reader to %s", storagePath)
	candidate := m.JoinStoragePath(storagePath)

	if overwrite, _ := ctx.Value(ctxkey.OverwriteExisting).(bool); !overwrite {
		// Unique filename
		candidate = fsutil.UniquePath(strings.TrimPrefix(m.config.BasePath, "/"), storagePath, func(c string) bool {
			return m.existsKey(ctx, c)
		}, 10)
	}

	// Determine content length
	size := int64(-1)
	if length := ctx.Value(ctxkey.ContentLength); length != nil {
		if l, ok := length.(int64); ok && l > 0 {
			size = l
		}
	}

	err := m.client.Put(ctx, candidate, r, size)
	if err != nil {
		return fmt.Errorf("failed to upload file to S3: %w", err)
	}

	return nil
}

func (m *S3) Exists(ctx context.Context, storagePath string) bool {
	m.logger.Debugf("Checking if file exists at %s", storagePath)

	return m.existsKey(ctx, m.JoinStoragePath(storagePath))
}

func (m *S3) existsKey(ctx context.Context, key string) bool {
	return m.client.Exists(ctx, key)
}

// FS returns a virtual filesystem interface (vfs.FileSystem) backed by this S3 storage.
func (m *S3) FS() *S3FS {
	if m.fs != nil {
		return m.fs
	}
	s3cfg := S3Config{
		Endpoint:        m.config.Endpoint,
		Region:          m.config.Region,
		AccessKeyID:     m.config.AccessKeyID,
		SecretAccessKey: m.config.SecretAccessKey,
		BucketName:      m.config.BucketName,
		BasePath:        m.config.BasePath,
		UseSSL:          strings.HasPrefix(m.config.Endpoint, "https://"),
		PathStyle:       !m.config.VirtualHost,
	}
	fsys, err := NewS3FSFromConfig(s3cfg)
	if err != nil {
		if m.logger != nil {
			m.logger.Errorf("Failed to create S3FS: %v", err)
		}
		return nil
	}
	m.fs = fsys
	return m.fs
}

// ListFiles implements storage.StorageListable.
func (m *S3) ListFiles(ctx context.Context, dirPath string) ([]storagetypes.FileInfo, error) {
	fsys := m.FS()
	if fsys == nil {
		return nil, fmt.Errorf("s3 client not initialized")
	}
	entries, err := fsys.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}
	results := make([]storagetypes.FileInfo, len(entries))
	for i, e := range entries {
		results[i] = storagetypes.FileInfo{
			Name:    e.Name,
			Path:    path.Join(dirPath, e.Name),
			Size:    e.Size,
			IsDir:   e.IsDir,
			ModTime: e.ModTime,
		}
	}
	return results, nil
}

// OpenFile implements storage.StorageReadable.
func (m *S3) OpenFile(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	fsys := m.FS()
	if fsys == nil {
		return nil, 0, fmt.Errorf("s3 client not initialized")
	}
	info, err := fsys.Stat(filePath)
	if err != nil {
		return nil, 0, err
	}
	rc, err := fsys.OpenFile(ctx, filePath, vfs.ReadOnly())
	if err != nil {
		return nil, 0, err
	}
	return rc, info.Size, nil
}

// Delete removes an object or directory tree at storagePath.
func (m *S3) Delete(ctx context.Context, storagePath string) error {
	fsys := m.FS()
	if fsys == nil {
		return fmt.Errorf("s3 client not initialized")
	}
	return fsys.RemoveAll(storagePath)
}

// Rename renames an object or directory tree on S3.
func (m *S3) Rename(ctx context.Context, oldPath, newPath string) error {
	fsys := m.FS()
	if fsys == nil {
		return fmt.Errorf("s3 client not initialized")
	}
	return fsys.Rename(oldPath, newPath)
}

// Stat retrieves metadata about storagePath.
func (m *S3) Stat(ctx context.Context, storagePath string) (*storagetypes.FileInfo, error) {
	fsys := m.FS()
	if fsys == nil {
		return nil, fmt.Errorf("s3 client not initialized")
	}
	info, err := fsys.Stat(storagePath)
	if err != nil {
		return nil, err
	}
	return &storagetypes.FileInfo{
		Name:    info.Name,
		Path:    storagePath,
		Size:    info.Size,
		IsDir:   info.IsDir,
		ModTime: info.ModTime,
	}, nil
}

// Mkdir creates a directory marker at dirPath.
func (m *S3) Mkdir(ctx context.Context, dirPath string) error {
	fsys := m.FS()
	if fsys == nil {
		return fmt.Errorf("s3 client not initialized")
	}
	return fsys.MkdirAll(dirPath)
}
