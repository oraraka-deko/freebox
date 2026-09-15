package remotes

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"freebox/storage"
	"freebox/vfs"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"golang.org/x/net/webdav"
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

func TestParseRemoteURI(t *testing.T) {
	// Test FTP custom path
	cfg, err := ParseRemoteURI("ftp:127.0.0.1:2121/path/we/want")
	if err != nil {
		t.Fatalf("ParseRemoteURI failed: %v", err)
	}
	if cfg.Type != TypeFTP || cfg.Host != "127.0.0.1" || cfg.Port != 2121 || cfg.Path != "/path/we/want" {
		t.Errorf("unexpected parsed config: %+v", cfg)
	}

	// Test SFTP with user and password
	cfg2, err := ParseRemoteURI("sftp://admin:secret123@myhost.com:2222/var/www/html")
	if err != nil {
		t.Fatalf("ParseRemoteURI sftp failed: %v", err)
	}
	if cfg2.Type != TypeSFTP || cfg2.Username != "admin" || cfg2.Password != "secret123" || cfg2.Host != "myhost.com" || cfg2.Port != 2222 || cfg2.Path != "/var/www/html" {
		t.Errorf("unexpected parsed config 2: %+v", cfg2)
	}
}

func TestRemotesManagerWebDAVAndS3(t *testing.T) {
	tempDir := t.TempDir()
	db, err := storage.Open(storage.Config{
		Path:       filepath.Join(tempDir, "test.db"),
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	registry := vfs.NewRegistry(db)
	mgr := NewManager(db, registry)

	// 1. WebDAV Remote
	wdDir := t.TempDir()
	wdHandler := &webdav.Handler{
		Prefix:     "/",
		FileSystem: webdav.Dir(wdDir),
		LockSystem: webdav.NewMemLS(),
	}
	wdServer := httptest.NewServer(wdHandler)
	defer wdServer.Close()

	wdCfg := RemoteConfig{
		Name:      "my-webdav",
		Type:      TypeWebDAV,
		URL:       wdServer.URL,
		AutoMount: true,
	}

	ok, err := mgr.TestConnection(wdCfg)
	if err != nil || !ok {
		t.Fatalf("WebDAV TestConnection failed: ok=%v, err=%v", ok, err)
	}

	if err := mgr.Create(wdCfg); err != nil {
		t.Fatalf("Create WebDAV remote failed: %v", err)
	}

	wdFS, exists := registry.Get("my-webdav")
	if !exists {
		t.Fatalf("expected my-webdav to be mounted in registry")
	}
	if err := wdFS.Write("/remotewd.txt", []byte("hello webdav remote")); err != nil {
		t.Fatalf("Write to WebDAV remote failed: %v", err)
	}
	readWD, err := wdFS.Read("/remotewd.txt")
	if err != nil || string(readWD) != "hello webdav remote" {
		t.Fatalf("Read from WebDAV remote failed: %v", err)
	}

	// 2. S3 Remote
	s3Backend := s3mem.New()
	fakeS3 := gofakes3.New(s3Backend)
	s3Server := httptest.NewServer(fakeS3.Server())
	defer s3Server.Close()

	if err := s3Backend.CreateBucket("remotes-bucket"); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	s3Cfg := RemoteConfig{
		Name:      "my-s3",
		Type:      TypeS3,
		Endpoint:  s3Server.URL,
		Bucket:    "remotes-bucket",
		Username:  "access-key",
		Password:  "secret-key",
		AutoMount: true,
		Options: map[string]string{
			"path_style": "true",
		},
	}

	ok, err = mgr.TestConnection(s3Cfg)
	if err != nil || !ok {
		t.Fatalf("S3 TestConnection failed: ok=%v, err=%v", ok, err)
	}

	if err := mgr.Create(s3Cfg); err != nil {
		t.Fatalf("Create S3 remote failed: %v", err)
	}

	s3FS, exists := registry.Get("my-s3")
	if !exists {
		t.Fatalf("expected my-s3 to be mounted in registry")
	}
	if err := s3FS.Write("/remotes3.txt", []byte("hello s3 remote")); err != nil {
		t.Fatalf("Write to S3 remote failed: %v", err)
	}
	readS3, err := s3FS.Read("/remotes3.txt")
	if err != nil || string(readS3) != "hello s3 remote" {
		t.Fatalf("Read from S3 remote failed: %v", err)
	}
}
