package webdav

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"freebox/vfs"
)

func TestWebDAVFS(t *testing.T) {
	server, tempDir := setupWebDAVServer(t)
	defer os.RemoveAll(tempDir)
	defer server.Close()

	client := NewClient(server.URL, "", "", nil)
	fsys := NewWebDAVFS(client, "")

	// 1. Capabilities
	caps := fsys.Capabilities()
	if !caps.Has(vfs.CapStreamRead) || !caps.Has(vfs.CapStreamWrite) {
		t.Fatalf("expected stream read and write caps")
	}

	// 2. MkdirAll
	if err := fsys.MkdirAll("/folder/sub"); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// 3. Write and Exists
	content := []byte("hello webdav vfs")
	if err := fsys.Write("/folder/sub/test.txt", content); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if !fsys.Exists("/folder/sub/test.txt") {
		t.Fatalf("expected /folder/sub/test.txt to exist")
	}

	// 4. Stat
	info, err := fsys.Stat("/folder/sub/test.txt")
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("Stat size mismatch: got %d, want %d", info.Size, len(content))
	}
	if info.IsDir {
		t.Fatalf("expected file, not dir")
	}

	// Stat root
	rootInfo, err := fsys.Stat("/")
	if err != nil || !rootInfo.IsDir {
		t.Fatalf("Stat root failed: info=%v, err=%v", rootInfo, err)
	}

	// 5. Read
	readBytes, err := fsys.Read("/folder/sub/test.txt")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readBytes) != string(content) {
		t.Fatalf("content mismatch: got %s, want %s", readBytes, content)
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
	wc.Write([]byte("stream created"))
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
		t.Fatalf("expected Walk to visit nodes")
	}

	// 11. Remove
	if err := fsys.Remove("/folder/renamed.txt"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if fsys.Exists("/folder/renamed.txt") {
		t.Fatalf("removed file still exists")
	}

	// Verify local disk content matches
	diskPath := filepath.Join(tempDir, "folder", "sub", "test.txt")
	diskData, err := os.ReadFile(diskPath)
	if err != nil || string(diskData) != string(content) {
		t.Fatalf("disk file mismatch: %v", err)
	}
}
