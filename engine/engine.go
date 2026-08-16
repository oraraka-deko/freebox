package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"freebox/clipboard"
	"freebox/dlna"
	"freebox/ftp"
	fbHttp "freebox/http"
	"freebox/local"
	"freebox/proxy"
	"freebox/smb"
	"freebox/ssh"
	"freebox/storage"
	"freebox/vfs"
)

// Engine is the central manager coordinating all tasks, VFS, streaming proxies, and servers.
type Engine struct {
	queue        *TaskQueue
	tasks        map[string]*TaskHandle
	db           *storage.DB
	streamServer *proxy.StreamServer
	streamPort   string
	servers      map[string]any
	clipboard    *clipboard.Clipboard
	local        *local.LocalManager
	mu           sync.RWMutex
	taskCounter  uint64
}

// EngineConfig holds configuration options for the main engine.
type EngineConfig struct {
	MaxWorkers int
	StreamPort string
	DB         *storage.DB
}

// DefaultEngineConfig returns default engine settings.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		MaxWorkers: 4,
		StreamPort: ":8090",
	}
}

// NewEngine initializes a new task management engine.
func NewEngine(cfg EngineConfig) *Engine {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 4
	}
	if cfg.StreamPort == "" {
		cfg.StreamPort = ":8090"
	}

	e := &Engine{
		tasks:        make(map[string]*TaskHandle),
		db:           cfg.DB,
		servers:      make(map[string]any),
		streamServer: proxy.NewStreamServer(),
		streamPort:   cfg.StreamPort,
		clipboard:    clipboard.NewClipboard(),
		local:        local.NewLocalManager(),
	}

	e.queue = NewTaskQueue(QueueConfig{MaxWorkers: cfg.MaxWorkers}, e.processTask)
	return e
}

func (e *Engine) generateID(prefix string) string {
	val := atomic.AddUint64(&e.taskCounter, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().Unix(), val)
}

// Clipboard returns the integrated staging clipboard.
func (e *Engine) Clipboard() *clipboard.Clipboard {
	return e.clipboard
}

// Local returns the local filesystem manager.
func (e *Engine) Local() *local.LocalManager {
	return e.local
}

// StreamServer returns the central proxy stream server.
func (e *Engine) StreamServer() *proxy.StreamServer {
	return e.streamServer
}

// SetDB sets or updates the backing storage DB.
func (e *Engine) SetDB(db *storage.DB) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.db = db
}

// Submit enqueues a task for asynchronous execution in the worker queue.
func (e *Engine) Submit(task *Task) (*TaskHandle, error) {
	if task.ID == "" {
		task.ID = e.generateID(string(task.Type))
	}

	handle := NewTaskHandle(task, context.Background())

	e.mu.Lock()
	e.tasks[task.ID] = handle
	e.mu.Unlock()

	e.saveTaskToDB(handle)

	if err := e.queue.Enqueue(handle); err != nil {
		handle.SetStatus(StatusFailed)
		e.saveTaskToDB(handle)
		return nil, err
	}
	return handle, nil
}

// Execute immediately runs a task synchronously without using the queue.
func (e *Engine) Execute(ctx context.Context, task *Task) (*TaskHandle, error) {
	if task.ID == "" {
		task.ID = e.generateID(string(task.Type))
	}

	handle := NewTaskHandle(task, ctx)

	e.mu.Lock()
	e.tasks[task.ID] = handle
	e.mu.Unlock()

	e.saveTaskToDB(handle)

	err := e.processTask(handle)
	return handle, err
}

func (e *Engine) saveTaskToDB(h *TaskHandle) {
	if e.db == nil || h == nil {
		return
	}
	rec := h.ToRecord()
	data, err := json.Marshal(rec)
	if err == nil {
		_ = e.db.PutEncrypted(storage.BucketTasks, rec.ID, data)
	}
}

