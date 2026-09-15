package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"freebox/vfs"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config holds connection and authentication configuration for S3.
type S3Config struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region,omitempty"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	BucketName      string `json:"bucketName"`
	BasePath        string `json:"basePath,omitempty"`
	UseSSL          bool   `json:"useSsl,omitempty"`
	PathStyle       bool   `json:"pathStyle,omitempty"`
}

// S3FS implements vfs.FileSystem interface backed by S3 (compatible with AWS S3, MinIO, Ceph, etc.).
type S3FS struct {
	client     *minio.Client
	bucketName string
	basePath   string
	timeout    time.Duration
}

// NewS3FS creates a new virtual filesystem backed by S3.
func NewS3FS(client *minio.Client, bucketName, basePath string) *S3FS {
	return &S3FS{
		client:     client,
		bucketName: bucketName,
		basePath:   strings.Trim(vfs.NormalizePath(basePath), "/"),
		timeout:    60 * time.Second,
	}
}

// NewS3FSFromConfig instantiates an S3 client and returns an S3FS virtual filesystem.
func NewS3FSFromConfig(cfg S3Config) (*S3FS, error) {
	endpoint := cfg.Endpoint
	useSSL := cfg.UseSSL
	if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
		useSSL = false
	} else if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
		useSSL = true
	}
	endpoint = strings.TrimRight(endpoint, "/")

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: useSSL,
		Region: cfg.Region,
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}

	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("failed creating S3 client: %w", err)
	}

	return NewS3FS(client, cfg.BucketName, cfg.BasePath), nil
}

func (s *S3FS) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.timeout)
}

func (s *S3FS) resolveKey(p string) string {
	p = vfs.NormalizePath(p)
	p = strings.TrimPrefix(p, "/")
	if s.basePath == "" {
		return p
	}
	if p == "" {
		return s.basePath
	}
	return path.Join(s.basePath, p)
}

// Client returns the underlying minio.Client.
func (s *S3FS) Client() *minio.Client {
	return s.client
}

// Bucket returns the target bucket name.
func (s *S3FS) Bucket() string {
	return s.bucketName
}

// Capabilities reports supported operations for S3.
func (s *S3FS) Capabilities() vfs.Capabilities {
	return vfs.Capabilities(vfs.CapStreamRead | vfs.CapStreamWrite)
}

// OpenFile opens a file for reading.
func (s *S3FS) OpenFile(ctx context.Context, p string, options vfs.OpenOptions) (vfs.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Write {
		return nil, vfs.ErrUnsupported
	}
	if !options.Read {
		return nil, errors.New("open requires read access")
	}

	key := s.resolveKey(p)
	obj, err := s.client.GetObject(ctx, s.bucketName, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, vfs.ErrNotFound
	}
	return obj, nil
}

// Open opens the named file for reading.
func (s *S3FS) Open(p string) (io.ReadCloser, error) {
	key := s.resolveKey(p)
	obj, err := s.client.GetObject(context.Background(), s.bucketName, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, vfs.ErrNotFound
	}
	return obj, nil
}

type s3WriteCloser struct {
	fs     *S3FS
	path   string
	buffer *bytes.Buffer
}

func (w *s3WriteCloser) Write(p []byte) (n int, err error) {
	return w.buffer.Write(p)
}

func (w *s3WriteCloser) Close() error {
	return w.fs.Write(w.path, w.buffer.Bytes())
}

// Create creates a write closer that flushes file contents to S3 upon Close.
func (s *S3FS) Create(p string) (io.WriteCloser, error) {
	return &s3WriteCloser{
		fs:     s,
		path:   vfs.NormalizePath(p),
		buffer: new(bytes.Buffer),
	}, nil
}

