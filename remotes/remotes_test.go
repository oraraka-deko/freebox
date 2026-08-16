package remotes

import (
	"path/filepath"
	"testing"

	"freebox/storage"
	"freebox/vfs"
)

func TestRemotesManager(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "remotes.db")
	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}
	defer db.Close()

	registry := vfs.NewRegistry(db)
	mgr := NewManager(db, registry)

	// Create new remote config
	cfg1 := RemoteConfig{
		Name:      "backup-cloud",
		Type:      TypeMemory,
		AutoMount: true,
	}

	err = mgr.Create(cfg1)
	if err != nil {
		t.Fatalf("Create remote failed: %v", err)
	}

	// Should prevent duplicate names
	err = mgr.Create(cfg1)
	if err != ErrRemoteExists {
		t.Errorf("expected ErrRemoteExists, got %v", err)
	}

	// Verify it auto-mounted
	_, exists := registry.Get("backup-cloud")
	if !exists {
		t.Errorf("expected backup-cloud to be mounted in registry")
	}

	// Get remote
	retrieved, err := mgr.Get("backup-cloud")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.Name != "backup-cloud" || retrieved.Type != TypeMemory {
		t.Errorf("unexpected retrieved remote: %+v", retrieved)
	}

	// List remotes
	list, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 remote in list, got %d", len(list))
	}

	// Update remote
	retrieved.Host = "192.168.1.50"
	err = mgr.Update(*retrieved)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, _ := mgr.Get("backup-cloud")
	if updated.Host != "192.168.1.50" {
		t.Errorf("expected updated host 192.168.1.50, got %s", updated.Host)
	}

	// Delete remote
	err = mgr.Delete("backup-cloud")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, existsAfter := registry.Get("backup-cloud")
	if existsAfter {
		t.Errorf("expected remote to be unmounted after deletion")
	}
}
