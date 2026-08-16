package ftp

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFTPClientServer(t *testing.T) {
	tempDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("hello ftp"), 0644)

	cfg := DefaultConfig()
	cfg.RootDir = tempDir
	server := NewServer(cfg)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(l)
	}()

	client, err := Dial(l.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial ftp server: %v", err)
	}
	defer client.Close()

	if err := client.Login("anonymous", "anonymous"); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	pwd, err := client.Pwd()
	if err != nil {
		t.Fatalf("pwd failed: %v", err)
	}
	if pwd != "/" {
		t.Errorf("expected pwd '/', got '%s'", pwd)
	}

	if err := client.Cwd("/"); err != nil {
		t.Fatalf("cwd failed: %v", err)
	}

	list, err := client.List("")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) == 0 {
		t.Errorf("expected listing with files, got 0")
	}

	_ = client.Quit()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	_ = <-errCh
}
