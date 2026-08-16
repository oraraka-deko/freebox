package client

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"freebox/api"
	"freebox/auth"
	"freebox/engine"
	"freebox/storage"
	"freebox/vfs"
)

func setupTestServer(t *testing.T) (*httptest.Server, func()) {
	tempDir, err := os.MkdirTemp("", "freebox-sdk-test-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}

	dbPath := filepath.Join(tempDir, "sdk.db")
	db, err := storage.Open(storage.Config{Path: dbPath, Passphrase: "sdk-test-secret"})
	if err != nil {
		t.Fatalf("storage open failed: %v", err)
	}

	authMgr, err := auth.NewManager(db, auth.ManagerConfig{
		SessionTTL:       2 * time.Hour,
		DefaultAdminUser: "admin",
		DefaultAdminPass: "admin123",
	})
	if err != nil {
		t.Fatalf("auth manager failed: %v", err)
	}

	eng := engine.NewEngine(engine.DefaultEngineConfig())
	reg := vfs.NewRegistry(db)

	srv := api.NewServer(api.ServerConfig{
		AuthMgr:   authMgr,
		StorageDB: db,
		Mounts:    reg,
		Engine:    eng,
	})

	ts := httptest.NewServer(srv.Handler())

	cleanup := func() {
		ts.Close()
		_ = srv.Close()
		eng.Close()
		_ = db.Close()
		_ = os.RemoveAll(tempDir)
	}

	return ts, cleanup
}

func TestClientSDK_EndToEnd(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	c := NewClient(ts.URL)
	defer c.Close()

	// 1. Authenticate
	session, err := c.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("client login failed: %v", err)
	}
	if session.Token == "" {
		t.Fatalf("empty token returned")
	}

	// 2. Connect WebSocket
	wsChan, err := c.ConnectWebSocket()
	if err != nil {
		t.Fatalf("websocket connect failed: %v", err)
	}
	if err := c.SubscribeWS("tasks"); err != nil {
		t.Fatalf("subscribe tasks failed: %v", err)
	}

	// Wait for subscription confirmation event
	select {
	case evt := <-wsChan:
		if evt.Type != "subscribed" {
			t.Logf("received ws event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Logf("timed out waiting for subscription ack")
	}

	// 3. Mount source and destination virtual storage
	err = c.Mount("node-a", vfs.MountConfig{Type: "mem"}, true)
	if err != nil {
		t.Fatalf("mount node-a failed: %v", err)
	}

	err = c.Mount("node-b", vfs.MountConfig{Type: "mem"}, true)
	if err != nil {
		t.Fatalf("mount node-b failed: %v", err)
	}

	mounts, err := c.ListMounts()
	if err != nil || len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d (err: %v)", len(mounts), err)
	}

	// 4. Upload file to node-a
	payload := []byte("Freebox Distributed File Content 12345")
	err = c.Upload("node-a", "/data/file.bin", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}

	// 5. Stat file
	info, err := c.Stat("node-a", "/data/file.bin")
	if err != nil || info.Size != int64(len(payload)) {
		t.Fatalf("stat failed or size mismatch: %v, info: %+v", err, info)
	}

	// 6. Download file
	rc, err := c.Download("node-a", "/data/file.bin")
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	downloaded, _ := io.ReadAll(rc)
	rc.Close()
	if string(downloaded) != string(payload) {
		t.Fatalf("downloaded data mismatch: %s", string(downloaded))
	}

	// 7. Transfer file from node-a to node-b (orchestrated task)
	taskResp, err := c.Transfer("node-a", "/data/file.bin", "node-b", "/backup/file.bin", false)
	if err != nil {
		t.Fatalf("transfer initiation failed: %v", err)
	}
	if taskResp.ID == "" {
		t.Fatalf("empty task id returned")
	}

	// Wait for transfer completion
	time.Sleep(100 * time.Millisecond)

	// Verify transferred file exists on node-b
	dstInfo, err := c.Stat("node-b", "/backup/file.bin")
	if err != nil || dstInfo.Size != int64(len(payload)) {
		t.Fatalf("transferred file not found on node-b: %v", err)
	}

	// 8. Stream proxy registration & playback URL
	err = c.RegisterStream("video1", "node-a", "/data/file.bin", "")
	if err != nil {
		t.Fatalf("register stream failed: %v", err)
	}
	streamURL := c.StreamURL("video1")
	if streamURL == "" {
		t.Fatalf("stream URL is empty")
	}

	// 9. Inspect task list
	tasks, err := c.ListTasks()
	if err != nil || len(tasks) == 0 {
		t.Fatalf("expected at least 1 task in history, got %d (err: %v)", len(tasks), err)
	}

	// 10. Logout
	if err := c.Logout(); err != nil {
		t.Fatalf("logout failed: %v", err)
	}
}
