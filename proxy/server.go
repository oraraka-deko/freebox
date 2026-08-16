package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StreamServer serves random-access StreamProxy files via HTTP with full Range seeking support.
type StreamServer struct {
	mu         sync.RWMutex
	sources    map[string]Source
	httpServer *http.Server
	listener   net.Listener
	running    bool
}

// NewStreamServer creates a new HTTP stream proxy server.
func NewStreamServer() *StreamServer {
	s := &StreamServer{
		sources: make(map[string]Source),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stream/", s.handleStream)
	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	return s
}

// RegisterSource registers a named Source to be served at /stream/<name>.
func (s *StreamServer) RegisterSource(name string, src Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[name] = src
}

// UnregisterSource removes a registered Source.
func (s *StreamServer) UnregisterSource(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sources, name)
}

// ListSources returns all registered source names.
func (s *StreamServer) ListSources() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, 0, len(s.sources))
	for name := range s.sources {
		res = append(res, name)
	}
	return res
}

// ServeHTTP satisfies the http.Handler interface for direct streaming.
func (s *StreamServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleStream(w, r)
}

func (s *StreamServer) handleStream(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/stream/")
	s.mu.RLock()
	src, ok := s.sources[name]
	s.mu.RUnlock()

	if !ok {
		http.Error(w, "Stream not found", http.StatusNotFound)
		return
	}

	proxyStream := NewStreamProxy(src, DefaultStreamProxyConfig())
	totalSize := proxyStream.Size()

	w.Header().Set("Accept-Ranges", "bytes")
	contentType := "application/octet-stream"
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp4", ".m4v":
		contentType = "video/mp4"
	case ".mkv":
		contentType = "video/x-matroska"
	case ".webm":
		contentType = "video/webm"
	case ".mp3":
		contentType = "audio/mpeg"
	case ".flac":
		contentType = "audio/flac"
	case ".wav":
		contentType = "audio/wav"
	case ".avi":
		contentType = "video/x-msvideo"
	default:
		if mt := mime.TypeByExtension(ext); mt != "" {
			contentType = mt
		}
	}
	w.Header().Set("Content-Type", contentType)

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		// Full content
		w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, proxyStream)
		}
		return
	}

	// Parse Range: bytes=start-end
	start, end, err := parseRangeHeader(rangeHeader, totalSize)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalSize))
		http.Error(w, "Requested Range Not Satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	contentLength := end - start + 1
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
	w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	w.WriteHeader(http.StatusPartialContent)

	if r.Method == http.MethodHead {
		return
	}

	if _, err := proxyStream.Seek(start, io.SeekStart); err != nil {
		return
	}

	_, _ = io.CopyN(w, proxyStream, contentLength)
}

func parseRangeHeader(h string, totalSize int64) (start, end int64, err error) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, errors.New("invalid range unit")
	}

	spec := strings.TrimPrefix(h, "bytes=")
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid range spec")
	}

	if parts[0] == "" {
		// Suffix range: bytes=-500 (last 500 bytes)
		length, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || length <= 0 {
			return 0, 0, errors.New("invalid suffix length")
		}
		start = totalSize - length
		if start < 0 {
			start = 0
		}
		end = totalSize - 1
		return start, end, nil
	}

	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= totalSize {
		return 0, 0, errors.New("invalid start offset")
	}

	if parts[1] == "" {
		// Open ended range: bytes=500-
		end = totalSize - 1
	} else {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, errors.New("invalid end offset")
		}
		if end >= totalSize {
			end = totalSize - 1
		}
	}

	return start, end, nil
}

// Serve accepts incoming connections on listener l.
func (s *StreamServer) Serve(l net.Listener) error {
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

// ListenAndServe listens on addr and serves streams.
func (s *StreamServer) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":8088"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("stream server listen error: %w", err)
	}
	return s.Serve(l)
}

// Shutdown stops the stream server.
func (s *StreamServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// Close immediately closes the stream server.
func (s *StreamServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}
