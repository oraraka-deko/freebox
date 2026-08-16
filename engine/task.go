package engine

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"freebox/proxy"
	"freebox/vfs"
)

// TaskType identifies the category of operation.
type TaskType string

const (
	TaskTypeCreate  TaskType = "CREATE"
	TaskTypeDelete  TaskType = "DELETE"
	TaskTypeCopy    TaskType = "COPY"
	TaskTypeMove    TaskType = "MOVE"
	TaskTypeOpen    TaskType = "OPEN"
	TaskTypeServe   TaskType = "SERVE"
	TaskTypeProxy   TaskType = "PROXY"
	TaskTypeCustom  TaskType = "CUSTOM"
)

// TaskStatus represents the current state of a task.
type TaskStatus string

const (
	StatusPending   TaskStatus = "PENDING"
	StatusRunning   TaskStatus = "RUNNING"
	StatusPaused    TaskStatus = "PAUSED"
	StatusCompleted TaskStatus = "COMPLETED"
	StatusFailed    TaskStatus = "FAILED"
	StatusCanceled  TaskStatus = "CANCELED"
)

// TaskProgress contains real-time progress metrics.
type TaskProgress struct {
	BytesProcessed int64
	TotalBytes     int64
	Percent        float64
	CurrentItem    string
	ItemsProcessed int64
	TotalItems     int64
	SpeedBytesSec  float64
	StartTime      time.Time
	EndTime        time.Time
	Duration       time.Duration
	Error          error
}

// TaskParams holds parameters for various task operations.
type TaskParams struct {
	// File system operations
	SrcFS    vfs.FileSystem
	DstFS    vfs.FileSystem
	SrcPath  string
	DstPath  string
	Data     []byte
	IsDir    bool
	Filter   func(fi *vfs.FileInfo) bool

	// Stream / Proxy operations
	ProxySource proxy.Source
	ProxyName   string
	StreamURL   string

	// Serving operations
	Protocol string // "http", "webdav", "ftp", "smb", "ssh", "dlna"
	Addr     string
	Server   any // underlying server instance

	// Custom execution
	Action func(ctx context.Context, handle *TaskHandle) error
}

// Task describes a unit of work to be executed immediately or via the queue.
type Task struct {
	ID          string
	Type        TaskType
	Description string
	Priority    int // Higher number = higher priority in queue
	Params      TaskParams
	Metadata    map[string]string
}

// TaskHandle provides thread-safe observation and control over a task.
type TaskHandle struct {
	task       *Task
	status     TaskStatus
	progress   TaskProgress
	mu         sync.RWMutex
	ctx        context.Context
	cancelFunc context.CancelFunc
	pauseChan  chan struct{}
	resumeChan chan struct{}
	isPaused   bool
	doneChan   chan struct{}
	onProgress func(p TaskProgress)
	onStatus   func(s TaskStatus)
	result     any
	openedFile io.ReadCloser
}

// NewTaskHandle creates a new TaskHandle for task.
func NewTaskHandle(t *Task, parentCtx context.Context) *TaskHandle {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)

	h := &TaskHandle{
		task:       t,
		status:     StatusPending,
		ctx:        ctx,
		cancelFunc: cancel,
		pauseChan:  make(chan struct{}, 1),
		resumeChan: make(chan struct{}, 1),
		doneChan:   make(chan struct{}),
		progress: TaskProgress{
			StartTime: time.Now(),
		},
	}
	return h
}

// ID returns the task ID.
func (h *TaskHandle) ID() string {
	return h.task.ID
}

// Task returns the underlying task definition.
func (h *TaskHandle) Task() *Task {
	return h.task
}

// Type returns the task type.
func (h *TaskHandle) Type() TaskType {
	return h.task.Type
}

// Status returns the current task status.
func (h *TaskHandle) Status() TaskStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status
}

// Progress returns a snapshot of task progress metrics.
func (h *TaskHandle) Progress() TaskProgress {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p := h.progress
	if h.status == StatusRunning {
		p.Duration = time.Since(p.StartTime)
		if p.Duration.Seconds() > 0 && p.BytesProcessed > 0 {
			p.SpeedBytesSec = float64(p.BytesProcessed) / p.Duration.Seconds()
		}
	}
	if p.TotalBytes > 0 {
		p.Percent = (float64(p.BytesProcessed) / float64(p.TotalBytes)) * 100.0
	}
	return p
}

