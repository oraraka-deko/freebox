package ipc

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func setupTestFakeS3(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	backend := s3mem.New()
	fakeSrv := gofakes3.New(backend)
	ts := httptest.NewServer(fakeSrv.Server())
	t.Cleanup(ts.Close)

	bucketName := "ipc-s3-bucket"
	if err := backend.CreateBucket(bucketName); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	return ts, bucketName
}

func TestIPCS3MountAndOperate(t *testing.T) {
	s3Server, bucketName := setupTestFakeS3(t)

	_, socketPath := newTestServer(t)
	c := newTestClient(t, socketPath)
	defer c.nc.Close()

	// 1. Test s3.test
	testRes := c.call("s3.test", map[string]any{
		"endpoint":        s3Server.URL,
		"bucket":          bucketName,
		"accessKeyId":     "access-key",
		"secretAccessKey": "secret-key",
		"pathStyle":       true,
	})
	if ok, _ := testRes["ok"].(bool); !ok {
		t.Fatalf("s3.test failed: %v", testRes)
	}

	// 2. Test s3.mount
	mountRes := c.call("s3.mount", map[string]any{
		"mountName":       "s3mount",
		"endpoint":        s3Server.URL,
		"bucket":          bucketName,
		"accessKeyId":     "access-key",
		"secretAccessKey": "secret-key",
		"path":            "virtual/root",
		"pathStyle":       true,
	})
	if ok, _ := mountRes["ok"].(bool); !ok {
		t.Fatalf("s3.mount failed: %v", mountRes)
	}

	// 3. Write file via VFS
	c.call("vfs.writeFile", map[string]any{
		"path": "s3mount:test.txt",
		"data": []byte("hello from s3 via ipc"),
	})

	// 4. Read file via VFS
	readRes := c.call("vfs.readFile", map[string]any{
		"path": "s3mount:test.txt",
	})
	dataStr, ok := readRes["data"].(string)
	if !ok {
		t.Fatalf("unexpected data format in readFile: %v", readRes)
	}
	decoded, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		t.Fatalf("failed decoding base64: %v", err)
	}
	if string(decoded) != "hello from s3 via ipc" {
		t.Fatalf("content mismatch: got %s", decoded)
	}

	// 5. ListDir via VFS
	listRaw := c.callRaw("vfs.listDir", map[string]any{
		"path": "s3mount:",
	})
	listSlice, ok := listRaw.([]any)
	if !ok || len(listSlice) == 0 {
		t.Fatalf("expected non-empty list from vfs.listDir: got %v", listRaw)
	}

	// 6. Unmount
	unmountRes := c.call("vfs.unmount", map[string]any{
		"name": "s3mount",
	})
	if ok, _ := unmountRes["ok"].(bool); !ok {
		t.Fatalf("vfs.unmount failed: %v", unmountRes)
	}
}
