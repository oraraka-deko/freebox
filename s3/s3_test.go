package s3_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"freebox/s3"
	"github.com/charmbracelet/log"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
)

func newTestContext(t *testing.T) context.Context {
	t.Helper()
	logger := log.NewWithOptions(nil, log.Options{ReportTimestamp: false})
	ctx := context.Background()
	return log.WithContext(ctx, logger)
}

func newFakeS3(t *testing.T) (*s3.S3, *storconfig.S3StorageConfig) {
	t.Helper()

	backend := s3mem.New()
	fakeSrv := gofakes3.New(backend)
	ts := httptest.NewServer(fakeSrv.Server())
	t.Cleanup(ts.Close)

	cfg := &storconfig.S3StorageConfig{
		BaseConfig: storconfig.BaseConfig{
			Name:   "test-s3",
			Type:   "s3",
			Enable: true,
		},
		Endpoint:        ts.URL,
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret",
		BucketName:      "test-bucket",
		BasePath:        "base",
		Region:          "us-east-1",
	}

	if err := backend.CreateBucket("test-bucket"); err != nil {
		t.Fatalf("failed to create fake bucket: %v", err)
	}

	s := &s3.S3{}
	ctx := newTestContext(t)
	if err := s.Init(ctx, cfg); err != nil {
		t.Fatalf("init s3 failed: %v", err)
	}

	return s, cfg
}

func TestS3(t *testing.T) {
	s, _ := newFakeS3(t)
	ctx := t.Context()

	content := []byte("hello world")
	reader := bytes.NewReader(content)
	key := "foo/bar.txt"

	if err := s.Save(ctx, reader, key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if !s.Exists(ctx, key) {
		t.Fatalf("Exists should return true for saved key")
	}

	if s.Exists(ctx, "nonexistent.txt") {
		t.Fatalf("Exists should return false for nonexistent key")
	}

	if err := s.Save(ctx, bytes.NewReader(content), key); err != nil {
		t.Fatalf("Save with existing key failed: %v", err)
	}

	if !s.Exists(ctx, "foo/bar_1.txt") {
		t.Fatalf("Exists should return true for unique renamed key")
	}

	var length int64 = int64(len(content))
	ctx = context.WithValue(ctx, ctxkey.ContentLength, length)
	if err := s.Save(ctx, bytes.NewReader(content), "size_test.txt"); err != nil {
		t.Fatalf("Save with content length failed: %v", err)
	}

	if !s.Exists(ctx, "size_test.txt") {
		t.Fatalf("Exists should return true for size_test.txt")
	}

	// Test management methods
	// 1. Stat
	info, err := s.Stat(ctx, "size_test.txt")
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size != length {
		t.Fatalf("Stat size mismatch: got %d, want %d", info.Size, length)
	}

	// 2. OpenFile
	rc, size, err := s.OpenFile(ctx, "size_test.txt")
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	readContent, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(readContent) != string(content) {
		t.Fatalf("OpenFile content mismatch: %v", err)
	}
	if size != length {
		t.Fatalf("OpenFile size mismatch: got %d, want %d", size, length)
	}

	// 3. ListFiles
	files, err := s.ListFiles(ctx, "foo")
	if err != nil {
		t.Fatalf("ListFiles failed: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("expected at least 2 files in foo, got %d", len(files))
	}

	// 4. Rename
	if err := s.Rename(ctx, "size_test.txt", "size_renamed.txt"); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	if s.Exists(ctx, "size_test.txt") {
		t.Fatalf("old file should not exist after rename")
	}
	if !s.Exists(ctx, "size_renamed.txt") {
		t.Fatalf("renamed file should exist")
	}

	// 5. Delete
	if err := s.Delete(ctx, "size_renamed.txt"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if s.Exists(ctx, "size_renamed.txt") {
		t.Fatalf("deleted file still exists")
	}

	// 6. Mkdir
	if err := s.Mkdir(ctx, "new_folder"); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
}