// UpdateProgress updates the internal progress state and triggers callbacks.
func (h *TaskHandle) UpdateProgress(bytesProcessed, totalBytes int64, currentItem string) {
	h.mu.Lock()
	h.progress.BytesProcessed = bytesProcessed
	h.progress.TotalBytes = totalBytes
	h.progress.CurrentItem = currentItem
	if totalBytes > 0 {
		h.progress.Percent = (float64(bytesProcessed) / float64(totalBytes)) * 100.0
	}
	cb := h.onProgress
	snapshot := h.progress
	h.mu.Unlock()

	if cb != nil {
		cb(snapshot)
	}
}

// SetStatus updates status and triggers status change callback.
func (h *TaskHandle) SetStatus(s TaskStatus) {
	h.mu.Lock()
	h.status = s
	if s == StatusCompleted || s == StatusFailed || s == StatusCanceled {
		h.progress.EndTime = time.Now()
		h.progress.Duration = h.progress.EndTime.Sub(h.progress.StartTime)
	}
	cb := h.onStatus
	h.mu.Unlock()

	if cb != nil {
		cb(s)
	}
}

// OnProgress registers a callback for progress updates.
func (h *TaskHandle) OnProgress(fn func(p TaskProgress)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onProgress = fn
}

// OnStatus registers a callback for status changes.
func (h *TaskHandle) OnStatus(fn func(s TaskStatus)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onStatus = fn
}

// Cancel cancels task execution.
func (h *TaskHandle) Cancel() {
	h.cancelFunc()
	h.SetStatus(StatusCanceled)
}

// Context returns task execution context.
func (h *TaskHandle) Context() context.Context {
	return h.ctx
}

// Pause pauses task execution.
func (h *TaskHandle) Pause() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status != StatusRunning {
		return errors.New("cannot pause task that is not running")
	}
	h.isPaused = true
	h.status = StatusPaused
	return nil
}

// Resume resumes a paused task.
func (h *TaskHandle) Resume() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status != StatusPaused {
		return errors.New("cannot resume task that is not paused")
	}
	h.isPaused = false
	h.status = StatusRunning
	select {
	case h.resumeChan <- struct{}{}:
	default:
	}
	return nil
}

// CheckPause blocks if task is paused, until resumed or canceled.
func (h *TaskHandle) CheckPause() error {
	h.mu.RLock()
	paused := h.isPaused
	h.mu.RUnlock()

	if !paused {
		return h.ctx.Err()
	}

	select {
	case <-h.ctx.Done():
		return h.ctx.Err()
	case <-h.resumeChan:
		return nil
	}
}

// Wait blocks until the task finishes execution.
func (h *TaskHandle) Wait() TaskStatus {
	<-h.doneChan
	return h.Status()
}

// SetResult stores the task execution result.
func (h *TaskHandle) SetResult(res any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.result = res
}

// Result returns the task execution result.
func (h *TaskHandle) Result() any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.result
}

// TaskRecord represents serializable persistent state of a task in the database.
type TaskRecord struct {
	ID             string            `json:"id"`
	Type           TaskType          `json:"type"`
	Description    string            `json:"description"`
	Status         TaskStatus        `json:"status"`
	Priority       int               `json:"priority"`
	SrcPath        string            `json:"src_path,omitempty"`
	DstPath        string            `json:"dst_path,omitempty"`
	BytesProcessed int64             `json:"bytes_processed"`
	TotalBytes     int64             `json:"total_bytes"`
	Percent        float64           `json:"percent"`
	CurrentItem    string            `json:"current_item,omitempty"`
	StartTime      time.Time         `json:"start_time"`
	EndTime        time.Time         `json:"end_time,omitempty"`
	Duration       time.Duration     `json:"duration"`
	Error          string            `json:"error,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// ToRecord exports the task handle's current state to a TaskRecord.
func (h *TaskHandle) ToRecord() TaskRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	p := h.progress
	errStr := ""
	if p.Error != nil {
		errStr = p.Error.Error()
	}

	return TaskRecord{
		ID:             h.task.ID,
		Type:           h.task.Type,
		Description:    h.task.Description,
		Status:         h.status,
		Priority:       h.task.Priority,
		SrcPath:        h.task.Params.SrcPath,
		DstPath:        h.task.Params.DstPath,
		BytesProcessed: p.BytesProcessed,
		TotalBytes:     p.TotalBytes,
		Percent:        p.Percent,
		CurrentItem:    p.CurrentItem,
		StartTime:      p.StartTime,
		EndTime:        p.EndTime,
		Duration:       p.Duration,
		Error:          errStr,
		Metadata:       h.task.Metadata,
	}
}

