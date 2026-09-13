package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"freebox/api/ws"
	"freebox/auth"
	"freebox/cert"
	"freebox/engine"
	"freebox/meta"
	"freebox/proxy"
	"freebox/remotes"
	"freebox/storage"
	"freebox/thumbnail"
	"freebox/vfs"
	"freebox/web"
)

// ServerConfig configures the API Server.
type ServerConfig struct {
	Addr       string
	AuthMgr    *auth.Manager
	StorageDB  *storage.DB
	Mounts     *vfs.Registry
	Engine     *engine.Engine
	StreamServ *proxy.StreamServer
	MetaMgr    *meta.Manager
	RemotesMgr *remotes.Manager
	ThumbMgr    *thumbnail.Engine
	CertMgr     *cert.Manager
}

// Server handles all REST API and WebSocket communication for Freebox.
type Server struct {
	addr        string
	authMgr     *auth.Manager
	storageDB   *storage.DB
	mounts      *vfs.Registry
	engine      *engine.Engine
	streamServ  *proxy.StreamServer
	metaMgr     *meta.Manager
	remotesMgr  *remotes.Manager
	thumbMgr    *thumbnail.Engine
	certMgr     *cert.Manager
	wsHub       *ws.Hub
	httpServer  *http.Server
}

// NewServer initializes a new API Server instance.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		addr:        cfg.Addr,
		authMgr:     cfg.AuthMgr,
		storageDB:   cfg.StorageDB,
		mounts:      cfg.Mounts,
		engine:      cfg.Engine,
		streamServ:  cfg.StreamServ,
		metaMgr:     cfg.MetaMgr,
		remotesMgr:  cfg.RemotesMgr,
		thumbMgr:    cfg.ThumbMgr,
		certMgr:     cfg.CertMgr,
	}

	if s.mounts == nil {
		s.mounts = vfs.NewRegistry(cfg.StorageDB)
	}
	if s.streamServ == nil && s.engine != nil {
		s.streamServ = s.engine.StreamServer()
	}
	if s.metaMgr == nil {
		s.metaMgr = meta.NewManager(cfg.StorageDB)
	}
	if s.remotesMgr == nil {
		s.remotesMgr = remotes.NewManager(cfg.StorageDB, s.mounts)
	}
	if s.thumbMgr == nil {
		s.thumbMgr = thumbnail.NewEngine(thumbnail.Config{MaxMemoryMB: 64})
	}
	if s.certMgr == nil {
		s.certMgr = cert.NewManager(cfg.StorageDB)
	}

	// Initialize WebSocket Hub
	s.wsHub = ws.NewHub(s.handleWSAction)

	return s
}

// Handler returns the configured http.Handler with all routes and middlewares.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public Auth Endpoints
	mux.HandleFunc("/api/auth/login", s.handleLogin)

	// Stream Proxy Direct Playback Endpoint (Public or Token auth)
	mux.HandleFunc("/stream/", s.handleStreamPlayback)

	// WebSocket Endpoint
	mux.HandleFunc("/api/ws", s.handleWebSocket)

	// Protected Endpoints
	mux.HandleFunc("/api/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("/api/auth/me", s.requireAuth(s.handleAuthMe))

	// Tasks & History
	mux.HandleFunc("/api/tasks", s.requireAuth(s.handleTasks))
	mux.HandleFunc("/api/tasks/history", s.requireAuth(s.handleTaskHistory))
	mux.HandleFunc("/api/tasks/", s.requireAuth(s.handleTaskByID))

	// Mounts & Remotes
	mux.HandleFunc("/api/mounts", s.requireAuth(s.handleMounts))
	mux.HandleFunc("/api/mounts/", s.requireAuth(s.handleMountByName))
	mux.HandleFunc("/api/remotes", s.requireAuth(s.handleRemotes))
	mux.HandleFunc("/api/remotes/test", s.requireAuth(s.handleRemoteTest))
	mux.HandleFunc("/api/remotes/", s.requireAuth(s.handleRemoteByName))

	// Search & Replace
	mux.HandleFunc("/api/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("/api/search/replace", s.requireAuth(s.handleSearchReplace))

	// Deduplication
	mux.HandleFunc("/api/dedup", s.requireAuth(s.handleDedup))

	// Metadata & Permissions
	mux.HandleFunc("/api/meta", s.requireAuth(s.handleMeta))

	// Media Thumbnails
	mux.HandleFunc("/api/thumbnail", s.requireAuth(s.handleThumbnail))

	// Archive Operations
	mux.HandleFunc("/api/archive/preview", s.requireAuth(s.handleArchivePreview))
	mux.HandleFunc("/api/archive/create", s.requireAuth(s.handleArchiveCreate))
	mux.HandleFunc("/api/archive/extract", s.requireAuth(s.handleArchiveExtract))

	// Certificates
	mux.HandleFunc("/api/cert/generate", s.requireAuth(s.handleCertGenerate))
	mux.HandleFunc("/api/cert/list", s.requireAuth(s.handleCertList))

	// Signer
	mux.HandleFunc("/api/sign", s.requireAuth(s.handleSign))
	mux.HandleFunc("/api/sign/verify", s.requireAuth(s.handleSignVerify))

	// VFS Operations
	mux.HandleFunc("/api/vfs/transfer", s.requireAuth(s.handleVFSTransfer))
	mux.HandleFunc("/api/vfs/", s.requireAuth(s.handleVFSOperation))

	// Stream Proxy Management
	mux.HandleFunc("/api/streams", s.requireAuth(s.handleStreams))
	mux.HandleFunc("/api/streams/", s.requireAuth(s.handleStreamByName))

	// Clipboard
	mux.HandleFunc("/api/clipboard", s.requireAuth(s.handleClipboard))
	mux.HandleFunc("/api/clipboard/paste", s.requireAuth(s.handleClipboardPaste))

	// Servers status
	mux.HandleFunc("/api/servers", s.requireAuth(s.handleServers))

	// Embedded Web Explorer UI
	webHandler := web.Handler()
	mux.Handle("/ui", webHandler)
	mux.Handle("/ui/", webHandler)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || !strings.HasPrefix(r.URL.Path, "/api/") {
			webHandler.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}))

	return s.corsMiddleware(mux)
}

