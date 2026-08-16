package meta

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"freebox/storage"
	"freebox/vfs"
)

func TestMetadataAndPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}
	defer db.Close()

	mgr := NewManager(db)

	// Test VFS Metadata
	mem := vfs.NewMemFS()
	_ = mem.Write("/sample.txt", []byte("Sample text content"))

	info, err := mgr.GetInfo(mem, "/sample.txt")
	if err != nil {
		t.Fatalf("GetInfo failed: %v", err)
	}
	if info.Name != "sample.txt" || info.Size != 19 {
		t.Errorf("unexpected info: %+v", info)
	}
	if info.HumanSize == "" {
		t.Errorf("expected non-empty HumanSize")
	}

	// Test Custom Metadata persistence in DB
	err = mgr.SetCustomMeta("/sample.txt", "author", "Alice")
	if err != nil {
		t.Fatalf("SetCustomMeta failed: %v", err)
	}
	err = mgr.SetCustomMeta("/sample.txt", "project", "Freebox")
	if err != nil {
		t.Fatalf("SetCustomMeta 2 failed: %v", err)
	}

	custom, err := mgr.GetCustomMeta("/sample.txt")
	if err != nil {
		t.Fatalf("GetCustomMeta failed: %v", err)
	}
	if custom["author"] != "Alice" || custom["project"] != "Freebox" {
		t.Errorf("custom meta mismatch: %+v", custom)
	}

	// Test DeleteCustomMeta
	_ = mgr.DeleteCustomMeta("/sample.txt", "author")
	customAfter, _ := mgr.GetCustomMeta("/sample.txt")
	if _, exists := customAfter["author"]; exists {
		t.Errorf("expected author key to be deleted")
	}

	// Test Permission Parsing
	mode, err := ParseOctalMode("0755")
	if err != nil {
		t.Fatalf("ParseOctalMode failed: %v", err)
	}
	perms := ParsePermissions(mode)
	if !perms.UserExec || !perms.GroupRead || !perms.OtherExec {
		t.Errorf("permission flags mismatch: %+v", perms)
	}

	// Test Local File Touch and Stat
	testLocalFile := filepath.Join(tmpDir, "local_test.txt")
	_ = os.WriteFile(testLocalFile, []byte("local data"), 0644)

	localInfo, err := mgr.GetLocalDetailedInfo(testLocalFile)
	if err != nil {
		t.Fatalf("GetLocalDetailedInfo failed: %v", err)
	}
	if localInfo.Name != "local_test.txt" {
		t.Errorf("expected local_test.txt, got %s", localInfo.Name)
	}

	oldModTime := localInfo.ModTime
	time.Sleep(10 * time.Millisecond)
	targetTime := time.Now().Add(-1 * time.Hour)
	err = mgr.Touch(testLocalFile, targetTime, targetTime)
	if err != nil {
		t.Fatalf("Touch failed: %v", err)
	}

	updatedInfo, _ := mgr.GetLocalDetailedInfo(testLocalFile)
	if updatedInfo.ModTime.Equal(oldModTime) {
		t.Errorf("ModTime was not updated by Touch")
	}
}