// ListHistory retrieves persisted task history from the database.
func (e *Engine) ListHistory() ([]TaskRecord, error) {
	if e.db == nil {
		return nil, nil
	}

	rawMap, err := e.db.ListDecrypted(storage.BucketTasks)
	if err != nil {
		return nil, err
	}

	var history []TaskRecord
	for _, data := range rawMap {
		var rec TaskRecord
		if err := json.Unmarshal(data, &rec); err == nil {
			history = append(history, rec)
		}
	}
	return history, nil
}

// DeleteTask removes a task from tracking and database.
func (e *Engine) DeleteTask(id string) error {
	e.mu.Lock()
	delete(e.tasks, id)
	e.mu.Unlock()

	if e.db != nil {
		return e.db.Delete(storage.BucketTasks, id)
	}
	return nil
}

// GetTask returns the task handle by ID.
func (e *Engine) GetTask(id string) (*TaskHandle, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	h, ok := e.tasks[id]
	return h, ok
}

// ListTasks returns all tracked task handles.
func (e *Engine) ListTasks() []*TaskHandle {
	e.mu.RLock()
	defer e.mu.RUnlock()
	results := make([]*TaskHandle, 0, len(e.tasks))
	for _, h := range e.tasks {
		results = append(results, h)
	}
	return results
}

// CancelTask cancels an active or queued task.
func (e *Engine) CancelTask(id string) error {
	h, ok := e.GetTask(id)
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	h.Cancel()
	return nil
}

// PauseTask pauses a running task.
func (e *Engine) PauseTask(id string) error {
	h, ok := e.GetTask(id)
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	return h.Pause()
}

// ResumeTask resumes a paused task.
func (e *Engine) ResumeTask(id string) error {
	h, ok := e.GetTask(id)
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	return h.Resume()
}

// Close gracefully stops the task engine and all worker queues.
func (e *Engine) Close() {
	e.queue.Close()
	_ = e.streamServer.Close()
}

// --- Internal Task Processor ---

func (e *Engine) processTask(h *TaskHandle) error {
	defer func() {
		e.saveTaskToDB(h)
		close(h.doneChan)
	}()

	if h.Status() == StatusCanceled {
		return context.Canceled
	}

	h.SetStatus(StatusRunning)
	e.saveTaskToDB(h)
	var err error

	switch h.task.Type {
	case TaskTypeCreate:
		err = e.execCreate(h)
	case TaskTypeDelete:
		err = e.execDelete(h)
	case TaskTypeCopy:
		err = e.execCopy(h)
	case TaskTypeMove:
		err = e.execMove(h)
	case TaskTypeOpen:
		err = e.execOpen(h)
	case TaskTypeProxy:
		err = e.execProxy(h)
	case TaskTypeServe:
		err = e.execServe(h)
	case TaskTypeCustom:
		if h.task.Params.Action != nil {
			err = h.task.Params.Action(h.Context(), h)
		}
	default:
		err = fmt.Errorf("unknown task type %s", h.task.Type)
	}

	if err != nil {
		if h.Status() != StatusCanceled {
			h.mu.Lock()
			h.progress.Error = err
			h.mu.Unlock()
			h.SetStatus(StatusFailed)
		}
		return err
	}

	if h.Status() == StatusRunning {
		h.SetStatus(StatusCompleted)
	}
	return nil
}

func (e *Engine) execCreate(h *TaskHandle) error {
	params := h.task.Params
	if params.DstFS == nil {
		return fmt.Errorf("target filesystem required")
	}

	if params.IsDir {
		return params.DstFS.MkdirAll(params.DstPath)
	}

	return params.DstFS.Write(params.DstPath, params.Data)
}

func (e *Engine) execDelete(h *TaskHandle) error {
	params := h.task.Params
	if params.SrcFS == nil {
		return fmt.Errorf("filesystem required")
	}
	return params.SrcFS.RemoveAll(params.SrcPath)
}