// Start begins listening on the configured address.
func (s *Server) Start() error {
	if s.addr == "" {
		s.addr = ":8080"
	}

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      s.Handler(),
		ReadTimeout:  30 * time.Minute, // Allow long streams / transfers
		WriteTimeout: 30 * time.Minute,
	}

	return s.httpServer.ListenAndServe()
}

// Close stops the HTTP server and WS Hub.
func (s *Server) Close() error {
	s.wsHub.Close()
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// AttachTaskCallbacks hooks progress and status changes to the WebSocket Hub
func (s *Server) AttachTaskCallbacks(h *engine.TaskHandle) {
	if h == nil {
		return
	}
	taskID := h.ID()
	h.OnProgress(func(p engine.TaskProgress) {
		s.BroadcastTaskProgress(taskID, p, h.Status())
	})
	h.OnStatus(func(st engine.TaskStatus) {
		s.BroadcastTaskProgress(taskID, h.Progress(), st)
		s.wsHub.Broadcast("tasks", "task_status_changed", map[string]interface{}{
			"task_id": taskID,
			"status":  st,
		})
	})
}

// WS Broadcast helper
func (s *Server) BroadcastTaskProgress(taskID string, progress engine.TaskProgress, status engine.TaskStatus) {
	data := map[string]interface{}{
		"task_id":         taskID,
		"status":          status,
		"bytes_processed": progress.BytesProcessed,
		"total_bytes":     progress.TotalBytes,
		"percent":         progress.Percent,
		"speed_bytes_sec": progress.SpeedBytesSec,
		"current_item":    progress.CurrentItem,
		"items_processed": progress.ItemsProcessed,
		"total_items":     progress.TotalItems,
	}
	s.wsHub.Broadcast("tasks", "task_progress", data)
	s.wsHub.Broadcast(fmt.Sprintf("tasks:%s", taskID), "task_progress", data)
}

// Context keys and helpers
type contextKey string

const userContextKey = contextKey("user_session")

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authMgr == nil {
			next(w, r)
			return
		}

		token := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		} else if qToken := r.URL.Query().Get("token"); qToken != "" {
			token = qToken
		}

		session, err := s.authMgr.ValidateToken(token)
		if err != nil {
			s.jsonError(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, session)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) jsonError(w http.ResponseWriter, message string, status int) {
	s.jsonResponse(w, status, map[string]interface{}{
		"error":  message,
		"status": status,
	})
}

func (s *Server) handleWSAction(client *ws.Client, event ws.Event) error {
	dataMap, ok := event.Data.(map[string]interface{})
	if !ok {
		return nil
	}

	action, _ := dataMap["action"].(string)
	taskID, _ := dataMap["task_id"].(string)

	if s.engine == nil || taskID == "" {
		return nil
	}

	switch action {
	case "pause":
		_ = s.engine.PauseTask(taskID)
	case "resume":
		_ = s.engine.ResumeTask(taskID)
	case "cancel":
		_ = s.engine.CancelTask(taskID)
	}
	return nil
}

// Hub returns the WebSocket hub.
func (s *Server) Hub() *ws.Hub {
	return s.wsHub
}

// Mounts returns the mount registry.
func (s *Server) Mounts() *vfs.Registry {
	return s.mounts
}

// Engine returns the task engine.
func (s *Server) Engine() *engine.Engine {
	return s.engine
}
