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
