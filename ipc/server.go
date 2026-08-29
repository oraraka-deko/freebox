package ipc

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
	"os"
	"sync"
)

// Conn represents one persistent JSON-RPC control connection.
type Conn struct {
	nc      net.Conn
	writeMu sync.Mutex
}

// WriteResponse sends a JSON-RPC response frame, serialized against concurrent writers.
func (c *Conn) WriteResponse(resp Response) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.nc, resp)
}

// Notify sends a server-initiated notification (no id) on this connection.
func (c *Conn) Notify(method string, params any) error {
	return c.WriteResponse(Response{JSONRPC: "2.0", Method: method, Result: params})
}

// Server listens for IPC connections on a Unix domain socket and/or a TCP
// loopback address, dispatching control-plane RPCs and fast file transfers.
type Server struct {
	Inst *Instance

	SocketPath string // Unix domain socket path, e.g. "/run/freebox/freebox.sock"
	TCPAddr    string // optional TCP loopback fallback, e.g. "127.0.0.1:9191"

	listeners []net.Listener
	wg        sync.WaitGroup
}

// NewServer creates a Server bound to the given instance.
func NewServer(inst *Instance, socketPath, tcpAddr string) *Server {
	return &Server{
		Inst:       inst,
		SocketPath: socketPath,
		TCPAddr:    tcpAddr,
	}
}

// Start binds the configured listeners and begins accepting connections.
// It returns once listeners are bound; Accept loops run in background goroutines.
func (s *Server) Start() error {
	if s.SocketPath != "" {
		_ = os.Remove(s.SocketPath)
		l, err := net.Listen("unix", s.SocketPath)
		if err != nil {
			return err
		}
		if err := os.Chmod(s.SocketPath, 0o600); err != nil {
			l.Close()
			return err
		}
		s.listeners = append(s.listeners, l)
	}
	if s.TCPAddr != "" {
		l, err := net.Listen("tcp", s.TCPAddr)
		if err != nil {
			s.Close()
			return err
		}
		s.listeners = append(s.listeners, l)
	}
	if len(s.listeners) == 0 {
		return errors.New("ipc: no listener configured (set SocketPath and/or TCPAddr)")
	}

	for _, l := range s.listeners {
		l := l
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.acceptLoop(l)
		}()
	}
	return nil
}

// Close stops all listeners. In-flight connections are not forcibly closed.
func (s *Server) Close() error {
	for _, l := range s.listeners {
		_ = l.Close()
	}
	if s.SocketPath != "" {
		_ = os.Remove(s.SocketPath)
	}
	return nil
}

// Wait blocks until all accept loops have exited (i.e. after Close).
func (s *Server) Wait() {
	s.wg.Wait()
}

func (s *Server) acceptLoop(l net.Listener) {
	for {
		nc, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("ipc: accept error: %v", err)
			return
		}
		go s.handleConn(nc)
	}
}

func (s *Server) handleConn(nc net.Conn) {
	magic, err := ReadMagic(nc)
	if err != nil {
		nc.Close()
		return
	}
	switch magic {
	case MagicControl:
		s.handleControlConn(nc)
	case MagicTransfer:
		s.handleTransferConn(nc)
	default:
		nc.Close()
	}
}

func (s *Server) handleControlConn(nc net.Conn) {
	defer nc.Close()
	conn := &Conn{nc: nc}
	r := bufio.NewReader(nc)

	for {
		var req Request
		if err := ReadFrame(r, &req); err != nil {
			return
		}
		go s.handleRequest(conn, req)
	}
}

func (s *Server) handleRequest(conn *Conn, req Request) {
	result, err := func() (result any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				err = errors.New("internal error handling request")
			}
		}()
		return dispatch(s.Inst, conn, req.Method, req.Params)
	}()

	if req.ID == nil {
		return // pure notification from client, no response expected
	}

	resp := Response{JSONRPC: "2.0", ID: req.ID}
	if err != nil {
		resp.Error = &RPCError{Code: -32000, Message: err.Error()}
	} else {
		resp.Result = result
	}
	if werr := conn.WriteResponse(resp); werr != nil {
		log.Printf("ipc: write response failed: %v", werr)
	}
}

func (s *Server) handleTransferConn(nc net.Conn) {
	defer nc.Close()
	token := make([]byte, TransferTokenSize)
	if _, err := readFull(nc, token); err != nil {
		return
	}
	s.Inst.Transfers.serve(context.Background(), nc, token)
}

func readFull(nc net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := nc.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
