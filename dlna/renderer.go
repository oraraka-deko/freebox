package dlna

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// RendererConfig defines the configuration for a DLNA Media Renderer.
type RendererConfig struct {
	FriendlyName string
	Manufacturer string
	ModelName    string
	ModelNumber  string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultRendererConfig returns the default DLNA renderer configuration.
func DefaultRendererConfig() RendererConfig {
	return RendererConfig{
		FriendlyName: "Freebox Media Renderer",
		Manufacturer: "Freebox",
		ModelName:    "Freebox-DMR",
		ModelNumber:  "1.0",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
}

// Renderer represents a DLNA Digital Media Renderer (DMR).
type Renderer struct {
	config     RendererConfig
	httpServer *http.Server
	listener   net.Listener
	mu         sync.Mutex
	running    bool
}

// NewRenderer creates a new DLNA media renderer instance.
func NewRenderer(cfg RendererConfig) *Renderer {
	r := &Renderer{
		config: cfg,
	}
	mux := http.NewServeMux()
	r.registerRoutes(mux)

	r.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}
	return r
}

func (r *Renderer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/renderer.xml", r.handleDeviceDescription)
	mux.HandleFunc("/AVTransport/control", r.handleAVTransportControl)
	mux.HandleFunc("/RenderingControl/control", r.handleRenderingControl)
}

func (r *Renderer) handleDeviceDescription(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	xmlDesc := fmt.Sprintf(`<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion>
    <major>1</major>
    <minor>0</minor>
  </specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>%s</friendlyName>
    <manufacturer>%s</manufacturer>
    <modelName>%s</modelName>
    <modelNumber>%s</modelNumber>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:AVTransport</serviceId>
        <SCPDURL>/AVTransport/scpd.xml</SCPDURL>
        <controlURL>/AVTransport/control</controlURL>
        <eventSubURL>/AVTransport/events</eventSubURL>
      </service>
      <service>
        <serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId>
        <SCPDURL>/RenderingControl/scpd.xml</SCPDURL>
        <controlURL>/RenderingControl/control</controlURL>
        <eventSubURL>/RenderingControl/events</eventSubURL>
      </service>
    </serviceList>
  </device>
</root>`, r.config.FriendlyName, r.config.Manufacturer, r.config.ModelName, r.config.ModelNumber)
	_, _ = w.Write([]byte(xmlDesc))
}

func (r *Renderer) handleAVTransportControl(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	resp := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:PlayResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"/>
  </s:Body>
</s:Envelope>`
	_, _ = w.Write([]byte(resp))
}

func (r *Renderer) handleRenderingControl(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	resp := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:GetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1">
      <CurrentVolume>50</CurrentVolume>
    </u:GetVolumeResponse>
  </s:Body>
</s:Envelope>`
	_, _ = w.Write([]byte(resp))
}

// Serve accepts incoming connections on the listener l.
func (r *Renderer) Serve(l net.Listener) error {
	r.mu.Lock()
	r.listener = l
	r.running = true
	r.mu.Unlock()

	err := r.httpServer.Serve(l)
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()

	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ListenAndServe listens on the TCP network address addr and then calls Serve.
func (r *Renderer) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":8201"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dlna renderer listen failed: %w", err)
	}
	return r.Serve(l)
}

// Shutdown gracefully shuts down the renderer without interrupting active connections.
func (r *Renderer) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running && r.listener == nil {
		return nil
	}
	return r.httpServer.Shutdown(ctx)
}

// Close immediately closes all active listeners and connections.
func (r *Renderer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running && r.listener == nil {
		return nil
	}
	return r.httpServer.Close()
}
