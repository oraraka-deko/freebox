package ipc

import (
	"bufio"
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	req := Request{JSONRPC: "2.0", ID: 1, Method: "vfs.listDir"}
	if err := WriteFrame(&buf, req); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	r := bufio.NewReader(&buf)
	var got Request
	if err := ReadFrame(r, &got); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Method != req.Method {
		t.Fatalf("method mismatch: got %q want %q", got.Method, req.Method)
	}
}

func TestReadFrameRejectsOversized(t *testing.T) {
	var buf bytes.Buffer
	header := []byte{0xFF, 0xFF, 0xFF, 0xFF} // huge length prefix
	buf.Write(header)

	r := bufio.NewReader(&buf)
	var got Request
	if err := ReadFrame(r, &got); err == nil {
		t.Fatal("expected error for oversized frame, got nil")
	}
}
