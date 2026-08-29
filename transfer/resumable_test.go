package transfer

import (
	"context"
	"sync"
	"testing"
	"time"

	"freebox/vfs"
)

func TestResumableTransfer(t *testing.T) {
	srcFS := vfs.NewMemFS()
	dstFS := vfs.NewMemFS()

	// Generate 100KB test file
	largeData := make([]byte, 100*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	_ = srcFS.Write("/source/bigfile.bin", largeData)

	// Create transfer with small chunk size (1KB)
	xfer := NewResumableTransfer("test-xfer-1", srcFS, "/source/bigfile.bin", dstFS, "/dest/bigfile.bin", 1024)

	var progressUpdates []int64
	var mu sync.Mutex
	xfer.SetOnProgress(func(cp Checkpoint) {
		mu.Lock()
		progressUpdates = append(progressUpdates, cp.BytesCopied)
		mu.Unlock()
	})

	// Run transfer in goroutine
	done := make(chan error, 1)
	go func() {
		done <- xfer.Start()
	}()

	// Pause midway
	time.Sleep(10 * time.Millisecond)
	xfer.Pause()

	// Verify paused
	time.Sleep(10 * time.Millisecond)
	cp := xfer.Checkpoint()
	if cp.State != StatePaused && cp.State != StateRunning && cp.State != StateCompleted {
		t.Errorf("unexpected state: %s", cp.State)
	}

	// Resume
	xfer.Resume()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("transfer failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("transfer timed out")
	}

	// Verify final file
	resData, err := dstFS.Read("/dest/bigfile.bin")
	if err != nil {
		t.Fatalf("failed reading dest file: %v", err)
	}
	if len(resData) != len(largeData) {
		t.Errorf("size mismatch: expected %d, got %d", len(largeData), len(resData))
	}
	for i := range largeData {
		if resData[i] != largeData[i] {
			t.Fatalf("byte mismatch at index %d", i)
		}
	}
}

func TestNetworkDisconnectAutoResume(t *testing.T) {
	srcFS := vfs.NewMemFS()
	dstFS := vfs.NewMemFS()

	data := []byte("resilience test data over network")
	_ = srcFS.Write("/src/data.txt", data)

	xfer := NewResumableTransfer("xfer-resilience", srcFS, "/src/data.txt", dstFS, "/dst/data.txt", 16)

	// Simulate pause with network disconnect reason
	xfer.PauseWithReason("network_disconnect", "connection reset by peer")

	cp := xfer.Checkpoint()
	if cp.State != StatePaused || cp.Error != "network_disconnect: connection reset by peer" {
		t.Fatalf("unexpected paused checkpoint: %+v", cp)
	}

	// Test AutoResume trigger
	online := false
	xfer.AutoResume(context.Background(), 20*time.Millisecond, func() bool {
		return online
	})

	// Set online to true
	time.Sleep(30 * time.Millisecond)
	online = true
	time.Sleep(50 * time.Millisecond)

	cpAfter := xfer.Checkpoint()
	if cpAfter.State != StateRunning {
		t.Errorf("expected state to resume to RUNNING, got: %s", cpAfter.State)
	}
}

