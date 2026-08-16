package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Config defines the configuration for the HTTP server.
type Config struct {
	RootDir        string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxHeaderBytes int
	EnableCORS     bool
}

// DefaultConfig returns default HTTP server configuration.
func DefaultConfig() Config {
	return Config{
		RootDir:      "./public",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		EnableCORS:   true,
	}
}

// Server represents an HTTP server.
type Server struct {
	config      Config
	mux         *http.ServeMux
	middlewares []func(http.Handler) http.Handler
	httpServer  *http.Server
	listener    net.Listener
	mu          sync.Mutex
	running     bool
}

// NewServer creates a new HTTP server.
func NewServer(cfg Config) *Server {
	mux := http.NewServeMux()
	s := &Server{
		config: cfg,
		mux:    mux,
	}

	if cfg.RootDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(cfg.RootDir)))
	}

	return s
}

// Use adds middleware to the HTTP request pipeline.
func (s *Server) Use(mw func(http.Handler) http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.middlewares = append(s.middlewares, mw)
}

// Handle registers the handler for the given pattern.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// HandleFunc registers the handler function for the given pattern.
func (s *Server) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, handler)
}

func (s *Server) buildHandler() http.Handler {
	var handler http.Handler = s.mux

	if s.config.EnableCORS {
		handler = s.corsMiddleware(handler)
	}

	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}

	return handler
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve accepts incoming connections on the listener l.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.running = true
	s.httpServer = &http.Server{
		Handler:        s.buildHandler(),
		ReadTimeout:    s.config.ReadTimeout,
		WriteTimeout:   s.config.WriteTimeout,
		IdleTimeout:    s.config.IdleTimeout,
		MaxHeaderBytes: s.config.MaxHeaderBytes,
	}
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

// ListenAndServe listens on the TCP network address addr and then calls Serve.
func (s *Server) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":80"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http server listen failed: %w", err)
	}
	return s.Serve(l)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// Close immediately closes all active listeners and connections.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}