// Read reads the entire file content.
func (s *S3FS) Read(p string) ([]byte, error) {
	rc, err := s.Open(p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Write writes bytes to an object on S3.
func (s *S3FS) Write(p string, data []byte) error {
	key := s.resolveKey(p)
	ctx, cancel := s.ctx()
	defer cancel()

	reader := bytes.NewReader(data)
	_, err := s.client.PutObject(ctx, s.bucketName, key, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType:          "application/octet-stream",
		DisableMultipart:     true,
		SendContentMd5:       true,
		DisableContentSha256: true,
	})
	return err
}

// Stat returns metadata about the file or directory.
func (s *S3FS) Stat(p string) (*vfs.FileInfo, error) {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return &vfs.FileInfo{
			Path:    "/",
			Name:    "/",
			IsDir:   true,
			ModTime: time.Now(),
		}, nil
	}

	key := s.resolveKey(p)
	ctx, cancel := s.ctx()
	defer cancel()

	objInfo, err := s.client.StatObject(ctx, s.bucketName, key, minio.StatObjectOptions{})
	if err == nil {
		isDir := strings.HasSuffix(key, "/") || objInfo.ContentType == "application/x-directory"
		return &vfs.FileInfo{
			Path:        p,
			Name:        path.Base(p),
			Size:        objInfo.Size,
			IsDir:       isDir,
			ModTime:     objInfo.LastModified,
			ContentType: objInfo.ContentType,
		}, nil
	}

	// Check if key is a virtual directory prefix with children
	prefix := strings.TrimSuffix(key, "/") + "/"
	objCh := s.client.ListObjects(ctx, s.bucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		MaxKeys:   1,
		Recursive: false,
	})
	for obj := range objCh {
		if obj.Err == nil {
			return &vfs.FileInfo{
				Path:    p,
				Name:    path.Base(p),
				IsDir:   true,
				ModTime: time.Now(),
			}, nil
		}
	}

	return nil, vfs.ErrNotFound
}

// ReadDir lists directory entries inside path.
func (s *S3FS) ReadDir(p string) ([]*vfs.FileInfo, error) {
	p = vfs.NormalizePath(p)
	key := s.resolveKey(p)

	var prefix string
	if key != "" {
		prefix = strings.TrimSuffix(key, "/") + "/"
	}

	ctx, cancel := s.ctx()
	defer cancel()

	objCh := s.client.ListObjects(ctx, s.bucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	})

	seen := make(map[string]bool)
	var results []*vfs.FileInfo

	for obj := range objCh {
		if obj.Err != nil {
			return nil, obj.Err
		}
		if obj.Key == prefix || obj.Key == strings.TrimSuffix(prefix, "/") {
			continue
		}

		rel := strings.TrimPrefix(obj.Key, prefix)
		isDir := strings.HasSuffix(obj.Key, "/") || strings.Contains(rel, "/")
		cleanName := strings.Trim(rel, "/")
		if strings.Contains(cleanName, "/") {
			parts := strings.Split(cleanName, "/")
			cleanName = parts[0]
			isDir = true
		}
		if cleanName == "" || seen[cleanName] {
			continue
		}
		seen[cleanName] = true

		entryPath := vfs.NormalizePath(p + "/" + cleanName)
		results = append(results, &vfs.FileInfo{
			Path:        entryPath,
			Name:        cleanName,
			Size:        obj.Size,
			IsDir:       isDir,
			ModTime:     obj.LastModified,
			ContentType: obj.ContentType,
		})
	}

	return results, nil
}

// MkdirAll creates a directory marker object on S3.
func (s *S3FS) MkdirAll(p string) error {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return nil
	}

	key := s.resolveKey(p)
	dirKey := strings.TrimSuffix(key, "/") + "/"

	ctx, cancel := s.ctx()
	defer cancel()

	_, err := s.client.PutObject(ctx, s.bucketName, dirKey, bytes.NewReader([]byte{}), 0, minio.PutObjectOptions{
		ContentType:          "application/x-directory",
		DisableMultipart:     true,
		SendContentMd5:       true,
		DisableContentSha256: true,
	})
	return err
}

// Remove deletes the named object or directory marker from S3.
func (s *S3FS) Remove(p string) error {
	p = vfs.NormalizePath(p)
	if p == "/" {
		return errors.New("cannot remove root directory")
	}

	key := s.resolveKey(p)
	ctx, cancel := s.ctx()
	defer cancel()

	_ = s.client.RemoveObject(ctx, s.bucketName, strings.TrimSuffix(key, "/")+"/", minio.RemoveObjectOptions{})
	return s.client.RemoveObject(ctx, s.bucketName, key, minio.RemoveObjectOptions{})
}

