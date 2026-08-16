package vfs

import (
	"os"
	"path/filepath"
	"testing"

	"freebox/storage"
)

func TestMountRegistry(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "freebox-vfs-test-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "vfs.db")
	db, err := storage.Open(storage.Config{Path: dbPath, Passphrase: "vfs-secret-pass"})
	if err != nil {
		t.Fatalf("failed opening storage db: %v", err)
	}
	defer db.Close()

	reg := NewRegistry(db)

	// 1. Mount memory FS
	if err := reg.Mount("mem-store", MountConfig{Type: "mem"}, true); err != nil {
		t.Fatalf("failed to mount mem-store: %v", err)
	}

	// 2. Mount local FS
	localRoot := filepath.Join(tempDir, "local_data")
	_ = os.MkdirAll(localRoot, 0755)
	if err := reg.Mount("local-store", MountConfig{Type: "local", Path: localRoot}, true); err != nil {
		t.Fatalf("failed to mount local-store: %v", err)
	}

	// 3. Test writing to mem-store
	memFS, ok := reg.Get("mem-store")
	if !ok {
		t.Fatalf("mem-store not found")
	}
	if err := memFS.Write("/test.txt", []byte("hello virtual world")); err != nil {
		t.Fatalf("write to mem-store failed: %v", err)
	}

	data, err := memFS.Read("/test.txt")
	if err != nil || string(data) != "hello virtual world" {
		t.Fatalf("read failed or mismatched: %v, %s", err, string(data))
	}

	// 4. List mounts
	mounts := reg.List()
	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(mounts))
	}

	// 5. Test reloading from encrypted DB
	reg2 := NewRegistry(db)
	if err := reg2.LoadAll(); err != nil {
		t.Fatalf("failed reloading mounts: %v", err)
	}

	mounts2 := reg2.List()
	if len(mounts2) != 2 {
		t.Fatalf("expected 2 reloaded mounts, got %d", len(mounts2))
	}
}
