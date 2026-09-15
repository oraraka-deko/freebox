package ipc

import (
	"encoding/base64"
	"net/http/httptest"
	"os"
	"testing"

	"golang.org/x/net/webdav"
)

func setupTestWebDAVServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "webdav_ipc_test")
	if err != nil {
		t.Fatalf("mk temp dir failed: %v", err)
	}

	handler := &webdav.Handler{
		Prefix:     "/",
		FileSystem: webdav.Dir(tempDir),
		LockSystem: webdav.NewMemLS(),
	}

	server := httptest.NewServer(handler)
	return server, tempDir
}

func TestIPCWebDAVMountAndOperate(t *testing.T) {
	wdServer, tempDir := setupTestWebDAVServer(t)
	defer os.RemoveAll(tempDir)
	defer wdServer.Close()

	_, socketPath := newTestServer(t)
	c := newTestClient(t, socketPath)
	defer c.nc.Close()

	// 1. Test webdav.test
	testRes := c.call("webdav.test", map[string]any{
		"url": wdServer.URL,
	})
	if ok, _ := testRes["ok"].(bool); !ok {
		t.Fatalf("webdav.test failed: %v", testRes)
	}

	// 2. Test webdav.mount
	mountRes := c.call("webdav.mount", map[string]any{
		"mountName": "wdmount",
		"url":       wdServer.URL,
	})
	if ok, _ := mountRes["ok"].(bool); !ok {
		t.Fatalf("webdav.mount failed: %v", mountRes)
	}

	// 3. Write file via VFS
	c.call("vfs.writeFile", map[string]any{
		"path": "wdmount:ipc_hello.txt",
		"data": []byte("hello from ipc webdav"),
	})

	// 4. Read file via VFS
	readRes := c.call("vfs.readFile", map[string]any{
		"path": "wdmount:ipc_hello.txt",
	})
	dataStr, ok := readRes["data"].(string)
	if !ok {
		t.Fatalf("unexpected data format in readFile: %v", readRes)
	}
	decoded, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		t.Fatalf("failed decoding base64: %v", err)
	}
	if string(decoded) != "hello from ipc webdav" {
		t.Fatalf("content mismatch: got %s", decoded)
	}

	// 5. ListDir via VFS
	listRaw := c.callRaw("vfs.listDir", map[string]any{
		"path": "wdmount:",
	})
	listSlice, ok := listRaw.([]any)
	if !ok || len(listSlice) == 0 {
		t.Fatalf("expected non-empty list from vfs.listDir: got %v", listRaw)
	}

	// 6. Unmount
	unmountRes := c.call("vfs.unmount", map[string]any{
		"name": "wdmount",
	})
	if ok, _ := unmountRes["ok"].(bool); !ok {
		t.Fatalf("vfs.unmount failed: %v", unmountRes)
	}
}
