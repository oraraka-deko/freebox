package s3

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"freebox/vfs"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func setupFakeS3Server(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	backend := s3mem.New()
	fakeSrv := gofakes3.New(backend)
	ts := httptest.NewServer(fakeSrv.Server())
	t.Cleanup(ts.Close)

	bucketName := "test-vfs-bucket"
	if err := backend.CreateBucket(bucketName); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	return ts, bucketName
}

func TestS3FS(t *testing.T) {
	server, bucketName := setupFakeS3Server(t)

	cfg := S3Config{
		Endpoint:        server.URL,
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		BucketName:      bucketName,
		BasePath:        "myroot",
		Region:          "us-east-1",
		PathStyle:       true,
	}

	fsys, err := NewS3FSFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewS3FSFromConfig failed: %v", err)
	}

	// 1. Capabilities
	caps := fsys.Capabilities()
	if !caps.Has(vfs.CapStreamRead) || !caps.Has(vfs.CapStreamWrite) {
		t.Fatalf("expected stream read and write capabilities")
	}

	// 2. MkdirAll
	if err := fsys.MkdirAll("/folder/sub"); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// 3. Write & Exists
	content := []byte("hello s3 vfs")
	if err := fsys.Write("/folder/sub/test.txt", content); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !fsys.Exists("/folder/sub/test.txt") {
		t.Fatalf("expected /folder/sub/test.txt to exist")
	}

	// 4. Stat file and root
	rootStat, err := fsys.Stat("/")
	if err != nil || !rootStat.IsDir {
		t.Fatalf("Stat / failed: stat=%v, err=%v", rootStat, err)
	}

	info, err := fsys.Stat("/folder/sub/test.txt")
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("Stat size mismatch: got %d, want %d", info.Size, len(content))
	}
	if info.IsDir {
		t.Fatalf("expected regular file, got dir")
	}

	// 5. Read
	readData, err := fsys.Read("/folder/sub/test.txt")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readData) != string(content) {
		t.Fatalf("Read content mismatch: got %s, want %s", readData, content)
	}

	// 6. Open / OpenFile
	rc, err := fsys.Open("/folder/sub/test.txt")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	all, _ := io.ReadAll(rc)
	rc.Close()
	if string(all) != string(content) {
		t.Fatalf("Open read mismatch")
	}

	ctx := context.Background()
	f, err := fsys.OpenFile(ctx, "/folder/sub/test.txt", vfs.ReadOnly())
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	all2, _ := io.ReadAll(f)
	f.Close()
	if string(all2) != string(content) {
		t.Fatalf("OpenFile read mismatch")
	}

	// 7. Create (via WriteCloser)
	wc, err := fsys.Create("/folder/sub/created.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	wc.Write([]byte("stream created on s3"))
	wc.Close()

	if !fsys.Exists("/folder/sub/created.txt") {
		t.Fatalf("created.txt does not exist")
	}

	// 8. ReadDir
	entries, err := fsys.ReadDir("/folder/sub")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries in /folder/sub, got %d", len(entries))
	}

	// 9. Rename
	if err := fsys.Rename("/folder/sub/created.txt", "/folder/renamed.txt"); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	if fsys.Exists("/folder/sub/created.txt") {
		t.Fatalf("old path should not exist after rename")
	}
	if !fsys.Exists("/folder/renamed.txt") {
		t.Fatalf("new path should exist after rename")
	}

	// 10. Walk
	walkCount := 0
	err = fsys.Walk("/folder", func(p string, info *vfs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		walkCount++
		return nil
	})
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	if walkCount == 0 {
		t.Fatalf("expected Walk to visit entries")
	}

	// 11. Remove
	if err := fsys.Remove("/folder/renamed.txt"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if fsys.Exists("/folder/renamed.txt") {
		t.Fatalf("file should not exist after remove")
	}

	// 12. RemoveAll
	if err := fsys.RemoveAll("/folder"); err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}
	if fsys.Exists("/folder/sub/test.txt") {
		t.Fatalf("file under folder should be removed by RemoveAll")
	}
}
