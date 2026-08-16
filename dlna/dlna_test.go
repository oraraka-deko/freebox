package dlna

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestDLNAClientServer(t *testing.T) {
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

	client := NewClient(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dev, err := client.GetDeviceDescription(ctx, "http://"+l.Addr().String()+"/description.xml")
	if err != nil {
		t.Fatalf("failed to get device description: %v", err)
	}

	if dev.FriendlyName != cfg.FriendlyName {
		t.Errorf("expected friendly name %s, got %s", cfg.FriendlyName, dev.FriendlyName)
	}

	browseResp, err := client.Browse(ctx, "http://"+l.Addr().String()+"/ContentDirectory/control", "0")
	if err != nil {
		t.Fatalf("failed to browse: %v", err)
	}
	if len(browseResp) == 0 {
		t.Errorf("empty browse response")
	}

	_ = server.Shutdown(ctx)
	_ = <-errCh
}

func TestDLNARendererClientServer(t *testing.T) {
	cfg := DefaultRendererConfig()
	renderer := NewRenderer(cfg)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- renderer.Serve(l)
	}()

	client := NewClient(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dev, err := client.GetDeviceDescription(ctx, "http://"+l.Addr().String()+"/renderer.xml")
	if err != nil {
		t.Fatalf("failed to get renderer description: %v", err)
	}
	if dev.FriendlyName != cfg.FriendlyName {
		t.Errorf("expected %s, got %s", cfg.FriendlyName, dev.FriendlyName)
	}

	if err := client.Play(ctx, "http://"+l.Addr().String()+"/AVTransport/control"); err != nil {
		t.Fatalf("failed to send play command: %v", err)
	}

	_ = renderer.Shutdown(ctx)
	_ = <-errCh
}
