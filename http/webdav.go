package http

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WebDAVConfig defines the configuration for a WebDAV server.
type WebDAVConfig struct {
	Prefix       string
	RootDir      string
	AuthUser     string
	AuthPassword string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultWebDAVConfig returns default WebDAV server configuration.
func DefaultWebDAVConfig() WebDAVConfig {
	return WebDAVConfig{
		Prefix:       "/webdav",
		RootDir:      "./webdav_root",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}
}

// WebDAVServer represents a WebDAV file server.
type WebDAVServer struct {
	config     WebDAVConfig
	httpServer *http.Server
	listener   net.Listener
	mu         sync.Mutex
	running    bool
}

// NewWebDAVServer creates a new WebDAV server instance.
func NewWebDAVServer(cfg WebDAVConfig) *WebDAVServer {
	if cfg.RootDir == "" {
		cfg.RootDir = "."
	}
	s := &WebDAVServer{
		config: cfg,
	}

	mux := http.NewServeMux()
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	mux.HandleFunc(prefix, s.handleWebDAV)
	if prefix != "/" {
		mux.HandleFunc(strings.TrimSuffix(prefix, "/"), s.handleWebDAV)
	}

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	return s
}

// Serve accepts incoming connections on listener l and serves WebDAV requests.
func (s *WebDAVServer) Serve(l net.Listener) error {
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

// ListenAndServe listens on TCP address addr and serves WebDAV.
func (s *WebDAVServer) ListenAndServe(addr string) error {
	if addr == "" {
		addr = ":8080"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("webdav listen failed: %w", err)
	}
	return s.Serve(l)
}

// Shutdown gracefully shuts down the WebDAV server.
func (s *WebDAVServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// Close immediately closes all active listeners and connections.
func (s *WebDAVServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}

func (s *WebDAVServer) checkAuth(r *http.Request) bool {
	if s.config.AuthUser == "" && s.config.AuthPassword == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return user == s.config.AuthUser && pass == s.config.AuthPassword
}

func (s *WebDAVServer) handleWebDAV(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Freebox WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("DAV", "1, 2")
	w.Header().Set("MS-Author-Via", "DAV")

	relPath := strings.TrimPrefix(r.URL.Path, s.config.Prefix)
	relPath = strings.TrimPrefix(relPath, "/")
	localPath := filepath.Join(s.config.RootDir, filepath.Clean("/"+relPath))

	switch r.Method {
	case "OPTIONS":
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, POST, PUT, DELETE, TRACE, PROPFIND, PROPPATCH, MKCOL, COPY, MOVE, LOCK, UNLOCK")
		w.WriteHeader(http.StatusOK)

	case "PROPFIND":
		s.handlePropfind(w, r, localPath, r.URL.Path)

	case "GET", "HEAD":
		http.ServeFile(w, r, localPath)

	case "PUT":
		s.handlePut(w, r, localPath)

	case "DELETE":
		s.handleDelete(w, r, localPath)

	case "MKCOL":
		s.handleMkcol(w, r, localPath)

	case "MOVE":
		s.handleMove(w, r, localPath)

	case "COPY":
		s.handleCopy(w, r, localPath)

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *WebDAVServer) handlePut(w http.ResponseWriter, r *http.Request, path string) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	f, err := os.Create(path)
	if err != nil {
		http.Error(w, "Cannot create file", http.StatusForbidden)
		return
	}
	defer f.Close()

	if _, err := io.Copy(f, r.Body); err != nil {
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *WebDAVServer) handleDelete(w http.ResponseWriter, r *http.Request, path string) {
	if err := os.RemoveAll(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to delete", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *WebDAVServer) handleMkcol(w http.ResponseWriter, r *http.Request, path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		http.Error(w, "Cannot create collection", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *WebDAVServer) handleMove(w http.ResponseWriter, r *http.Request, srcPath string) {
	destHeader := r.Header.Get("Destination")
	if destHeader == "" {
		http.Error(w, "Bad Destination", http.StatusBadRequest)
		return
	}
	destRel := strings.TrimPrefix(destHeader, s.config.Prefix)
	destPath := filepath.Join(s.config.RootDir, filepath.Clean("/"+destRel))

	if err := os.Rename(srcPath, destPath); err != nil {
		http.Error(w, "Cannot move resource", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *WebDAVServer) handleCopy(w http.ResponseWriter, r *http.Request, srcPath string) {
	destHeader := r.Header.Get("Destination")
	if destHeader == "" {
		http.Error(w, "Bad Destination", http.StatusBadRequest)
		return
	}
	destRel := strings.TrimPrefix(destHeader, s.config.Prefix)
	destPath := filepath.Join(s.config.RootDir, filepath.Clean("/"+destRel))

	srcFile, err := os.Open(srcPath)
	if err != nil {
		http.Error(w, "Source not found", http.StatusNotFound)
		return
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		http.Error(w, "Cannot create directory", http.StatusInternalServerError)
		return
	}

	destFile, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "Cannot create destination file", http.StatusConflict)
		return
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		http.Error(w, "Copy failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

type propfindResponse struct {
	XMLName  xml.Name       `xml:"multistatus"`
	Xmlns    string         `xml:"xmlns,attr,omitempty"`
	Response []responseItem `xml:"response"`
}

type responseItem struct {
	XMLName  xml.Name `xml:"response"`
	Href     string   `xml:"href"`
	Propstat propstat `xml:"propstat"`
}

type propstat struct {
	XMLName xml.Name `xml:"propstat"`
	Prop    prop     `xml:"prop"`
	Status  string   `xml:"status"`
}

type prop struct {
	XMLName          xml.Name      `xml:"prop"`
	ResourceType     *resourceType `xml:"resourcetype,omitempty"`
	GetContentLength int64         `xml:"getcontentlength,omitempty"`
	GetLastModified  string        `xml:"getlastmodified,omitempty"`
	DisplayName      string        `xml:"displayname,omitempty"`
}

type resourceType struct {
	XMLName    xml.Name  `xml:"resourcetype"`
	Collection *struct{} `xml:"collection,omitempty"`
}

func (s *WebDAVServer) handlePropfind(w http.ResponseWriter, r *http.Request, path string, reqHref string) {
	fi, err := os.Stat(path)
	if err != nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	var items []responseItem
	item := responseItem{
		Href: reqHref,
		Propstat: propstat{
			Status: "HTTP/1.1 200 OK",
			Prop: prop{
				DisplayName:     fi.Name(),
				GetLastModified: fi.ModTime().UTC().Format(http.TimeFormat),
			},
		},
	}
	if fi.IsDir() {
		item.Propstat.Prop.ResourceType = &resourceType{Collection: &struct{}{}}
	} else {
		item.Propstat.Prop.GetContentLength = fi.Size()
	}
	items = append(items, item)

	depth := r.Header.Get("Depth")
	if fi.IsDir() && depth != "0" {
		entries, _ := os.ReadDir(path)
		for _, entry := range entries {
			eInfo, err := entry.Info()
			if err != nil {
				continue
			}
			childHref := strings.TrimSuffix(reqHref, "/") + "/" + entry.Name()
			cItem := responseItem{
				Href: childHref,
				Propstat: propstat{
					Status: "HTTP/1.1 200 OK",
					Prop: prop{
						DisplayName:     entry.Name(),
						GetLastModified: eInfo.ModTime().UTC().Format(http.TimeFormat),
					},
				},
			}
			if entry.IsDir() {
				cItem.Propstat.Prop.ResourceType = &resourceType{Collection: &struct{}{}}
			} else {
				cItem.Propstat.Prop.GetContentLength = eInfo.Size()
			}
			items = append(items, cItem)
		}
	}

	res := propfindResponse{
		Xmlns:    "DAV:",
		Response: items,
	}

	w.Header().Set("Content-Type", "application/xml; charset=\"utf-8\"")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	_ = enc.Encode(res)
}
