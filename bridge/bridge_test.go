package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"freebox/engine"
)

func TestBridgeEndToEnd(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "freebox_bridge_test_*")
	if err != nil {
		t.Fatalf("Failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	hEngine, err := InitEngineHelper(dbPath, "test-passphrase", 2, ":9090")
	if err != nil || hEngine == 0 {
		t.Fatalf("Failed initializing bridge engine: %v", err)
	}
	defer Freebox_CloseEngine(uint64ToC(hEngine))

	srcPathBytes := []byte("local:testfile.txt")
	contentBytes := []byte("hello freebox byte bridge with search and replace")

	uintTaskID := SubmitTaskBytesHelper(hEngine, 1, srcPathBytes, nil, contentBytes, 10, 0)
	if uintTaskID == 0 {
		t.Fatalf("Failed submitting task via byte bridge")
	}

	inst := getInstance(hEngine)
	if inst == nil {
		t.Fatalf("Failed getting instance")
	}

	stringID := getTaskStringID(uintTaskID)
	handle, ok := inst.Engine.GetTask(stringID)
	if !ok || handle == nil {
		t.Fatalf("Task handle not found for ID %s", stringID)
	}

	status := handle.Wait()
	if status != engine.StatusCompleted {
		progress := handle.Progress()
		t.Fatalf("Expected task COMPLETED, got %v (err: %v)", status, progress.Error)
	}

	// 1. Test Clipboard Copy & Paste
	copyPath := []byte("local:testfile.txt")
	if err := ClipboardCopyHelper(hEngine, copyPath); err != nil {
		t.Fatalf("Clipboard copy failed: %v", err)
	}

	count := int(Freebox_ClipboardCount(uint64ToC(hEngine)))
	if count != 1 {
		t.Fatalf("Expected clipboard count 1, got %d", count)
	}

	pasteDest := []byte("local:copied_testfile.txt")
	pasteTaskID, err := ClipboardPasteHelper(hEngine, pasteDest)
	if err != nil || pasteTaskID == 0 {
		t.Fatalf("Clipboard paste failed: %v", err)
	}

	pasteHandle, ok := inst.Engine.GetTask(getTaskStringID(pasteTaskID))
	if ok && pasteHandle != nil {
		pasteHandle.Wait()
	}

	// 2. Test Search and Replace
	rootSearch := []byte("local:")
	namePat := []byte("*.txt")
	contentPat := []byte("search and replace")
	replaceWith := []byte("FAST DART BRIDGE")
	searchTaskID, err := SearchHelper(hEngine, rootSearch, namePat, contentPat, replaceWith, 4, 1, true, true)
	if err != nil || searchTaskID == 0 {
		t.Fatalf("Search/replace submission failed: %v", err)
	}
	searchHandle, ok := inst.Engine.GetTask(getTaskStringID(searchTaskID))
	if ok && searchHandle != nil {
		searchHandle.Wait()
	}

	// 3. Test Deduplication Scan
	dedupTaskID, err := DedupHelper(hEngine, rootSearch, 2, 1, 1, 1, 4)
	if err != nil || dedupTaskID == 0 {
		t.Fatalf("Dedup scan submission failed: %v", err)
	}
	dedupHandle, ok := inst.Engine.GetTask(getTaskStringID(dedupTaskID))
	if ok && dedupHandle != nil {
		dedupHandle.Wait()
	}

	// 4. Test Archive Compress and Extract
	arcSrc := []byte("local:testfile.txt")
	arcDst := []byte("local:archive.zip")
	compTaskID, err := ArchiveCompressHelper(hEngine, 1, arcSrc, arcDst)
	if err != nil || compTaskID == 0 {
		t.Fatalf("Archive compress submission failed: %v", err)
	}
	compHandle, ok := inst.Engine.GetTask(getTaskStringID(compTaskID))
	if ok && compHandle != nil {
		compHandle.Wait()
	}

	numEntries, err := ArchivePreviewHelper(hEngine, arcDst)
	if err != nil || numEntries < 1 {
		t.Fatalf("Expected at least 1 archive entry, got %d (err: %v)", numEntries, err)
	}

	// 5. Test Metadata Extraction
	title, err := ExtractMetadataHelper(hEngine, arcSrc)
	if err != nil || title == "" {
		t.Fatalf("Metadata extraction failed: %v", err)
	}

	// 6. Test Cryptographic Detached Signing & Verification
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed generating RSA key: %v", err)
	}
	privKeyBytes, _ := x509.MarshalPKCS8PrivateKey(privKey)
	privKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privKeyBytes})
	pubKeyBytes, _ := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	pubKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})

	sigHex, err := SignFileHelper(hEngine, arcSrc, privKeyPEM)
	if err != nil || sigHex == "" {
		t.Fatalf("File signing failed: %v", err)
	}

	if valid := VerifyFileSignatureHelper(hEngine, arcSrc, []byte(sigHex), pubKeyPEM); !valid {
		t.Fatalf("Signature verification failed")
	}

	// 7. Test Background HTTP Server Start & Stop
	httpPort := []byte(":18888")
	if err := StartHTTPServerHelper(hEngine, httpPort); err != nil {
		t.Fatalf("Starting HTTP server failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := StopHTTPServerHelper(hEngine); err != nil {
		t.Fatalf("Stopping HTTP server failed: %v", err)
	}
}
