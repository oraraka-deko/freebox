package ipc

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testClient is a minimal control-connection client for exercising the server in tests.
type testClient struct {
	t    *testing.T
	nc   net.Conn
	r    *bufio.Reader
	next int
}

func newTestClient(t *testing.T, socketPath string) *testClient {
	t.Helper()
	nc, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := nc.Write(MagicControl[:]); err != nil {
		t.Fatalf("write magic: %v", err)
	}
	return &testClient{t: t, nc: nc, r: bufio.NewReader(nc), next: 1}
}

func (c *testClient) call(method string, params any) map[string]any {
	c.t.Helper()
	id := c.next
	c.next++
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		c.t.Fatalf("marshal params: %v", err)
	}
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: paramsRaw}
	if err := WriteFrame(c.nc, req); err != nil {
		c.t.Fatalf("write request: %v", err)
	}

	for {
		var resp Response
		if err := ReadFrame(c.r, &resp); err != nil {
			c.t.Fatalf("read response: %v", err)
		}
		if resp.ID == nil {
			continue // skip notifications
		}
		if resp.Error != nil {
			c.t.Fatalf("%s failed: %s", method, resp.Error.Message)
		}
		m, _ := resp.Result.(map[string]any)
		return m
	}
}

func (c *testClient) callRaw(method string, params any) any {
	c.t.Helper()
	id := c.next
	c.next++
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		c.t.Fatalf("marshal params: %v", err)
	}
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: paramsRaw}
	if err := WriteFrame(c.nc, req); err != nil {
		c.t.Fatalf("write request: %v", err)
	}

	for {
		var resp Response
		if err := ReadFrame(c.r, &resp); err != nil {
			c.t.Fatalf("read response: %v", err)
		}
		if resp.ID == nil {
			continue // skip notifications
		}
		if resp.Error != nil {
			c.t.Fatalf("%s failed: %s", method, resp.Error.Message)
		}
		return resp.Result
	}
}

func (c *testClient) readNotification() Response {
	c.t.Helper()
	for {
		var resp Response
		if err := ReadFrame(c.r, &resp); err != nil {
			c.t.Fatalf("read notification: %v", err)
		}
		if resp.ID == nil && resp.Method != "" {
			return resp
		}
	}
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	inst, err := NewInstance(InstanceConfig{DBPath: filepath.Join(dir, "freebox.db")})
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	t.Cleanup(inst.Close)

	socketPath := filepath.Join(dir, "freebox.sock")
	srv := NewServer(inst, socketPath, "")
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv, socketPath
}

func TestVFSMountWriteReadListDir(t *testing.T) {
	_, socketPath := newTestServer(t)
	dataDir := t.TempDir()

	c := newTestClient(t, socketPath)
	defer c.nc.Close()

	c.call("vfs.mount", map[string]any{"name": "test", "type": "local", "path": dataDir})

	c.call("vfs.writeFile", map[string]any{"path": "test:hello.txt", "data": []byte("hello world")})

	read := c.call("vfs.readFile", map[string]any{"path": "test:hello.txt"})
	dataStr, ok := read["data"].(string)
	if !ok {
		t.Fatalf("unexpected data type: %T", read["data"])
	}
	decoded, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != "hello world" {
		t.Fatalf("unexpected file content: %v", decoded)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "hello.txt")); err != nil {
		t.Fatalf("expected file on disk: %v", err)
	}
}

func TestTasksSubmitAndNotify(t *testing.T) {
	_, socketPath := newTestServer(t)
	dataDir := t.TempDir()

	c := newTestClient(t, socketPath)
	defer c.nc.Close()

	c.call("vfs.mount", map[string]any{"name": "test", "type": "local", "path": dataDir})
	c.call("vfs.writeFile", map[string]any{"path": "test:src.txt", "data": []byte("copy me")})

	result := c.call("tasks.submit", map[string]any{
		"type": "COPY",
		"src":  "test:src.txt",
		"dst":  "test:dst.txt",
	})
	taskID, _ := result["taskId"].(string)
	if taskID == "" {
		t.Fatalf("expected taskId, got %v", result)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for task completion notification")
		default:
		}
		notif := c.readNotification()
		if notif.Method != "task.status" {
			continue
		}
		params, _ := notif.Result.(map[string]any)
		if params["status"] == "COMPLETED" {
			return
		}
		if params["status"] == "FAILED" {
			t.Fatalf("task failed: %v", params["error"])
		}
	}
}
