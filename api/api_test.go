package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"freebox/auth"
	"freebox/engine"
	"freebox/storage"
	"freebox/vfs"
)

func setupTestAPIServer(t *testing.T) (*Server, *httptest.Server, *auth.Session, func()) {
	tempDir, err := os.MkdirTemp("", "freebox-api-test-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}

	dbPath := filepath.Join(tempDir, "api.db")
	db, err := storage.Open(storage.Config{Path: dbPath, Passphrase: "test-api-secret"})
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

	srv := NewServer(ServerConfig{
		AuthMgr:   authMgr,
		StorageDB: db,
		Mounts:    reg,
		Engine:    eng,
	})

	ts := httptest.NewServer(srv.Handler())

	// Authenticate admin session
	session, err := authMgr.Authenticate("admin", "admin123")
	if err != nil {
		t.Fatalf("auth failed: %v", err)
	}

	cleanup := func() {
		ts.Close()
		_ = srv.Close()
		eng.Close()
		_ = db.Close()
		_ = os.RemoveAll(tempDir)
	}

	return srv, ts, session, cleanup
}

func authedRequest(method, url, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func TestAPIServer_FullLifecycle(t *testing.T) {
	_, ts, session, cleanup := setupTestAPIServer(t)
	defer cleanup()

	client := ts.Client()

	// 1. Test /api/auth/me
	req, _ := authedRequest(http.MethodGet, ts.URL+"/api/auth/me", session.Token, nil)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("auth me failed: status %v, err %v", resp.StatusCode, err)
	}

	// 2. Mount two virtual filesystems (src-mem and dst-mem)
	mountBody1, _ := json.Marshal(MountRequest{
		Name:   "src-mem",
		Config: vfs.MountConfig{Type: "mem"},
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/mounts", session.Token, bytes.NewReader(mountBody1))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("mount src-mem failed: status %v", resp.StatusCode)
	}

	mountBody2, _ := json.Marshal(MountRequest{
		Name:   "dst-mem",
		Config: vfs.MountConfig{Type: "mem"},
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/mounts", session.Token, bytes.NewReader(mountBody2))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("mount dst-mem failed: status %v", resp.StatusCode)
	}

	// 3. Upload file to src-mem
	fileContent := []byte("Hello Freebox Remote API World!")
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/vfs/src-mem/upload?path=/test.txt", session.Token, bytes.NewReader(fileContent))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("upload file failed: status %v", resp.StatusCode)
	}

	// 4. Stat file in src-mem
	req, _ = authedRequest(http.MethodGet, ts.URL+"/api/vfs/src-mem/stat?path=/test.txt", session.Token, nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("stat failed: status %v", resp.StatusCode)
	}
	var fi vfs.FileInfo
	_ = json.NewDecoder(resp.Body).Decode(&fi)
	if fi.Size != int64(len(fileContent)) {
		t.Fatalf("size mismatch: expected %d, got %d", len(fileContent), fi.Size)
	}

	// 5. Download file from src-mem
	req, _ = authedRequest(http.MethodGet, ts.URL+"/api/vfs/src-mem/download?path=/test.txt", session.Token, nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("download failed: status %v", resp.StatusCode)
	}
	downloaded, _ := io.ReadAll(resp.Body)
	if string(downloaded) != string(fileContent) {
		t.Fatalf("download content mismatch: expected %s, got %s", string(fileContent), string(downloaded))
	}

	// 6. Cross-Mount File Transfer (src-mem -> dst-mem)
	transferBody, _ := json.Marshal(VFSTransferRequest{
		SrcMount: "src-mem",
		SrcPath:  "/test.txt",
		DstMount: "dst-mem",
		DstPath:  "/copied.txt",
		Move:     false,
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/vfs/transfer", session.Token, bytes.NewReader(transferBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("transfer request failed: status %v", resp.StatusCode)
	}
	var taskResp TaskResponse
	_ = json.NewDecoder(resp.Body).Decode(&taskResp)

	// Wait briefly for transfer task execution
	time.Sleep(100 * time.Millisecond)

	// Verify copied file exists in dst-mem
	req, _ = authedRequest(http.MethodGet, ts.URL+"/api/vfs/dst-mem/stat?path=/copied.txt", session.Token, nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("transferred file not found in dst-mem: status %v", resp.StatusCode)
	}

	// 7. Register Stream Proxy Source
	streamBody, _ := json.Marshal(RegisterStreamRequest{
		Name:      "test-stream",
		MountName: "src-mem",
		Path:      "/test.txt",
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/streams", session.Token, bytes.NewReader(streamBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("register stream failed: status %v", resp.StatusCode)
	}

	// Play stream via public playback URL
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/stream/test-stream", nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("stream playback failed: status %v", resp.StatusCode)
	}
	streamedBytes, _ := io.ReadAll(resp.Body)
	if string(streamedBytes) != string(fileContent) {
		t.Fatalf("stream content mismatch")
	}

	// 8. Clipboard operations
	clipBody, _ := json.Marshal(map[string]interface{}{
		"mount": "src-mem",
		"path":  "/test.txt",
		"cut":   false,
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/clipboard", session.Token, bytes.NewReader(clipBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("clipboard add failed: status %v", resp.StatusCode)
	}

	// 9. Inspect servers status
	req, _ = authedRequest(http.MethodGet, ts.URL+"/api/servers", session.Token, nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get servers status failed: status %v", resp.StatusCode)
	}

	// 10. Test Search API
	searchBody, _ := json.Marshal(SearchAPIRequest{
		Mount:          "src-mem",
		RootPath:       "/",
		ContentPattern: "API",
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/search", session.Token, bytes.NewReader(searchBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("search API failed: status %v", resp.StatusCode)
	}

	// 11. Test Metadata API
	req, _ = authedRequest(http.MethodGet, ts.URL+"/api/meta?mount=src-mem&path=/test.txt", session.Token, nil)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("meta GET API failed: status %v", resp.StatusCode)
	}

	// 12. Test Remotes API
	remoteBody, _ := json.Marshal(map[string]interface{}{
		"name":       "my-remote",
		"type":       "mem",
		"auto_mount": true,
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/remotes", session.Token, bytes.NewReader(remoteBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("remotes POST API failed: status %v", resp.StatusCode)
	}

	// 13. Test Cert API
	certBody, _ := json.Marshal(map[string]interface{}{
		"common_name":   "test.domain",
		"validity_days": 30,
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/cert/generate", session.Token, bytes.NewReader(certBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("cert generate API failed: status %v", resp.StatusCode)
	}

	// 14. Test Dedup API
	dedupBody, _ := json.Marshal(DedupAPIRequest{
		Mount:    "src-mem",
		RootPath: "/",
		Method:   "meta",
		Action:   "report",
	})
	req, _ = authedRequest(http.MethodPost, ts.URL+"/api/dedup", session.Token, bytes.NewReader(dedupBody))
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("dedup API failed: status %v", resp.StatusCode)
	}
}