func (e *Engine) execCopy(h *TaskHandle) error {
	params := h.task.Params
	if params.SrcFS == nil || params.DstFS == nil {
		return fmt.Errorf("source and destination filesystems required")
	}

	srcInfo, err := params.SrcFS.Stat(params.SrcPath)
	if err != nil {
		return fmt.Errorf("failed to stat source: %w", err)
	}

	if !srcInfo.IsDir {
		return e.copySingleFile(h, params.SrcFS, params.SrcPath, params.DstFS, params.DstPath, srcInfo.Size)
	}

	// Copy directory tree
	return params.SrcFS.Walk(params.SrcPath, func(p string, fi *vfs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := h.CheckPause(); err != nil {
			return err
		}

		rel := strings.TrimPrefix(p, params.SrcPath)
		rel = strings.TrimPrefix(rel, "/")
		target := vfs.NormalizePath(params.DstPath + "/" + rel)

		if fi.IsDir {
			return params.DstFS.MkdirAll(target)
		}

		return e.copySingleFile(h, params.SrcFS, p, params.DstFS, target, fi.Size)
	})
}

func (e *Engine) copySingleFile(h *TaskHandle, srcFS vfs.FileSystem, srcPath string, dstFS vfs.FileSystem, dstPath string, totalSize int64) error {
	_ = dstFS.MkdirAll(path.Dir(dstPath))

	r, err := srcFS.Open(srcPath)
	if err != nil {
		return err
	}
	defer r.Close()

	w, err := dstFS.Create(dstPath)
	if err != nil {
		return err
	}
	defer w.Close()

	buf := make([]byte, 64*1024)
	var copied int64

	for {
		if err := h.CheckPause(); err != nil {
			return err
		}

		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = io.ErrShortWrite
				}
			}
			copied += int64(nw)
			h.UpdateProgress(copied, totalSize, srcPath)
			if ew != nil {
				return ew
			}
			if nr != nw {
				return io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				break
			}
			return er
		}
	}
	return nil
}

func (e *Engine) execMove(h *TaskHandle) error {
	params := h.task.Params
	if params.SrcFS == nil {
		return fmt.Errorf("filesystem required")
	}

	// If same filesystem, use Rename
	if params.DstFS == nil || params.SrcFS == params.DstFS {
		return params.SrcFS.Rename(params.SrcPath, params.DstPath)
	}

	// Cross-filesystem Move: Copy then Delete
	if err := e.execCopy(h); err != nil {
		return err
	}
	return params.SrcFS.RemoveAll(params.SrcPath)
}

func (e *Engine) execOpen(h *TaskHandle) error {
	params := h.task.Params
	if params.SrcFS == nil {
		return fmt.Errorf("filesystem required")
	}
	rc, err := params.SrcFS.Open(params.SrcPath)
	if err != nil {
		return err
	}
	h.openedFile = rc
	h.SetResult(rc)
	return nil
}

func (e *Engine) execProxy(h *TaskHandle) error {
	params := h.task.Params
	if params.ProxySource == nil {
		return fmt.Errorf("proxy source required")
	}
	if params.ProxyName == "" {
		params.ProxyName = "stream-" + h.ID()
	}

	e.streamServer.RegisterSource(params.ProxyName, params.ProxySource)
	streamURL := fmt.Sprintf("http://127.0.0.1%s/stream/%s", e.streamPort, params.ProxyName)
	h.SetResult(streamURL)
	return nil
}

