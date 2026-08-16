package smb

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Share defines a shared directory configuration for SMB.
type Share struct {
	Name     string
	Path     string
	ReadOnly bool
}

// Config defines the configuration for the SMB/Samba server.
type Config struct {
	ServerName     string
	Workgroup      string
	Shares         map[string]Share
	MaxConnections int
	IdleTimeout    time.Duration
}

// DefaultConfig returns default SMB configuration.
func DefaultConfig() Config {
	return Config{
		ServerName:     "FREEBOX-SMB",
		Workgroup:      "WORKGROUP",
		Shares:         make(map[string]Share),
		MaxConnections: 100,
		IdleTimeout:    10 * time.Minute,
	}
}

// Server represents an SMB/Samba server.
type Server struct {
	config    Config
	listener  net.Listener
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	closed    bool
	closeChan chan struct{}
}

// NewServer creates a new SMB server instance.
func NewServer(cfg Config) *Server {
	if cfg.Shares == nil {
		cfg.Shares = make(map[string]Share)
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 10 * time.Minute
	}
	return &Server{
		config:    cfg,
		conns:     make(map[net.Conn]struct{}),
		closeChan: make(chan struct{}),
	}
}

// AddShare registers a new shared directory.
func (s *Server) AddShare(name string, path string, readOnly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Shares[name] = Share{
		Name:     name,
		Path:     filepath.Clean(path),
		ReadOnly: readOnly,
	}
	_ = os.MkdirAll(path, 0755)
}

// Serve accepts incoming connections on listener l and serves SMB sessions.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.closed = false
	s.mu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return fmt.Errorf("smb accept error: %w", err)
		}

		s.mu.Lock()
		if s.config.MaxConnections > 0 && len(s.conns) >= s.config.MaxConnections {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		go s.handleConn(conn)
	}
}

// ListenAndServe listens on TCP address addr and serves SMB connections.
func (s *Server) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":445"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("smb listen failed: %w", err)
	}
	return s.Serve(l)
}

// Shutdown gracefully shuts down the SMB server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closeChan)

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}

	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()

	return err
}

// Close immediately closes all active listeners and connections.
func (s *Server) Close() error {
	return s.Shutdown(context.Background())
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	// NetBIOS Session header / Direct TCP packet loop
	header := make([]byte, 4)
	for {
		if s.config.IdleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))
		}

		_, err := io.ReadFull(conn, header)
		if err != nil {
			return
		}

		// Direct TCP / NetBIOS header: 1 byte type, 3 bytes length
		length := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		if length <= 0 || length > 0x1000000 {
			return
		}

		payload := make([]byte, length)
		_, err = io.ReadFull(conn, payload)
		if err != nil {
			return
		}

		resp := s.processSMBPayload(payload)
		if resp != nil {
			respHeader := make([]byte, 4)
			binary.BigEndian.PutUint32(respHeader, uint32(len(resp)))
			if _, err := conn.Write(respHeader); err != nil {
				return
			}
			if _, err := conn.Write(resp); err != nil {
				return
			}
		}
	}
}

// processSMBPayload inspects SMB protocol header (SMB1 \xFFSMB or SMB2 \xFE'S''M''B')
func (s *Server) processSMBPayload(payload []byte) []byte {
	if len(payload) < 4 {
		return nil
	}

	// SMB2 / SMB3 packet check: 0xFE, 'S', 'M', 'B'
	if payload[0] == 0xFE && payload[1] == 'S' && payload[2] == 'M' && payload[3] == 'B' {
		return s.handleSMB2(payload)
	}

	// SMB1 packet check: 0xFF, 'S', 'M', 'B'
	if payload[0] == 0xFF && payload[1] == 'S' && payload[2] == 'M' && payload[3] == 'B' {
		return s.handleSMB1(payload)
	}

	return nil
}

func (s *Server) handleSMB2(req []byte) []byte {
	if len(req) < 64 {
		return nil
	}
	// SMB2 Header: StructureSize (64 bytes)
	command := binary.LittleEndian.Uint16(req[12:14])
	messageID := binary.LittleEndian.Uint64(req[24:32])

	resp := make([]byte, 64)
	resp[0] = 0xFE
	resp[1] = 'S'
	resp[2] = 'M'
	resp[3] = 'B'
	binary.LittleEndian.PutUint16(resp[4:6], 64) // Header Size
	binary.LittleEndian.PutUint16(resp[12:14], command)
	// Flags: 0x01 (SMB2_FLAGS_SERVER_TO_REDIR)
	binary.LittleEndian.PutUint32(resp[16:20], 0x00000001)
	binary.LittleEndian.PutUint64(resp[24:32], messageID)

	// Switch on SMB2 Command (0 = Negotiate)
	switch command {
	case 0: // SMB2 Negotiate
		body := make([]byte, 65)
		binary.LittleEndian.PutUint16(body[0:2], 65)     // StructureSize
		binary.LittleEndian.PutUint16(body[2:4], 0x0202) // DialectRevision (SMB 2.0.2)
		return append(resp, body...)
	default:
		// Return STATUS_NOT_SUPPORTED (0xC00000BB) in NTStatus field
		binary.LittleEndian.PutUint32(resp[8:12], 0xC00000BB)
		return resp
	}
}

func (s *Server) handleSMB1(req []byte) []byte {
	if len(req) < 32 {
		return nil
	}
	command := req[4]

	resp := make([]byte, 32)
	copy(resp[0:4], []byte{0xFF, 'S', 'M', 'B'})
	resp[4] = command
	// Flags: response flag
	resp[9] = 0x80

	switch command {
	case 0x72: // SMB_COM_NEGOTIATE
		// Negotiate Protocol response
		body := []byte{
			0x01,                   // WordCount
			0x00, 0x00,             // Selected Dialect Index (0)
			0x00, 0x00,             // ByteCount
		}
		return append(resp, body...)
	default:
		// Error response
		binary.LittleEndian.PutUint32(resp[5:9], 0xC00000BB)
		return resp
	}
}
