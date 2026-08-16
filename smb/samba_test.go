package smb

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSMBClientServer(t *testing.T) {
	cfg := DefaultConfig()
	server := NewServer(cfg)
	server.AddShare("Public", t.TempDir(), false)

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
		t.Fatalf("failed to dial smb server: %v", err)
	}
	defer client.Close()

	dialect, err := client.Negotiate()
	if err != nil {
		t.Fatalf("negotiate failed: %v", err)
	}
	if dialect != 0x0202 {
		t.Errorf("expected dialect 0x0202, got 0x%04x", dialect)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	_ = <-errCh
}