func (e *Engine) execServe(h *TaskHandle) error {
	params := h.task.Params
	protocol := strings.ToLower(params.Protocol)
	addr := params.Addr

	switch protocol {
	case "http":
		srv := fbHttp.NewServer(fbHttp.DefaultConfig())
		e.registerServer(h.ID(), srv)
		return srv.ListenAndServe(addr)
	case "webdav":
		srv := fbHttp.NewWebDAVServer(fbHttp.DefaultWebDAVConfig())
		e.registerServer(h.ID(), srv)
		return srv.ListenAndServe(addr)
	case "ftp":
		srv := ftp.NewServer(ftp.DefaultConfig())
		e.registerServer(h.ID(), srv)
		return srv.ListenAndServe(addr)
	case "smb":
		srv := smb.NewServer(smb.DefaultConfig())
		e.registerServer(h.ID(), srv)
		return srv.ListenAndServe(addr)
	case "ssh", "sftp":
		srv := ssh.NewServer(ssh.DefaultConfig())
		e.registerServer(h.ID(), srv)
		return srv.ListenAndServe(addr)
	case "dlna":
		srv := dlna.NewServer(dlna.DefaultConfig())
		e.registerServer(h.ID(), srv)
		return srv.ListenAndServe(addr)
	default:
		return fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

func (e *Engine) registerServer(id string, srv any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.servers[id] = srv
}

// --- High-Level Convenience Methods (with queued or immediate options) ---

// CreateFile creates a file with data on fs.
func (e *Engine) CreateFile(fs vfs.FileSystem, filePath string, data []byte, queued bool) (*TaskHandle, error) {
	task := &Task{
		Type:        TaskTypeCreate,
		Description: fmt.Sprintf("Create file %s", filePath),
		Params: TaskParams{
			DstFS:   fs,
			DstPath: filePath,
			Data:    data,
			IsDir:   false,
		},
	}
	if queued {
		return e.Submit(task)
	}
	return e.Execute(context.Background(), task)
}

// CreateDir creates a directory on fs.
func (e *Engine) CreateDir(fs vfs.FileSystem, dirPath string, queued bool) (*TaskHandle, error) {
	task := &Task{
		Type:        TaskTypeCreate,
		Description: fmt.Sprintf("Create directory %s", dirPath),
		Params: TaskParams{
			DstFS:   fs,
			DstPath: dirPath,
			IsDir:   true,
		},
	}
	if queued {
		return e.Submit(task)
	}
	return e.Execute(context.Background(), task)
}

// Delete removes a file or directory from fs.
func (e *Engine) Delete(fs vfs.FileSystem, targetPath string, queued bool) (*TaskHandle, error) {
	task := &Task{
		Type:        TaskTypeDelete,
		Description: fmt.Sprintf("Delete %s", targetPath),
		Params: TaskParams{
			SrcFS:   fs,
			SrcPath: targetPath,
		},
	}
	if queued {
		return e.Submit(task)
	}
	return e.Execute(context.Background(), task)
}

// Copy copies a file or directory tree from srcFS to dstFS.
func (e *Engine) Copy(srcFS vfs.FileSystem, srcPath string, dstFS vfs.FileSystem, dstPath string, queued bool) (*TaskHandle, error) {
	task := &Task{
		Type:        TaskTypeCopy,
		Description: fmt.Sprintf("Copy %s -> %s", srcPath, dstPath),
		Params: TaskParams{
			SrcFS:   srcFS,
			SrcPath: srcPath,
			DstFS:   dstFS,
			DstPath: dstPath,
		},
	}
	if queued {
		return e.Submit(task)
	}
	return e.Execute(context.Background(), task)
}

// Move moves a file or directory from srcPath to dstPath.
func (e *Engine) Move(srcFS vfs.FileSystem, srcPath string, dstFS vfs.FileSystem, dstPath string, queued bool) (*TaskHandle, error) {
	task := &Task{
		Type:        TaskTypeMove,
		Description: fmt.Sprintf("Move %s -> %s", srcPath, dstPath),
		Params: TaskParams{
			SrcFS:   srcFS,
			SrcPath: srcPath,
			DstFS:   dstFS,
			DstPath: dstPath,
		},
	}
	if queued {
		return e.Submit(task)
	}
	return e.Execute(context.Background(), task)
}

// Open opens a file stream for reading.
func (e *Engine) Open(fs vfs.FileSystem, filePath string) (io.ReadCloser, error) {
	task := &Task{
		Type:        TaskTypeOpen,
		Description: fmt.Sprintf("Open %s", filePath),
		Params: TaskParams{
			SrcFS:   fs,
			SrcPath: filePath,
		},
	}
	h, err := e.Execute(context.Background(), task)
	if err != nil {
		return nil, err
	}
	return h.openedFile, nil
}

// Proxy registers a random-access Source with the stream proxy server.
func (e *Engine) Proxy(src proxy.Source, name string, queued bool) (*TaskHandle, error) {
	task := &Task{
		Type:        TaskTypeProxy,
		Description: fmt.Sprintf("Proxy stream %s", name),
		Params: TaskParams{
			ProxySource: src,
			ProxyName:   name,
		},
	}
	if queued {
		return e.Submit(task)
	}
	return e.Execute(context.Background(), task)
}

// --- Clipboard Integration ---

// ClipboardCopy stages paths from srcFS into the engine's clipboard for copying.
func (e *Engine) ClipboardCopy(srcFS vfs.FileSystem, paths ...string) {
	e.clipboard.Copy(srcFS, paths...)
}

// ClipboardCut stages paths from srcFS into the engine's clipboard for moving.
func (e *Engine) ClipboardCut(srcFS vfs.FileSystem, paths ...string) {
	e.clipboard.Cut(srcFS, paths...)
}

// ClipboardPaste applies all staged clipboard operations to dstDir on dstFS.
func (e *Engine) ClipboardPaste(dstFS vfs.FileSystem, dstDir string, queued bool) ([]*TaskHandle, error) {
	plans, err := e.clipboard.PlanPaste(dstFS, dstDir)
	if err != nil {
		return nil, err
	}

	handles := make([]*TaskHandle, 0, len(plans))
	hasCut := false

	for _, plan := range plans {
		var h *TaskHandle
		var opErr error

		switch plan.Op {
		case clipboard.OpCopy:
			h, opErr = e.Copy(plan.SrcFS, plan.SrcPath, plan.DstFS, plan.DstPath, queued)
		case clipboard.OpCut:
			hasCut = true
			h, opErr = e.Move(plan.SrcFS, plan.SrcPath, plan.DstFS, plan.DstPath, queued)
		case clipboard.OpCreate:
			if plan.IsDir {
				h, opErr = e.CreateDir(plan.DstFS, plan.DstPath, queued)
			} else {
				h, opErr = e.CreateFile(plan.DstFS, plan.DstPath, plan.Data, queued)
			}
		case clipboard.OpDelete:
			h, opErr = e.Delete(plan.SrcFS, plan.SrcPath, queued)
		default:
			opErr = fmt.Errorf("unsupported clipboard op: %s", plan.Op)
		}

		if opErr != nil {
			return handles, opErr
		}
		handles = append(handles, h)
	}

	if hasCut {
		e.clipboard.Clear()
	}

	return handles, nil
}

// PushLocal uploads a local host file/directory to a remote server VFS path.
func (e *Engine) PushLocal(localPath string, remoteFS vfs.FileSystem, remotePath string, queued bool) (*TaskHandle, error) {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return nil, err
	}
	srcFS := vfs.NewOSFS(filepath.Dir(abs))
	srcRel := "/" + filepath.Base(abs)
	return e.Copy(srcFS, srcRel, remoteFS, remotePath, queued)
}

// PullLocal downloads a remote file/directory from remoteFS to a local host path.
func (e *Engine) PullLocal(remoteFS vfs.FileSystem, remotePath string, localPath string, queued bool) (*TaskHandle, error) {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return nil, err
	}
	_ = e.local.CreateDir(filepath.Dir(abs))
	dstFS := vfs.NewOSFS(filepath.Dir(abs))
	dstRel := "/" + filepath.Base(abs)
	return e.Copy(remoteFS, remotePath, dstFS, dstRel, queued)
}
