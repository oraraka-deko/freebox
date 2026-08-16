package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"freebox/storage"
)

func setupTestDB(t *testing.T) (*storage.DB, func()) {
	tempDir, err := os.MkdirTemp("", "freebox-auth-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tempDir, "auth.db")
	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-auth-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(tempDir)
	}
	return db, cleanup
}

func TestAuthManager_Flow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mgr, err := NewManager(db, ManagerConfig{
		SessionTTL:       1 * time.Hour,
		DefaultAdminUser: "admin",
		DefaultAdminPass: "admin123",
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// 1. Authenticate default admin
	session, err := mgr.Authenticate("admin", "admin123")
	if err != nil {
		t.Fatalf("authentication failed: %v", err)
	}
	if session.Token == "" {
		t.Fatalf("session token is empty")
	}
	if session.Role != "admin" {
		t.Fatalf("expected admin role, got %s", session.Role)
	}

	// 2. Validate token
	validSession, err := mgr.ValidateToken(session.Token)
	if err != nil {
		t.Fatalf("token validation failed: %v", err)
	}
	if validSession.Username != "admin" {
		t.Fatalf("expected username admin, got %s", validSession.Username)
	}

	// 3. Create another user and authenticate
	if err := mgr.CreateUser("developer", "secret456", "operator"); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	devSession, err := mgr.Authenticate("developer", "secret456")
	if err != nil {
		t.Fatalf("dev auth failed: %v", err)
	}
	if devSession.Username != "developer" || devSession.Role != "operator" {
		t.Fatalf("unexpected dev session properties: %+v", devSession)
	}

	// 4. Invalid credentials test
	_, err = mgr.Authenticate("developer", "wrongpass")
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}

	// 5. Revoke session
	if err := mgr.RevokeToken(devSession.Token); err != nil {
		t.Fatalf("failed to revoke token: %v", err)
	}
	_, err = mgr.ValidateToken(devSession.Token)
	if err != ErrTokenInvalid {
		t.Fatalf("expected ErrTokenInvalid after revoke, got %v", err)
	}
}
