package remotes

import (
	"context"
	"testing"

	"freebox/storage"
	"freebox/vfs"
)

func TestEditorDirectMode(t *testing.T) {
	memFS := vfs.NewMemFS()
	_ = memFS.Write("/remote/file.txt", []byte("initial remote content"))

	mediaCache := storage.NewMediaCache(storage.MediaCacheConfig{MaxMemoryMB: 8})
	mgr := NewEditorManager(mediaCache, t.TempDir())

	ctx := context.Background()
	sess, err := mgr.Open(ctx, memFS, "rem1", "/remote/file.txt", EditModeDirect)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	data, err := sess.Read()
	if err != nil || string(data) != "initial remote content" {
		t.Fatalf("Read mismatch: %v, got %s", err, string(data))
	}

	// Write directly
	if err := sess.Write([]byte("updated direct content")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify remote updated immediately
	remData, _ := memFS.Read("/remote/file.txt")
	if string(remData) != "updated direct content" {
		t.Fatalf("remote file content mismatch: got %s", string(remData))
	}

	_ = mgr.CloseSession(ctx, sess.ID, false)
}

func TestEditorLocalCopyMode(t *testing.T) {
	memFS := vfs.NewMemFS()
	_ = memFS.Write("/remote/large_file.txt", []byte("original content"))

	mediaCache := storage.NewMediaCache(storage.MediaCacheConfig{MaxMemoryMB: 8})
	mgr := NewEditorManager(mediaCache, t.TempDir())

	ctx := context.Background()
	sess, err := mgr.Open(ctx, memFS, "rem1", "/remote/large_file.txt", EditModeLocalCopy)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Local edits
	if err := sess.Write([]byte("local edit step 1")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := sess.Write([]byte("local edit final version")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Remote file should still be original until commit
	remDataBefore, _ := memFS.Read("/remote/large_file.txt")
	if string(remDataBefore) != "original content" {
		t.Errorf("remote was modified before commit: %s", string(remDataBefore))
	}

	// Commit local edits
	if err := sess.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Remote file should now have final version
	remDataAfter, _ := memFS.Read("/remote/large_file.txt")
	if string(remDataAfter) != "local edit final version" {
		t.Fatalf("remote not updated on commit: got %s", string(remDataAfter))
	}

	_ = mgr.CloseSession(ctx, sess.ID, false)
}

func TestEditorConflictDetection(t *testing.T) {
	memFS := vfs.NewMemFS()
	_ = memFS.Write("/remote/shared.txt", []byte("base v1"))

	mediaCache := storage.NewMediaCache(storage.MediaCacheConfig{MaxMemoryMB: 8})
	mgr := NewEditorManager(mediaCache, t.TempDir())

	ctx := context.Background()
	sess, err := mgr.Open(ctx, memFS, "rem1", "/remote/shared.txt", EditModeLocalCopy)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Local edit
	_ = sess.Write([]byte("user A edit"))

	// Simulate concurrent modification on remote by another user
	_ = memFS.Write("/remote/shared.txt", []byte("user B concurrent edit"))

	// Commit should detect conflict
	err = sess.Commit(ctx)
	if err != ErrConflict {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}

	_ = sess.Discard()
}

