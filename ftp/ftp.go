package ftp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config holds the configuration settings for the FTP server.
type Config struct {
	RootDir        string
	User           string
	Password       string
	MaxConnections int
	IdleTimeout    time.Duration
}

// DefaultConfig returns the default FTP server configuration.
func DefaultConfig() Config {
	return Config{
		RootDir:        "./ftp_root",
		User:           "anonymous",
		Password:       "anonymous",
		MaxConnections: 50,
		IdleTimeout:    5 * time.Minute,
	}
}

// Server represents an FTP server.
type Server struct {
	config    Config
	listener  net.Listener
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	closed    bool
	closeChan chan struct{}
}

// NewServer creates a new FTP server instance.
func NewServer(cfg Config) *Server {
	if cfg.RootDir == "" {
		cfg.RootDir = "."
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

// Serve accepts incoming connections on listener l and serves FTP sessions.
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
			return fmt.Errorf("ftp accept error: %w", err)
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

// ListenAndServe listens on the TCP network address addr and then calls Serve.
func (s *Server) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":21"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ftp listen failed: %w", err)
	}
	return s.Serve(l)
}

// Shutdown gracefully stops the FTP server, closing listeners and active connections.
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

// Close immediately terminates all listeners and connections.
func (s *Server) Close() error {
	return s.Shutdown(context.Background())
}

type session struct {
	server       *Server
	conn         net.Conn
	reader       *bufio.Reader
	user         string
	authenticated bool
	workDir      string
	dataListener net.Listener
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	sess := &session{
		server:  s,
		conn:    conn,
		reader:  bufio.NewReader(conn),
		workDir: "/",
	}

	if err := sess.writeLine("220 Freebox FTP Service Ready"); err != nil {
		return
	}

	for {
		if s.config.IdleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))
		}

		line, err := sess.reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		if err := sess.executeCommand(line); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
		}
	}
}

func (sess *session) writeLine(line string) error {
	_, err := fmt.Fprintf(sess.conn, "%s\r\n", line)
	return err
}

func (sess *session) executeCommand(line string) error {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch cmd {
	case "USER":
		sess.user = arg
		if sess.server.config.User == "" || sess.server.config.User == "anonymous" {
			sess.authenticated = true
			return sess.writeLine("230 User logged in, proceed.")
		}
		return sess.writeLine("331 User name okay, need password.")

	case "PASS":
		if sess.user == sess.server.config.User && (sess.server.config.Password == "" || arg == sess.server.config.Password) {
			sess.authenticated = true
			return sess.writeLine("230 User logged in, proceed.")
		}
		return sess.writeLine("530 Not logged in.")

	case "SYST":
		return sess.writeLine("215 UNIX Type: L8")

	case "FEAT":
		_ = sess.writeLine("211-Features:")
		_ = sess.writeLine(" UTF8")
		_ = sess.writeLine(" PASV")
		_ = sess.writeLine(" EPSV")
		return sess.writeLine("211 End")

	case "PWD", "XPWD":
		return sess.writeLine(fmt.Sprintf(`257 "%s" is the current directory`, sess.workDir))

	case "TYPE":
		return sess.writeLine("200 Type set to " + arg)

	case "NOOP":
		return sess.writeLine("200 OK")

	case "PASV":
		return sess.handlePASV()

	case "EPSV":
		return sess.handleEPSV()

	case "CWD", "XCWD":
		sess.workDir = filepath.Clean(filepath.Join(sess.workDir, arg))
		return sess.writeLine(fmt.Sprintf("250 Directory successfully changed to %s", sess.workDir))

	case "CDUP":
		sess.workDir = filepath.Clean(filepath.Join(sess.workDir, ".."))
		return sess.writeLine("200 Directory changed to parent.")

	case "LIST", "NLST":
		return sess.handleList()

	case "QUIT":
		_ = sess.writeLine("221 Goodbye.")
		return io.EOF

	default:
		return sess.writeLine("502 Command not implemented.")
	}
}

func (sess *session) handlePASV() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return sess.writeLine("425 Can't open passive connection.")
	}
	sess.dataListener = l

	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		return sess.writeLine("425 Can't determine passive address.")
	}

	ip := tcpAddr.IP.To4()
	if ip == nil {
		ip = net.ParseIP("127.0.0.1").To4()
	}
	port := tcpAddr.Port
	p1 := port / 256
	p2 := port % 256

	msg := fmt.Sprintf("227 Entering Passive Mode (%d,%d,%d,%d,%d,%d)",
		ip[0], ip[1], ip[2], ip[3], p1, p2)
	return sess.writeLine(msg)
}

func (sess *session) handleEPSV() error {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return sess.writeLine("425 Can't open extended passive connection.")
	}
	sess.dataListener = l

	tcpAddr := l.Addr().(*net.TCPAddr)
	msg := fmt.Sprintf("229 Entering Extended Passive Mode (|||%d|)", tcpAddr.Port)
	return sess.writeLine(msg)
}

func (sess *session) handleList() error {
	if sess.dataListener == nil {
		return sess.writeLine("425 Use PASV or EPSV first.")
	}
	defer func() {
		_ = sess.dataListener.Close()
		sess.dataListener = nil
	}()

	_ = sess.writeLine("150 Here comes the directory listing.")
	dataConn, err := sess.dataListener.Accept()
	if err != nil {
		return sess.writeLine("425 Transfer failed.")
	}
	defer dataConn.Close()

	targetDir := filepath.Join(sess.server.config.RootDir, sess.workDir)
	entries, _ := os.ReadDir(targetDir)

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mode := "-rw-r--r--"
		if entry.IsDir() {
			mode = "drwxr-xr-x"
		}
		line := fmt.Sprintf("%s 1 owner group %10d %s %s\r\n",
			mode,
			info.Size(),
			info.ModTime().Format("Jan 02 15:04"),
			entry.Name(),
		)
		_, _ = io.WriteString(dataConn, line)
	}

	return sess.writeLine("226 Directory send OK.")
}