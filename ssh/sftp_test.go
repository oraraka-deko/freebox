package ssh

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSSHClientServer(t *testing.T) {
	cfg := DefaultConfig()
	server := NewServer(cfg)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(l)
	}()

	client, err := Dial(l.Addr().String(), "SSH-2.0-TestClient", 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial ssh server: %v", err)
	}
	defer client.Close()

	banner := client.ServerBanner()
	if !strings.HasPrefix(banner, "SSH-") {
		t.Errorf("unexpected server banner: %s", banner)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	_ = <-errCh
}
