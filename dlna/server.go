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

// Config defines the configuration for a DLNA Media Server.
type Config struct {
	FriendlyName string
	Manufacturer string
	ModelName    string
	ModelNumber  string
	RootDir      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultConfig returns the default DLNA server configuration.
func DefaultConfig() Config {
	return Config{
		FriendlyName: "Freebox Media Server",
		Manufacturer: "Freebox",
		ModelName:    "Freebox-DMS",
		ModelNumber:  "1.0",
		RootDir:      "./media",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
}

// Server represents a DLNA Digital Media Server (DMS).
type Server struct {
	config     Config
	httpServer *http.Server
	listener   net.Listener
	mu         sync.Mutex
	running    bool
}

// NewServer creates a new DLNA media server instance.
func NewServer(cfg Config) *Server {
	s := &Server{
		config: cfg,
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}
	return s
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/description.xml", s.handleDeviceDescription)
	mux.HandleFunc("/ContentDirectory/scpd.xml", s.handleContentDirectorySCPD)
	mux.HandleFunc("/ContentDirectory/control", s.handleContentDirectoryControl)
	if s.config.RootDir != "" {
		mux.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(s.config.RootDir))))
	}
}

func (s *Server) handleDeviceDescription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	xmlDesc := fmt.Sprintf(`<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion>
    <major>1</major>
    <minor>0</minor>
  </specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
    <friendlyName>%s</friendlyName>
    <manufacturer>%s</manufacturer>
    <modelName>%s</modelName>
    <modelNumber>%s</modelNumber>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId>
        <SCPDURL>/ContentDirectory/scpd.xml</SCPDURL>
        <controlURL>/ContentDirectory/control</controlURL>
        <eventSubURL>/ContentDirectory/events</eventSubURL>
      </service>
    </serviceList>
  </device>
</root>`, s.config.FriendlyName, s.config.Manufacturer, s.config.ModelName, s.config.ModelNumber)
	_, _ = w.Write([]byte(xmlDesc))
}

func (s *Server) handleContentDirectorySCPD(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	scpd := `<?xml version="1.0"?>
<scpd xmlns="urn:schemas-upnp-org:service-1-0">
  <specVersion>
    <major>1</major>
    <minor>0</minor>
  </specVersion>
  <actionList>
    <action>
      <name>Browse</name>
    </action>
    <action>
      <name>GetSystemUpdateID</name>
    </action>
  </actionList>
</scpd>`
	_, _ = w.Write([]byte(scpd))
}

func (s *Server) handleContentDirectoryControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	resp := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <Result>&lt;DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"/&gt;</Result>
      <NumberReturned>0</NumberReturned>
      <TotalMatches>0</TotalMatches>
      <UpdateID>1</UpdateID>
    </u:BrowseResponse>
  </s:Body>
</s:Envelope>`
	_, _ = w.Write([]byte(resp))
}

// Serve accepts incoming connections on the listener l, creating a new service goroutine for each.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.running = true
	s.mu.Unlock()

	err := s.httpServer.Serve(l)
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ListenAndServe listens on the TCP network address addr and then calls Serve to handle incoming requests.
func (s *Server) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":8200"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dlna server listen failed: %w", err)
	}
	return s.Serve(l)
}

// Shutdown gracefully shuts down the server without interrupting any active connections.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running && s.listener == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// Close immediately closes all active listeners and connections.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running && s.listener == nil {
		return nil
	}
	return s.httpServer.Close()
}
