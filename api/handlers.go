package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"freebox/auth"
	"freebox/clipboard"
	"freebox/engine"
	"freebox/proxy"
	"freebox/vfs"
)

// --- Auth Handlers ---

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if s.authMgr == nil {
		s.jsonResponse(w, http.StatusOK, map[string]string{
			"token":    "no-auth-mode",
			"username": req.Username,
			"role":     "admin",
		})
		return
	}

	session, err := s.authMgr.Authenticate(req.Username, req.Password)
	if err != nil {
		s.jsonError(w, "Authentication failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	s.jsonResponse(w, http.StatusOK, session)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, _ := r.Context().Value(userContextKey).(*auth.Session)
	if session != nil && s.authMgr != nil {
		_ = s.authMgr.RevokeToken(session.Token)
	}

	s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Logged out successfully"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	session, _ := r.Context().Value(userContextKey).(*auth.Session)
	s.jsonResponse(w, http.StatusOK, session)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	username := "anonymous"
	if s.authMgr != nil {
		token := r.URL.Query().Get("token")
		if token == "" {
			authHeader := r.Header.Get("Authorization")
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
		session, err := s.authMgr.ValidateToken(token)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		username = session.Username
	}

	s.wsHub.ServeHTTP(w, r, username)
}

func (s *Server) handleStreamPlayback(w http.ResponseWriter, r *http.Request) {
	if s.streamServ == nil {
		http.Error(w, "Stream proxy unavailable", http.StatusServiceUnavailable)
		return
	}
	s.streamServ.ServeHTTP(w, r)
}

// --- Task Handlers ---

type TaskResponse struct {
	ID          string              `json:"id"`
	Type        engine.TaskType     `json:"type"`
	Description string              `json:"description"`
	Status      engine.TaskStatus   `json:"status"`
	Progress    engine.TaskProgress `json:"progress"`
	Metadata    map[string]string   `json:"metadata,omitempty"`
}

func formatTaskResponse(h *engine.TaskHandle) TaskResponse {
	return TaskResponse{
		ID:          h.Task().ID,
		Type:        h.Task().Type,
		Description: h.Task().Description,
		Status:      h.Status(),
		Progress:    h.Progress(),
		Metadata:    h.Task().Metadata,
	}
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		s.jsonError(w, "Engine not initialized", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		tasks := s.engine.ListTasks()
		resp := make([]TaskResponse, 0, len(tasks))
		for _, t := range tasks {
			resp = append(resp, formatTaskResponse(t))
		}
		s.jsonResponse(w, http.StatusOK, resp)

	case http.MethodPost:
		var taskReq struct {
			Type        engine.TaskType   `json:"type"`
			Description string            `json:"description"`
			Priority    int               `json:"priority"`
			Metadata    map[string]string `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&taskReq); err != nil {
			s.jsonError(w, "Invalid task request: "+err.Error(), http.StatusBadRequest)
			return
		}

		task := &engine.Task{
			Type:        taskReq.Type,
			Description: taskReq.Description,
			Priority:    taskReq.Priority,
			Metadata:    taskReq.Metadata,
		}

		handle, err := s.engine.Submit(task)
		if err != nil {
			s.jsonError(w, "Failed submitting task: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.AttachTaskCallbacks(handle)

		s.jsonResponse(w, http.StatusAccepted, formatTaskResponse(handle))

	default:
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		s.jsonError(w, "Engine not initialized", http.StatusInternalServerError)
		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(trimmed, "/")
	taskID := parts[0]

	handle, found := s.engine.GetTask(taskID)
	if !found {
		s.jsonError(w, "Task not found", http.StatusNotFound)
		return
	}

	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			s.jsonResponse(w, http.StatusOK, formatTaskResponse(handle))
			return
		}
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := parts[1]
	if r.Method != http.MethodPost {
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var err error
	switch action {
	case "pause":
		err = s.engine.PauseTask(taskID)
	case "resume":
		err = s.engine.ResumeTask(taskID)
	case "cancel":
		err = s.engine.CancelTask(taskID)
	default:
		s.jsonError(w, "Unknown task action: "+action, http.StatusBadRequest)
		return
	}

	if err != nil {
		s.jsonError(w, "Action failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.jsonResponse(w, http.StatusOK, formatTaskResponse(handle))
}

// --- Mount Handlers ---

type MountRequest struct {
	Name    string          `json:"name"`
	Config  vfs.MountConfig `json:"config"`
	Persist bool            `json:"persist"`
}

func (s *Server) handleMounts(w http.ResponseWriter, r *http.Request) {
	if s.mounts == nil {
		s.jsonError(w, "Mount registry not available", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.jsonResponse(w, http.StatusOK, s.mounts.List())

	case http.MethodPost:
		var req MountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.jsonError(w, "Invalid mount body: "+err.Error(), http.StatusBadRequest)
			return
		}

		if err := s.mounts.Mount(req.Name, req.Config, req.Persist); err != nil {
			s.jsonError(w, "Failed to mount: "+err.Error(), http.StatusBadRequest)
			return
		}

		s.jsonResponse(w, http.StatusCreated, map[string]string{
			"message": fmt.Sprintf("Mount %s created successfully", req.Name),
			"name":    req.Name,
		})

	default:
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMountByName(w http.ResponseWriter, r *http.Request) {
	if s.mounts == nil {
		s.jsonError(w, "Mount registry not available", http.StatusInternalServerError)
		return
	}

	mountName := strings.TrimPrefix(r.URL.Path, "/api/mounts/")
	if mountName == "" {
		s.jsonError(w, "Mount name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		for _, m := range s.mounts.List() {
			if m.Name == mountName {
				s.jsonResponse(w, http.StatusOK, m)
				return
			}
		}
		s.jsonError(w, "Mount not found", http.StatusNotFound)

	case http.MethodDelete:
		if err := s.mounts.Unmount(mountName); err != nil {
			s.jsonError(w, "Failed to unmount: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.jsonResponse(w, http.StatusOK, map[string]string{
			"message": fmt.Sprintf("Mount %s removed", mountName),
		})

	default:
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- VFS File Operations ---

func (s *Server) handleVFSOperation(w http.ResponseWriter, r *http.Request) {
	if s.mounts == nil {
		s.jsonError(w, "Mount registry not available", http.StatusInternalServerError)
		return
	}

	// Route: /api/vfs/{mountName}/{operation}
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/vfs/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		s.jsonError(w, "Invalid VFS operation route", http.StatusBadRequest)
		return
	}

	mountName := parts[0]
	operation := parts[1]
	targetPath := r.URL.Query().Get("path")
	if targetPath == "" {
		targetPath = "/"
	}

	fs, found := s.mounts.Get(mountName)
	if !found {
		s.jsonError(w, fmt.Sprintf("Mount %s not found", mountName), http.StatusNotFound)
		return
	}

	switch operation {
	case "stat":
		if r.Method != http.MethodGet {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		info, err := fs.Stat(targetPath)
		if err != nil {
			s.jsonError(w, "Stat failed: "+err.Error(), http.StatusNotFound)
			return
		}
		s.jsonResponse(w, http.StatusOK, info)

	case "readdir":
		if r.Method != http.MethodGet {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		entries, err := fs.ReadDir(targetPath)
		if err != nil {
			s.jsonError(w, "ReadDir failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.jsonResponse(w, http.StatusOK, entries)

	case "download":
		if r.Method != http.MethodGet {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		info, err := fs.Stat(targetPath)
		if err != nil {
			s.jsonError(w, "File not found: "+err.Error(), http.StatusNotFound)
			return
		}
		if info.IsDir {
			s.jsonError(w, "Cannot download a directory directly", http.StatusBadRequest)
			return
		}

		rc, err := fs.Open(targetPath)
		if err != nil {
			s.jsonError(w, "Failed opening file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rc.Close()

		// Read seeker if supported or fallback to full/chunked transfer
		if seeker, ok := rc.(io.ReadSeeker); ok {
			http.ServeContent(w, r, info.Name, info.ModTime, seeker)
			return
		}

		// Non-seeker streaming
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", info.Name))
		if info.Size > 0 {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size))
		}
		_, _ = io.Copy(w, rc)

	case "upload":
		if r.Method != http.MethodPost {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Support multipart upload or raw body
		var reader io.Reader = r.Body
		filename := path.Base(targetPath)

		if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
			mr, err := r.MultipartReader()
			if err == nil {
				for {
					part, err := mr.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						s.jsonError(w, "Multipart parse error: "+err.Error(), http.StatusBadRequest)
						return
					}
					if part.FileName() != "" {
						reader = part
						filename = part.FileName()
						break
					}
				}
			}
		}

		destPath := targetPath
		if strings.HasSuffix(destPath, "/") {
			destPath = path.Join(destPath, filename)
		}

		wc, err := fs.Create(destPath)
		if err != nil {
			s.jsonError(w, "Create file failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer wc.Close()

		written, err := io.Copy(wc, reader)
		if err != nil {
			s.jsonError(w, "Write file failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		s.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"message": "File uploaded successfully",
			"path":    destPath,
			"bytes":   written,
		})

	case "mkdir":
		if r.Method != http.MethodPost {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := fs.MkdirAll(targetPath); err != nil {
			s.jsonError(w, "Mkdir failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Directory created", "path": targetPath})

	case "delete":
		if r.Method != http.MethodDelete {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := fs.RemoveAll(targetPath); err != nil {
			s.jsonError(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Deleted successfully", "path": targetPath})

	case "rename":
		if r.Method != http.MethodPost {
			s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			OldPath string `json:"old_path"`
			NewPath string `json:"new_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.jsonError(w, "Invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := fs.Rename(req.OldPath, req.NewPath); err != nil {
			s.jsonError(w, "Rename failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Renamed successfully"})

	default:
		s.jsonError(w, "Unknown VFS operation: "+operation, http.StatusBadRequest)
	}
}

// --- Cross-Mount Transfer Handler (Remote <-> RemoteX2 and RemoteX2 -> RemoteX2) ---

type VFSTransferRequest struct {
	SrcMount string `json:"src_mount"`
	SrcPath  string `json:"src_path"`
	DstMount string `json:"dst_mount"`
	DstPath  string `json:"dst_path"`
	Move     bool   `json:"move"`
}

func (s *Server) handleVFSTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.mounts == nil || s.engine == nil {
		s.jsonError(w, "Engine or Mount Registry not available", http.StatusInternalServerError)
		return
	}

	var req VFSTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid transfer request: "+err.Error(), http.StatusBadRequest)
		return
	}

	srcFS, found := s.mounts.Get(req.SrcMount)
	if !found {
		s.jsonError(w, fmt.Sprintf("Source mount %s not found", req.SrcMount), http.StatusBadRequest)
		return
	}

	dstFS, found := s.mounts.Get(req.DstMount)
	if !found {
		s.jsonError(w, fmt.Sprintf("Destination mount %s not found", req.DstMount), http.StatusBadRequest)
		return
	}

	taskType := engine.TaskTypeCopy
	if req.Move {
		taskType = engine.TaskTypeMove
	}

	task := &engine.Task{
		Type:        taskType,
		Description: fmt.Sprintf("Transfer from %s:%s to %s:%s", req.SrcMount, req.SrcPath, req.DstMount, req.DstPath),
		Params: engine.TaskParams{
			SrcFS:   srcFS,
			SrcPath: req.SrcPath,
			DstFS:   dstFS,
			DstPath: req.DstPath,
		},
		Metadata: map[string]string{
			"src_mount": req.SrcMount,
			"src_path":  req.SrcPath,
			"dst_mount": req.DstMount,
			"dst_path":  req.DstPath,
		},
	}

	handle, err := s.engine.Submit(task)
	if err != nil {
		s.jsonError(w, "Failed submitting transfer task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.AttachTaskCallbacks(handle)

	s.jsonResponse(w, http.StatusAccepted, formatTaskResponse(handle))
}

// --- Stream Proxy Management Handlers ---

type RegisterStreamRequest struct {
	Name      string `json:"name"`
	MountName string `json:"mount"`
	Path      string `json:"path"`
	URL       string `json:"url"` // External direct URL fallback
}

func (s *Server) handleStreams(w http.ResponseWriter, r *http.Request) {
	if s.streamServ == nil {
		s.jsonError(w, "Stream server not available", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.jsonResponse(w, http.StatusOK, s.streamServ.ListSources())

	case http.MethodPost:
		var req RegisterStreamRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.jsonError(w, "Invalid stream request: "+err.Error(), http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			s.jsonError(w, "Stream name required", http.StatusBadRequest)
			return
		}

		var source proxy.Source
		var err error

		if req.MountName != "" && req.Path != "" {
			fs, found := s.mounts.Get(req.MountName)
			if !found {
				s.jsonError(w, fmt.Sprintf("Mount %s not found", req.MountName), http.StatusBadRequest)
				return
			}
			source, err = proxy.NewVFSSource(fs, req.Path, true)
		} else if req.URL != "" {
			source, err = proxy.NewHTTPSource(req.URL, nil)
		} else {
			s.jsonError(w, "Either mount+path or URL required", http.StatusBadRequest)
			return
		}

		if err != nil {
			s.jsonError(w, "Failed creating stream source: "+err.Error(), http.StatusBadRequest)
			return
		}

		s.streamServ.RegisterSource(req.Name, source)
		s.jsonResponse(w, http.StatusCreated, map[string]string{
			"message":    "Stream registered",
			"name":       req.Name,
			"stream_url": fmt.Sprintf("/stream/%s", req.Name),
		})

	default:
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleStreamByName(w http.ResponseWriter, r *http.Request) {
	if s.streamServ == nil {
		s.jsonError(w, "Stream server not available", http.StatusInternalServerError)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/streams/")
	if name == "" {
		s.jsonError(w, "Stream name required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		s.streamServ.UnregisterSource(name)
		s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Stream unregistered"})
	default:
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Clipboard Handlers ---

func (s *Server) handleClipboard(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil || s.engine.Clipboard() == nil {
		s.jsonError(w, "Clipboard not available", http.StatusInternalServerError)
		return
	}

	cb := s.engine.Clipboard()
	switch r.Method {
	case http.MethodGet:
		s.jsonResponse(w, http.StatusOK, cb.Items())

	case http.MethodPost:
		var req struct {
			MountName string `json:"mount"`
			Path      string `json:"path"`
			Cut       bool   `json:"cut"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.jsonError(w, "Invalid clipboard request: "+err.Error(), http.StatusBadRequest)
			return
		}

		fs, found := s.mounts.Get(req.MountName)
		if !found {
			s.jsonError(w, fmt.Sprintf("Mount %s not found", req.MountName), http.StatusBadRequest)
			return
		}

		if req.Cut {
			cb.Cut(fs, req.Path)
		} else {
			cb.Copy(fs, req.Path)
		}

		s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Item added to clipboard"})

	case http.MethodDelete:
		cb.Clear()
		s.jsonResponse(w, http.StatusOK, map[string]string{"message": "Clipboard cleared"})

	default:
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleClipboardPaste(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil || s.engine.Clipboard() == nil {
		s.jsonError(w, "Clipboard not available", http.StatusInternalServerError)
		return
	}

	if r.Method != http.MethodPost {
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DstMount string `json:"dst_mount"`
		DstPath  string `json:"dst_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "Invalid paste request: "+err.Error(), http.StatusBadRequest)
		return
	}

	dstFS, found := s.mounts.Get(req.DstMount)
	if !found {
		s.jsonError(w, fmt.Sprintf("Destination mount %s not found", req.DstMount), http.StatusBadRequest)
		return
	}

	items := s.engine.Clipboard().Items()
	if len(items) == 0 {
		s.jsonError(w, "Clipboard is empty", http.StatusBadRequest)
		return
	}

	var handles []TaskResponse
	for _, item := range items {
		taskType := engine.TaskTypeCopy
		if item.Op == clipboard.OpCut {
			taskType = engine.TaskTypeMove
		}

		targetItemPath := path.Join(req.DstPath, path.Base(item.Path))
		task := &engine.Task{
			Type:        taskType,
			Description: fmt.Sprintf("Paste %s to %s:%s", item.Path, req.DstMount, targetItemPath),
			Params: engine.TaskParams{
				SrcFS:   item.SourceFS,
				SrcPath: item.Path,
				DstFS:   dstFS,
				DstPath: targetItemPath,
			},
		}

		h, err := s.engine.Submit(task)
		if err == nil {
			s.AttachTaskCallbacks(h)
			handles = append(handles, formatTaskResponse(h))
		}
	}

	s.engine.Clipboard().Clear()
	s.jsonResponse(w, http.StatusAccepted, handles)
}

// --- Protocol Servers Status Handler ---

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.jsonError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Report active proxy and storage server statuses
	statuses := map[string]interface{}{
		"stream_proxy": map[string]interface{}{
			"active":  s.streamServ != nil,
			"sources": len(s.streamServ.ListSources()),
		},
		"engine": map[string]interface{}{
			"active":     s.engine != nil,
			"task_count": len(s.engine.ListTasks()),
		},
		"mounts": map[string]interface{}{
			"count": len(s.mounts.List()),
		},
	}

	s.jsonResponse(w, http.StatusOK, statuses)
}

var (
	_ = errors.New
	_ = context.Background
)
