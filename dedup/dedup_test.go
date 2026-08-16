package dedup

import (
	"context"
	"testing"
	"time"

	"freebox/vfs"
)

func TestDeduplication(t *testing.T) {
	fs := vfs.NewMemFS()

	// Identical contents (100 bytes each)
	contentA := []byte("This is identical file content replicated across several directories in the virtual filesystem.")
	_ = fs.MkdirAll("/dir1")
	_ = fs.MkdirAll("/dir2/sub")
	_ = fs.MkdirAll("/dir3")

	_ = fs.Write("/dir1/original.txt", contentA)
	time.Sleep(10 * time.Millisecond)
	_ = fs.Write("/dir2/sub/dup1.txt", contentA)
	time.Sleep(10 * time.Millisecond)
	_ = fs.Write("/dir3/dup2.txt", contentA)

	// Unique file
	_ = fs.Write("/dir1/unique.txt", []byte("Completely different content here."))

	// Run Deduplication with QuickHash
	engine := NewEngine(fs)
	groups, prog, err := engine.Deduplicate(context.Background(), DedupOptions{
		RootPath:   "/",
		Method:     MethodQuickHash,
		Action:     ActionReportOnly,
		KeepPolicy: KeepOldest,
	})
	if err != nil {
		t.Fatalf("Dedup failed: %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(groups))
	}
	if groups[0].Master.Path != "/dir1/original.txt" {
		t.Errorf("expected master to be /dir1/original.txt (oldest), got %s", groups[0].Master.Path)
	}
	if len(groups[0].Duplicates) != 2 {
		t.Fatalf("expected 2 duplicates, got %d", len(groups[0].Duplicates))
	}
	if prog.DuplicateBytes != int64(len(contentA)*2) {
		t.Errorf("expected %d duplicate bytes, got %d", len(contentA)*2, prog.DuplicateBytes)
	}

	// Test ActionDelete
	_, progDel, err := engine.Deduplicate(context.Background(), DedupOptions{
		RootPath:   "/",
		Method:     MethodSHA256,
		Action:     ActionDelete,
		KeepPolicy: KeepOldest,
		DryRun:     false,
	})
	if err != nil {
		t.Fatalf("Delete dedup failed: %v", err)
	}
	if progDel.DeletedFiles != 2 {
		t.Errorf("expected 2 deleted files, got %d", progDel.DeletedFiles)
	}
	if !fs.Exists("/dir1/original.txt") {
		t.Errorf("master file /dir1/original.txt was deleted")
	}
	if fs.Exists("/dir2/sub/dup1.txt") {
		t.Errorf("duplicate /dir2/sub/dup1.txt still exists")
	}
	if fs.Exists("/dir3/dup2.txt") {
		t.Errorf("duplicate /dir3/dup2.txt still exists")
	}
}

func TestDeduplicationMethods(t *testing.T) {
	methods := []DedupMethod{MethodMD5, MethodSHA256, MethodSHA1, MethodCRC32, MethodMeta}
	for _, m := range methods {
		t.Run(string(m), func(t *testing.T) {
			fs := vfs.NewMemFS()
			_ = fs.MkdirAll("/a")
			_ = fs.MkdirAll("/b")
			data := []byte("Testing different deduplication algorithms for consistency.")
			_ = fs.Write("/a/file.dat", data)
			_ = fs.Write("/b/file.dat", data)

			engine := NewEngine(fs)
			groups, _, err := engine.Deduplicate(context.Background(), DedupOptions{
				RootPath: "/",
				Method:   m,
				Action:   ActionReportOnly,
			})
			if err != nil {
				t.Fatalf("method %s failed: %v", m, err)
			}
			if len(groups) != 1 {
				t.Errorf("method %s expected 1 group, got %d", m, len(groups))
			}
		})
	}
}
