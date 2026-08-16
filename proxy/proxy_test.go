package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"freebox/vfs"
)

func TestStreamProxySeekAndRandomAccess(t *testing.T) {
	// Create a large 10MB virtual dataset (e.g. video file)
	largeData := make([]byte, 10*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	memSrc := NewMemSource(largeData)

	// Configure stream proxy with small chunk size (16KB) and max 4 chunks in RAM (64KB total memory usage!)
	cfg := StreamProxyConfig{
		ChunkSize:      16 * 1024,
		MaxMemoryChunks: 4,
	}

	stream := NewStreamProxy(memSrc, cfg)
	defer stream.Close()

	if stream.Size() != 10*1024*1024 {
		t.Fatalf("expected size 10MB, got %d", stream.Size())
	}

	// 1. Seek to end of file (simulate media player seeking to MOOV atom or end timestamp)
	seekPos := int64(9 * 1024 * 1024)
	newOff, err := stream.Seek(seekPos, io.SeekStart)
	if err != nil || newOff != seekPos {
		t.Fatalf("seek failed: %v", err)
	}

	buf := make([]byte, 100)
	n, err := stream.Read(buf)
	if err != nil || n != len(buf) {
		t.Fatalf("read after seek failed: %v, n=%d", err, n)
	}

	expected := largeData[seekPos : seekPos+100]
	if !bytes.Equal(buf, expected) {
		t.Errorf("data mismatch after seek")
	}

	// 2. Random ReadAt
	randBuf := make([]byte, 50)
	randOff := int64(2*1024*1024 + 500)
	n, err = stream.ReadAt(randBuf, randOff)
	if err != nil || n != len(randBuf) {
		t.Fatalf("readAt failed: %v", err)
	}
	if !bytes.Equal(randBuf, largeData[randOff:randOff+50]) {
		t.Errorf("readAt data mismatch")
	}
}

func TestStreamProxyRandomWriteAccess(t *testing.T) {
	memFS := vfs.NewMemFS()
	_ = memFS.Write("/media/video.mp4", []byte("AAAAABBBBBCCCCCDDDDD"))

	vfsSrc, err := NewVFSSource(memFS, "/media/video.mp4", false)
	if err != nil {
		t.Fatalf("failed to create VFS source: %v", err)
	}

	cfg := StreamProxyConfig{
		ChunkSize:      4,
		MaxMemoryChunks: 2,
	}

	stream := NewStreamProxy(vfsSrc, cfg)

	// Random write at offset 5 (replacing "BBBBB" with "XXXXX")
	patch := []byte("XXXXX")
	n, err := stream.WriteAt(patch, 5)
	if err != nil || n != len(patch) {
		t.Fatalf("writeAt failed: %v", err)
	}

	// Read back through stream
	readBuf := make([]byte, 5)
	_, _ = stream.ReadAt(readBuf, 5)
	if string(readBuf) != "XXXXX" {
		t.Errorf("expected 'XXXXX', got '%s'", string(readBuf))
	}

	// Flush and close to write back to VFS source
	if err := stream.Close(); err != nil {
		t.Fatalf("close/flush failed: %v", err)
	}

	// Verify persistence in underlying VFS
	persisted, err := memFS.Read("/media/video.mp4")
	if err != nil {
		t.Fatalf("vfs read failed: %v", err)
	}
	if string(persisted) != "AAAAAXXXXXCCCCCDDDDD" {
		t.Errorf("expected 'AAAAAXXXXXCCCCCDDDDD', got '%s'", string(persisted))
	}
}

func TestMultiSourceProxy(t *testing.T) {
	part1 := NewMemSource([]byte("PART1_"))
	part2 := NewMemSource([]byte("PART2_"))
	part3 := NewMemSource([]byte("PART3_"))

	cfg := DefaultStreamProxyConfig()
	multiStream := NewMultiStreamProxy(cfg, part1, part2, part3)
	defer multiStream.Close()

	if multiStream.Size() != 18 {
		t.Fatalf("expected total composite size 18, got %d", multiStream.Size())
	}

	// Seek across boundary of part 1 and part 2 (offset 4)
	buf := make([]byte, 6)
	_, err := multiStream.Seek(4, io.SeekStart)
	if err != nil {
		t.Fatalf("seek failed: %v", err)
	}

	n, err := multiStream.Read(buf)
	if err != nil || n != 6 {
		t.Fatalf("multi read failed: %v", err)
	}
	// "1_PART"
	if string(buf) != "1_PART" {
		t.Errorf("expected '1_PART', got '%s'", string(buf))
	}
}

func TestStreamServerHTTPRangeSeeking(t *testing.T) {
	mediaContent := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	src := NewMemSource(mediaContent)

	server := NewStreamServer()
	server.RegisterSource("video.mkv", src)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go func() { _ = server.Serve(lis) }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	addr := lis.Addr().String()
	url := fmt.Sprintf("http://%s/stream/video.mkv", addr)

	// 1. Request byte range (bytes=10-15)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=10-15")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP range request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected status 206 Partial Content, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ABCDEF" {
		t.Errorf("expected 'ABCDEF', got '%s'", string(body))
	}

	// 2. Request suffix range (bytes=-5) -> last 5 bytes "VWXYZ"
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Range", "bytes=-5")

	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP suffix range request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected status 206, got %d", resp2.StatusCode)
	}

	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "VWXYZ" {
		t.Errorf("expected 'VWXYZ', got '%s'", string(body2))
	}
}
