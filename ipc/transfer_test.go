package ipc

import (
	"bytes"
	"encoding/hex"
	"io"
	"net"
	"testing"
)

func dialTransfer(t *testing.T, socketPath, token string) net.Conn {
	t.Helper()
	nc, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial transfer: %v", err)
	}
	if _, err := nc.Write(MagicTransfer[:]); err != nil {
		t.Fatalf("write magic: %v", err)
	}
	raw, err := hex.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if _, err := nc.Write(raw); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return nc
}

func TestFastTransferWriteThenRead(t *testing.T) {
	_, socketPath := newTestServer(t)
	dataDir := t.TempDir()

	c := newTestClient(t, socketPath)
	defer c.nc.Close()

	c.call("vfs.mount", map[string]any{"name": "test", "type": "local", "path": dataDir})

	// Write a multi-MB payload through the fast path.
	payload := bytes.Repeat([]byte("0123456789abcdef"), 500000) // 8MB

	openResp := c.call("transfer.open", map[string]any{"mode": "write", "path": "test:big.bin"})
	token, _ := openResp["token"].(string)
	if token == "" {
		t.Fatalf("expected token, got %v", openResp)
	}

	wc := dialTransfer(t, socketPath, token)
	if _, err := wc.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("close write conn: %v", err)
	}

	waitForNotification(t, c, "transfer.complete")

	// Read it back through the fast path and verify byte-for-byte equality.
	readResp := c.call("transfer.open", map[string]any{"mode": "read", "path": "test:big.bin"})
	readToken, _ := readResp["token"].(string)
	if readToken == "" {
		t.Fatalf("expected token, got %v", readResp)
	}

	rc := dialTransfer(t, socketPath, readToken)
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	waitForNotification(t, c, "transfer.complete")

	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d bytes", len(got), len(payload))
	}
}

func waitForNotification(t *testing.T, c *testClient, method string) {
	t.Helper()
	for {
		notif := c.readNotification()
		if notif.Method == method {
			return
		}
		if notif.Method == "transfer.error" {
			t.Fatalf("transfer error: %v", notif.Result)
		}
	}
}
