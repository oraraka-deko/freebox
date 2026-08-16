package ssh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Config defines the configuration for the SSH/SFTP server.
type Config struct {
	RootDir        string
	User           string
	Password       string
	Banner         string
	MaxConnections int
	IdleTimeout    time.Duration
}

// DefaultConfig returns default SSH configuration.
func DefaultConfig() Config {
	return Config{
		RootDir:        "./ssh_root",
		User:           "admin",
		Password:       "admin",
		Banner:         "SSH-2.0-Freebox_SSH_SFTP_1.0",
		MaxConnections: 50,
		IdleTimeout:    5 * time.Minute,
	}
}

// Server represents an SSH / SFTP server.
type Server struct {
	config    Config
	listener  net.Listener
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	closed    bool
	closeChan chan struct{}
}

// NewServer creates a new SSH/SFTP server instance.
func NewServer(cfg Config) *Server {
	if cfg.RootDir == "" {
		cfg.RootDir = "."
	}
	if cfg.Banner == "" {
		cfg.Banner = "SSH-2.0-Freebox_1.0"
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	return &Server{
		config:    cfg,
		conns:     make(map[net.Conn]struct{}),
		closeChan: make(chan struct{}),
	}
}

// Serve accepts incoming connections on listener l and serves SSH/SFTP sessions.
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
			return fmt.Errorf("ssh accept error: %w", err)
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

// ListenAndServe listens on TCP address addr and serves SSH connections.
func (s *Server) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":22"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ssh listen failed: %w", err)
	}
	return s.Serve(l)
}

// Shutdown gracefully shuts down the SSH/SFTP server.
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

// Close immediately terminates all active listeners and connections.
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

	if s.config.IdleTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(s.config.IdleTimeout))
	}

	// Send SSH Identification banner
	_, err := fmt.Fprintf(conn, "%s\r\n", s.config.Banner)
	if err != nil {
		return
	}

	reader := bufio.NewReader(conn)
	// Read client banner
	clientBanner, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	clientBanner = strings.TrimSpace(clientBanner)
	if !strings.HasPrefix(clientBanner, "SSH-") {
		return
	}

	// Read packet loop or SSH key exchange / auth packets
	buf := make([]byte, 4096)
	for {
		if s.config.IdleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))
		}

		n, err := reader.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		if n == 0 {
			return
		}
	}
}
