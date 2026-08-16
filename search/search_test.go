package search

import (
	"context"
	"fmt"
	"testing"
	"time"

	"freebox/vfs"
)

func setupTestVFS(t *testing.T) vfs.FileSystem {
	fs := vfs.NewMemFS()
	_ = fs.MkdirAll("/docs/sub")
	_ = fs.MkdirAll("/logs")
	_ = fs.MkdirAll("/data")

	_ = fs.Write("/docs/hello.txt", []byte("Hello World!\nThis is a fast search engine.\nFreebox file system.\n"))
	_ = fs.Write("/docs/sub/notes.md", []byte("# Notes\nImportant config: PORT=8080\nAnother line.\n"))
	_ = fs.Write("/logs/app.log", []byte("[INFO] Server started\n[ERROR] Database connection timed out\n[DEBUG] Retrying...\n"))
	_ = fs.Write("/data/items.csv", []byte("id,name,price\n1,Widget,9.99\n2,Gadget,19.99\n"))
	_ = fs.Write("/data/sample.bin", []byte{0x00, 0xFF, 0x00, 0xFE, 0x12, 0x34})

	return fs
}

func TestSearchFileNames(t *testing.T) {
	fs := setupTestVFS(t)
	engine := NewEngine(fs)

	res, stats, err := engine.Search(context.Background(), SearchQuery{
		RootPath:      "/",
		NamePattern:   "*.txt",
		NameMatchType: MatchGlob,
		Target:        TargetNameOnly,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Name != "hello.txt" {
		t.Errorf("expected hello.txt, got %s", res[0].Name)
	}
	if stats.FilesScanned == 0 {
		t.Errorf("expected FilesScanned > 0")
	}
}

func TestSearchFileContent(t *testing.T) {
	fs := setupTestVFS(t)
	engine := NewEngine(fs)

	res, _, err := engine.Search(context.Background(), SearchQuery{
		RootPath:         "/",
		ContentPattern:   "PORT=8080",
		ContentMatchType: MatchSubstring,
		Target:           TargetContentOnly,
	})
	if err != nil {
		t.Fatalf("Content search failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 match, got %d", len(res))
	}
	if res[0].Path != "/docs/sub/notes.md" {
		t.Errorf("unexpected match path: %s", res[0].Path)
	}
	if len(res[0].Matches) != 1 {
		t.Fatalf("expected 1 content match line, got %d", len(res[0].Matches))
	}
	if res[0].Matches[0].LineNumber != 2 {
		t.Errorf("expected match on line 2, got %d", res[0].Matches[0].LineNumber)
	}
}

func TestBatchReplaceContent(t *testing.T) {
	fs := setupTestVFS(t)
	engine := NewEngine(fs)

	// Dry run test
	resDry, statsDry, err := engine.Search(context.Background(), SearchQuery{
		RootPath:         "/docs",
		ContentPattern:   "fast search engine",
		ContentMatchType: MatchSubstring,
		Replacement:      "ultra-fast search engine",
		IsReplace:        true,
		DryRun:           true,
		Target:           TargetContentOnly,
	})
	if err != nil {
		t.Fatalf("dry run replace failed: %v", err)
	}
	if len(resDry) != 1 || statsDry.Replacements != 1 {
		t.Fatalf("expected 1 replacement in dry run, got %d", statsDry.Replacements)
	}

	// Content should still be unchanged
	raw, _ := fs.Read("/docs/hello.txt")
	if string(raw) == "ultra-fast" {
		t.Errorf("dry run modified file")
	}

	// Apply replacement
	resApply, statsApply, err := engine.Search(context.Background(), SearchQuery{
		RootPath:         "/docs",
		ContentPattern:   "fast search engine",
		ContentMatchType: MatchSubstring,
		Replacement:      "ultra-fast search engine",
		IsReplace:        true,
		DryRun:           false,
		CreateBackup:     true,
		Target:           TargetContentOnly,
	})
	if err != nil {
		t.Fatalf("apply replace failed: %v", err)
	}
	if len(resApply) != 1 || statsApply.Replacements != 1 {
		t.Fatalf("expected 1 replacement, got %d", statsApply.Replacements)
	}

	// Verify new content
	rawNew, _ := fs.Read("/docs/hello.txt")
	if !contains(string(rawNew), "ultra-fast search engine") {
		t.Errorf("file content was not replaced: %s", string(rawNew))
	}

	// Verify backup file was created
	if !fs.Exists("/docs/hello.txt.bak") {
		t.Errorf("backup file .bak was not created")
	}
}

func TestBatchReplaceFileNames(t *testing.T) {
	fs := setupTestVFS(t)
	engine := NewEngine(fs)

	_, stats, err := engine.Search(context.Background(), SearchQuery{
		RootPath:      "/logs",
		NamePattern:   "app.log",
		NameMatchType: MatchExact,
		Replacement:   "server.log",
		IsReplace:     true,
		DryRun:        false,
		Target:        TargetNameOnly,
	})
	if err != nil {
		t.Fatalf("rename search failed: %v", err)
	}
	if stats.Replacements != 1 {
		t.Errorf("expected 1 rename replacement, got %d", stats.Replacements)
	}
	if !fs.Exists("/logs/server.log") {
		t.Errorf("renamed file /logs/server.log does not exist")
	}
	if fs.Exists("/logs/app.log") {
		t.Errorf("original file /logs/app.log still exists")
	}
}

func TestLargeScaleSearchSimulation(t *testing.T) {
	fs := vfs.NewMemFS()
	totalTinyFiles := 5000
	for i := 0; i < totalTinyFiles; i++ {
		p := fmt.Sprintf("/tiny/dir_%d/file_%d.txt", i/100, i)
		_ = fs.Write(p, []byte(fmt.Sprintf("key_%d=val_%d\n", i, i*2)))
	}

	engine := NewEngine(fs)
	start := time.Now()
	res, stats, err := engine.Search(context.Background(), SearchQuery{
		RootPath:         "/tiny",
		ContentPattern:   "key_4242=val_8484",
		ContentMatchType: MatchExact,
		Target:           TargetContentOnly,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("large scale search failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 match among %d files, got %d", totalTinyFiles, len(res))
	}
	t.Logf("Scanned %d files in %v (%0.2f files/sec)", stats.FilesScanned, elapsed, stats.ItemsPerSecond)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
