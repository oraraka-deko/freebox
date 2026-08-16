package http

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHTTPClientServer(t *testing.T) {
	cfg := DefaultConfig()
	server := NewServer(cfg)

	server.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.0"}`))
	})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(l)
	}()

	client := NewClient(WithTimeout(2 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var data struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := client.GetJSON(ctx, "http://"+l.Addr().String()+"/api/info", &data); err != nil {
		t.Fatalf("GetJSON failed: %v", err)
	}

	if data.Status != "ok" || data.Version != "1.0" {
		t.Errorf("unexpected response: %+v", data)
	}

	_ = server.Shutdown(ctx)
	_ = <-errCh
}

func TestWebDAVClientServer(t *testing.T) {
	cfg := DefaultWebDAVConfig()
	cfg.RootDir = t.TempDir()
	cfg.Prefix = "/webdav"
	server := NewWebDAVServer(cfg)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(l)
	}()

	client := NewWebDAVClient("http://"+l.Addr().String()+"/webdav", "", "", 2*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Put file
	if err := client.Put(ctx, "/test.txt", bytes.NewBufferString("hello webdav")); err != nil {
		t.Fatalf("put failed: %v", err)
	}

	// 2. Get file
	r, err := client.Get(ctx, "/test.txt")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	content, _ := io.ReadAll(r)
	_ = r.Close()
	if string(content) != "hello webdav" {
		t.Errorf("unexpected content: %s", string(content))
	}

	// 3. Propfind
	resources, err := client.Propfind(ctx, "/", "1")
	if err != nil {
		t.Fatalf("propfind failed: %v", err)
	}
	if len(resources) == 0 {
		t.Errorf("expected at least 1 resource")
	}

	// 4. Mkcol
	if err := client.Mkcol(ctx, "/folder"); err != nil {
		t.Fatalf("mkcol failed: %v", err)
	}

	// 5. Delete file
	if err := client.Delete(ctx, "/test.txt"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_ = server.Shutdown(ctx)
	_ = <-errCh
}
