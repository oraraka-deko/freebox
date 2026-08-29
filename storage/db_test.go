package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedDB(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "freebox-db-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	cfg := Config{
		Path:       dbPath,
		Passphrase: "super-secret-password-123",
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("failed to open DB: %v", err)
	}

	// Test unencrypted put/get
	key := "test-key"
	val := []byte("plain text data")
	if err := db.Put(BucketSettings, key, val); err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	readVal, err := db.Get(BucketSettings, key)
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}
	if string(readVal) != string(val) {
		t.Fatalf("expected %s, got %s", string(val), string(readVal))
	}

	// Test encrypted put/get
	secretKey := "secret-token"
	secretVal := []byte("confidential credentials")
	if err := db.PutEncrypted(BucketUsers, secretKey, secretVal); err != nil {
		t.Fatalf("failed to put encrypted: %v", err)
	}

	// Verify raw value in DB is encrypted
	rawVal, err := db.Get(BucketUsers, secretKey)
	if err != nil {
		t.Fatalf("failed to get raw: %v", err)
	}
	if string(rawVal) == string(secretVal) {
		t.Fatalf("raw value was stored in plaintext!")
	}

	// Verify decrypted value matches original
	decryptedVal, err := db.GetDecrypted(BucketUsers, secretKey)
	if err != nil {
		t.Fatalf("failed to get decrypted: %v", err)
	}
	if string(decryptedVal) != string(secretVal) {
		t.Fatalf("expected decrypted %s, got %s", string(secretVal), string(decryptedVal))
	}

	// Test list decrypted
	if err := db.PutEncrypted(BucketUsers, "user2", []byte("pass2")); err != nil {
		t.Fatalf("failed to put user2: %v", err)
	}

	allUsers, err := db.ListDecrypted(BucketUsers)
	if err != nil {
		t.Fatalf("failed to list decrypted: %v", err)
	}
	if len(allUsers) != 2 {
		t.Fatalf("expected 2 users, got %d", len(allUsers))
	}
	if string(allUsers["user2"]) != "pass2" {
		t.Fatalf("expected pass2, got %s", string(allUsers["user2"]))
	}

	// Test close and reopen
	if err := db.Close(); err != nil {
		t.Fatalf("failed to close db: %v", err)
	}

	db2, err := Open(cfg)
	if err != nil {
		t.Fatalf("failed to reopen DB: %v", err)
	}
	defer db2.Close()

	decryptedAfterReopen, err := db2.GetDecrypted(BucketUsers, secretKey)
	if err != nil {
		t.Fatalf("failed to get decrypted after reopen: %v", err)
	}
	if string(decryptedAfterReopen) != string(secretVal) {
		t.Fatalf("data mismatch after reopen")
	}
}

func TestTaskHistoryAndClipboard(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(Config{Path: filepath.Join(tmpDir, "test.db")})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	hist := NewTaskHistoryStore(db)

	// Add 15 tasks
	for i := 1; i <= 15; i++ {
		_ = hist.AppendTask(TaskHistoryRecord{
			ID:          filepath.Join("task", string(rune('A'+i))),
			Type:        "COPY",
			Description: "Copy task number",
			Status:      "COMPLETED",
		})
	}

	// Query last 10 tasks (Task 1 requirement: "like 10 task ago what we dose performing")
	recent, err := hist.GetRecentTasks(10)
	if err != nil {
		t.Fatalf("GetRecentTasks failed: %v", err)
	}
	if len(recent) != 10 {
		t.Fatalf("expected 10 tasks, got %d", len(recent))
	}

	// Test Clipboard History
	clipHist := NewClipboardHistoryStore(db)
	stageID, err := clipHist.SaveStage([]ClipboardItemRecord{
		{Op: "COPY", Mount: "local", Path: "/data/file.txt", Size: 1024},
	})
	if err != nil || stageID == "" {
		t.Fatalf("SaveStage failed: %v", err)
	}

	latest, err := clipHist.GetLatestStage()
	if err != nil || latest == nil || len(latest.Items) != 1 {
		t.Fatalf("GetLatestStage failed: %v", err)
	}
	if latest.Items[0].Path != "/data/file.txt" {
		t.Errorf("unexpected item path: %s", latest.Items[0].Path)
	}
}