// RemoveAll removes path and any children objects under its prefix.
func (s *S3FS) RemoveAll(p string) error {
	p = vfs.NormalizePath(p)
	key := s.resolveKey(p)

	ctx, cancel := s.ctx()
	defer cancel()

	var prefix string
	if p != "/" && key != "" {
		prefix = strings.TrimSuffix(key, "/") + "/"
		_ = s.client.RemoveObject(ctx, s.bucketName, key, minio.RemoveObjectOptions{})
		_ = s.client.RemoveObject(ctx, s.bucketName, prefix, minio.RemoveObjectOptions{})
	} else if s.basePath != "" {
		prefix = strings.TrimSuffix(s.basePath, "/") + "/"
	}

	objCh := s.client.ListObjects(ctx, s.bucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	for obj := range objCh {
		if obj.Err == nil {
			_ = s.client.RemoveObject(ctx, s.bucketName, obj.Key, minio.RemoveObjectOptions{})
		}
	}
	return nil
}

// Rename renames (copies and deletes) an object or directory tree on S3.
func (s *S3FS) Rename(oldPath, newPath string) error {
	oldPath = vfs.NormalizePath(oldPath)
	newPath = vfs.NormalizePath(newPath)
	oldKey := s.resolveKey(oldPath)
	newKey := s.resolveKey(newPath)

	ctx, cancel := s.ctx()
	defer cancel()

	// Check if single object exists
	stat, err := s.client.StatObject(ctx, s.bucketName, oldKey, minio.StatObjectOptions{})
	if err == nil && !strings.HasSuffix(oldKey, "/") && stat.ContentType != "application/x-directory" {
		src := minio.CopySrcOptions{Bucket: s.bucketName, Object: oldKey}
		dst := minio.CopyDestOptions{Bucket: s.bucketName, Object: newKey}
		if _, err := s.client.CopyObject(ctx, dst, src); err != nil {
			return fmt.Errorf("failed copying object from %s to %s: %w", oldKey, newKey, err)
		}
		return s.client.RemoveObject(ctx, s.bucketName, oldKey, minio.RemoveObjectOptions{})
	}

	// Rename directory tree
	oldPrefix := strings.TrimSuffix(oldKey, "/") + "/"
	newPrefix := strings.TrimSuffix(newKey, "/") + "/"

	objCh := s.client.ListObjects(ctx, s.bucketName, minio.ListObjectsOptions{
		Prefix:    oldPrefix,
		Recursive: true,
	})

	for obj := range objCh {
		if obj.Err != nil {
			return obj.Err
		}
		rel := strings.TrimPrefix(obj.Key, oldPrefix)
		dstKey := newPrefix + rel

		src := minio.CopySrcOptions{Bucket: s.bucketName, Object: obj.Key}
		dst := minio.CopyDestOptions{Bucket: s.bucketName, Object: dstKey}
		if _, err := s.client.CopyObject(ctx, dst, src); err != nil {
			return fmt.Errorf("failed copying %s to %s: %w", obj.Key, dstKey, err)
		}
		_ = s.client.RemoveObject(ctx, s.bucketName, obj.Key, minio.RemoveObjectOptions{})
	}

	_ = s.client.RemoveObject(ctx, s.bucketName, oldKey, minio.RemoveObjectOptions{})
	_ = s.client.RemoveObject(ctx, s.bucketName, oldPrefix, minio.RemoveObjectOptions{})

	return nil
}

// Exists checks if the named path exists on S3.
func (s *S3FS) Exists(p string) bool {
	_, err := s.Stat(p)
	return err == nil
}

// Walk walks the file tree rooted at root.
func (s *S3FS) Walk(root string, fn func(p string, info *vfs.FileInfo, err error) error) error {
	root = vfs.NormalizePath(root)
	info, err := s.Stat(root)
	if err != nil {
		return fn(root, nil, err)
	}

	if err := fn(root, info, nil); err != nil {
		return err
	}

	if !info.IsDir {
		return nil
	}

	entries, err := s.ReadDir(root)
	if err != nil {
		return fn(root, nil, err)
	}

	for _, entry := range entries {
		if err := s.Walk(entry.Path, fn); err != nil {
			return err
		}
	}
	return nil
}

// Ensure interface compliance
var _ vfs.FileSystem = (*S3FS)(nil)
var _ vfs.OpenFileSystem = (*S3FS)(nil)
