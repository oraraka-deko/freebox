package local

import (
	"os"
	"path/filepath"
	"testing"

	"freebox/vfs"
)

func TestLocalManagerCRUD(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewLocalManager()

	testFile := filepath.Join(tempDir, "local_test.txt")
	testDir := filepath.Join(tempDir, "subfolder")

	// 1. Create file & dir
	if err := mgr.CreateFile(testFile, []byte("local-content")); err != nil {
		t.Fatalf("create local file failed: %v", err)
	}
	if err := mgr.CreateDir(testDir); err != nil {
		t.Fatalf("create local dir failed: %v", err)
	}

	// 2. Read & Stat
	data, err := mgr.Read(testFile)
	if err != nil || string(data) != "local-content" {
		t.Errorf("failed reading local file: %v, content=%s", err, string(data))
	}

	info, err := mgr.Stat(testFile)
	if err != nil || info.Size != int64(len("local-content")) {
		t.Errorf("stat failed: %v, info=%+v", err, info)
	}

	// 3. Rename
	renamedFile := filepath.Join(tempDir, "renamed.txt")
	if err := mgr.Rename(testFile, renamedFile); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	// 4. List
	entries, err := mgr.List(tempDir)
	if err != nil || len(entries) < 2 {
		t.Errorf("list failed or returned wrong count: %v, entries=%d", err, len(entries))
	}

	// 5. Delete
	if err := mgr.Delete(renamedFile); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := os.Stat(renamedFile); !os.IsNotExist(err) {
		t.Errorf("file should be deleted")
	}
}

func TestLocalManagerPushAndPull(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewLocalManager()
	remoteFS := vfs.NewMemFS()

	localSrc := filepath.Join(tempDir, "upload.bin")
	_ = os.WriteFile(localSrc, []byte("payload-to-upload"), 0644)

	// 1. Push local file to remote VFS
	if err := mgr.PushToServer(localSrc, remoteFS, "/remote/upload.bin"); err != nil {
		t.Fatalf("push to server failed: %v", err)
	}

	remoteData, err := remoteFS.Read("/remote/upload.bin")
	if err != nil || string(remoteData) != "payload-to-upload" {
		t.Errorf("remote data mismatch: %v, content=%s", err, string(remoteData))
	}

	// 2. Pull remote file back to local destination
	localDst := filepath.Join(tempDir, "downloaded.bin")
	if err := mgr.PullFromServer(remoteFS, "/remote/upload.bin", localDst); err != nil {
		t.Fatalf("pull from server failed: %v", err)
	}

	downloaded, err := os.ReadFile(localDst)
	if err != nil || string(downloaded) != "payload-to-upload" {
		t.Errorf("downloaded data mismatch: %v, content=%s", err, string(downloaded))
	}
}