func TestTreeAndHeaderCache(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(Config{Path: filepath.Join(tmpDir, "test.db")})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	cache := NewTreeCache(0)

	// Test caching directory tree & tiny header
	cache.PutChildren("rem1", "/docs", []CachedFileInfo{
		{Path: "/docs/report.pdf", Name: "report.pdf", Size: 2048, IsDir: false},
		{Path: "/docs/images", Name: "images", IsDir: true},
	}, 0)

	// Put tiny 512B header for fast file type detection
	zipHeader := []byte("PK\x03\x04\x14\x00\x00\x00")
	cache.PutHeader("rem1", "/docs/archive.zip", zipHeader, "")

	// Verify tree lookup
	entries, ok := cache.GetChildren("rem1", "/docs", false)
	if !ok || len(entries) != 2 {
		t.Fatalf("GetChildren failed: ok=%v, count=%d", ok, len(entries))
	}

	// Verify header & mime lookup
	h, mime, ok := cache.GetHeader("rem1", "/docs/archive.zip")
	if !ok || string(h) != string(zipHeader) || mime != "application/zip" {
		t.Fatalf("GetHeader mismatch: ok=%v, mime=%s", ok, mime)
	}

	// Verify offline usability (Task 2 requirement: keep UI intact during temporary disconnect)
	if !cache.IsOfflineUsable("rem1", "/docs") {
		t.Errorf("expected /docs to be offline usable")
	}

	// Test DB persistence
	if err := cache.SaveToDB(db, "rem1"); err != nil {
		t.Fatalf("SaveToDB failed: %v", err)
	}

	cache2 := NewTreeCache(0)
	if err := cache2.LoadFromDB(db, "rem1"); err != nil {
		t.Fatalf("LoadFromDB failed: %v", err)
	}
	entries2, ok := cache2.GetChildren("rem1", "/docs", true)
	if !ok || len(entries2) != 2 {
		t.Fatalf("restored GetChildren mismatch: ok=%v, count=%d", ok, len(entries2))
	}
}

func TestMediaCacheAndEditDrafts(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := Open(Config{Path: filepath.Join(tmpDir, "test.db")})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	media := NewMediaCache(MediaCacheConfig{DB: db, MaxMemoryMB: 16})

	// Test Thumbnail caching
	thumbData := []byte("fake-jpeg-thumbnail-bytes")
	media.PutThumbnail("thumb-key-1", thumbData, "image/jpeg")

	retrieved, mime, ok := media.GetThumbnail("thumb-key-1")
	if !ok || string(retrieved) != string(thumbData) || mime != "image/jpeg" {
		t.Fatalf("GetThumbnail failed: ok=%v, mime=%s", ok, mime)
	}

	// Test Media Proxy chunk caching
	chunkData := []byte("stream-chunk-0-bytes")
	media.PutProxyChunk("stream123", 0, chunkData, 0)
	cRet, ok := media.GetProxyChunk("stream123", 0)
	if !ok || string(cRet) != string(chunkData) {
		t.Fatalf("GetProxyChunk failed")
	}

	// Test Archive preview cache
	media.PutArchivePreview("arch-key-1", []byte(`[{"name":"file.txt"}]`))
	archData, ok := media.GetArchivePreview("arch-key-1")
	if !ok || string(archData) != `[{"name":"file.txt"}]` {
		t.Fatalf("GetArchivePreview failed")
	}

	// Test In-memory & DB file editing draft (Task 3 & 5 requirements)
	draft := EditDraft{
		SessionID:      "edit-sess-1",
		Mount:          "rem1",
		RemotePath:     "/data/code.go",
		Mode:           "local_copy",
		DraftData:      []byte("package main\n\nfunc main() {}"),
		OriginalSHA256: "abc123hash",
		IsDirty:        true,
	}
	if err := media.SaveEditDraft(draft); err != nil {
		t.Fatalf("SaveEditDraft failed: %v", err)
	}

	retDraft, ok := media.GetEditDraft("edit-sess-1")
	if !ok || retDraft.RemotePath != "/data/code.go" || !retDraft.IsDirty {
		t.Fatalf("GetEditDraft failed: ok=%v, draft=%+v", ok, retDraft)
	}
}

