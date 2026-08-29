package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"sync"
	"time"

	"freebox/internal/bufferpool"
	"freebox/storage"
	"freebox/vfs"
)

var (
	ErrTransferPaused   = errors.New("transfer paused")
	ErrTransferCanceled = errors.New("transfer canceled")
)

// isNetworkDisconnect checks if an error was caused by a transient network drop.
func isNetworkDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection") ||
		strings.Contains(msg, "reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "refused") ||
		strings.Contains(msg, "network") ||
		strings.Contains(msg, "disconnect")
}

// TransferState represents current state of a resumable transfer session.
type TransferState string

const (
	StateIdle      TransferState = "IDLE"
	StateRunning   TransferState = "RUNNING"
	StatePaused    TransferState = "PAUSED"
	StateCompleted TransferState = "COMPLETED"
	StateFailed    TransferState = "FAILED"
	StateCanceled  TransferState = "CANCELED"
)

// Checkpoint stores persistent resume metadata for a transfer.
type Checkpoint struct {
	ID          string        `json:"id"`
	SrcPath     string        `json:"src_path"`
	DstPath     string        `json:"dst_path"`
	TotalBytes  int64         `json:"total_bytes"`
	BytesCopied int64         `json:"bytes_copied"`
	Offset      int64         `json:"offset"`
	State       TransferState `json:"state"`
	StartTime   time.Time     `json:"start_time"`
	LastActive  time.Time     `json:"last_active"`
	ChunkSize   int           `json:"chunk_size"`
	Error       string        `json:"error,omitempty"`
}

// ResumableTransfer orchestrates pausable, resumable file copying between VFS instances.
type ResumableTransfer struct {
	id         string
	srcFS      vfs.FileSystem
	srcPath    string
	dstFS      vfs.FileSystem
	dstPath    string
	checkpoint Checkpoint
	chunkSize  int
	pauseChan  chan struct{}
	resumeChan chan struct{}
	cancelCtx  context.Context
	cancelFunc context.CancelFunc
	onProgress func(cp Checkpoint)
	mu         sync.RWMutex
}

// NewResumableTransfer creates a new transfer session.
func NewResumableTransfer(id string, srcFS vfs.FileSystem, srcPath string, dstFS vfs.FileSystem, dstPath string, chunkSize int) *ResumableTransfer {
	if chunkSize <= 0 {
		chunkSize = 64 * 1024 // 64 KB
	}
	if id == "" {
		id = fmt.Sprintf("xfer-%d", time.Now().UnixNano())
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ResumableTransfer{
		id:         id,
		srcFS:      srcFS,
		srcPath:    vfs.NormalizePath(srcPath),
		dstFS:      dstFS,
		dstPath:    vfs.NormalizePath(dstPath),
		chunkSize:  chunkSize,
		pauseChan:  make(chan struct{}, 1),
		resumeChan: make(chan struct{}, 1),
		cancelCtx:  ctx,
		cancelFunc: cancel,
		checkpoint: Checkpoint{
			ID:        id,
			SrcPath:   srcPath,
			DstPath:   dstPath,
			State:     StateIdle,
			ChunkSize: chunkSize,
			StartTime: time.Now(),
		},
	}
}

// SetOnProgress registers progress callback.
func (t *ResumableTransfer) SetOnProgress(fn func(cp Checkpoint)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onProgress = fn
}

// Checkpoint returns a snapshot of current transfer checkpoint.
func (t *ResumableTransfer) Checkpoint() Checkpoint {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.checkpoint
}

// Start begins or resumes transfer from current checkpoint offset.
func (t *ResumableTransfer) Start() error {
	t.mu.Lock()
	t.checkpoint.State = StateRunning
	t.checkpoint.LastActive = time.Now()
	t.mu.Unlock()

	srcInfo, err := t.srcFS.Stat(t.srcPath)
	if err != nil {
		t.fail(err)
		return fmt.Errorf("stat src failed: %w", err)
	}

	t.mu.Lock()
	t.checkpoint.TotalBytes = srcInfo.Size
	t.mu.Unlock()

	// Ensure destination directory
	_ = t.dstFS.MkdirAll(path.Dir(t.dstPath))

	srcReader, err := t.srcFS.OpenFile(t.cancelCtx, t.srcPath, vfs.ReadOnly())
	if err != nil {
		t.fail(err)
		return fmt.Errorf("open src failed: %w", err)
	}
	defer srcReader.Close()

	// A future durable journal resumes only from verified native seek boundaries.
	// Until that journal is loaded, never emulate a seek by discarding a prefix.
	currentOffset := t.checkpoint.Offset
	if currentOffset > 0 {
		err := fmt.Errorf("resume without verified random-access state: %w", vfs.ErrUnsupported)
		t.fail(err)
		return err
	}

	// Buffer for copying. The pool keeps concurrent transfer allocation bounded.
	buf := bufferpool.Acquire(t.chunkSize)
	defer bufferpool.Release(buf)
	if len(buf) > t.chunkSize {
		buf = buf[:t.chunkSize]
	}

	dstWriter, err := t.dstFS.OpenFile(t.cancelCtx, t.dstPath, vfs.WriteOnly())
	if err != nil {
		t.fail(err)
		return fmt.Errorf("open destination failed: %w", err)
	}
	defer dstWriter.Close()
	writer, ok := dstWriter.(io.Writer)
	if !ok {
		err := fmt.Errorf("destination stream write: %w", vfs.ErrUnsupported)
		t.fail(err)
		return err
	}

	for {
		// Check cancellation
		if t.cancelCtx.Err() != nil {
			t.mu.Lock()
			t.checkpoint.State = StateCanceled
			t.mu.Unlock()
			return ErrTransferCanceled
		}

		// Check pause
		t.mu.RLock()
		isPaused := (t.checkpoint.State == StatePaused)
		t.mu.RUnlock()

		if isPaused {
			select {
			case <-t.cancelCtx.Done():
				t.mu.Lock()
				t.checkpoint.State = StateCanceled
				t.mu.Unlock()
				return ErrTransferCanceled
			case <-t.resumeChan:
				t.mu.Lock()
				t.checkpoint.State = StateRunning
				t.mu.Unlock()
			}
		}

		n, rErr := srcReader.Read(buf)
		if n > 0 {
			written, err := writer.Write(buf[:n])
			if err != nil {
				if isNetworkDisconnect(err) {
					t.PauseWithReason("network_disconnect", err.Error())
					continue
				}
				t.fail(err)
				return fmt.Errorf("write error: %w", err)
			}
			if written != n {
				err := io.ErrShortWrite
				t.fail(err)
				return err
			}

			t.mu.Lock()
			t.checkpoint.Offset += int64(written)
			t.checkpoint.BytesCopied = t.checkpoint.Offset
			t.checkpoint.LastActive = time.Now()
			cb := t.onProgress
			snapshot := t.checkpoint
			t.mu.Unlock()

			if cb != nil {
				cb(snapshot)
			}
		}

		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			if isNetworkDisconnect(rErr) {
				t.PauseWithReason("network_disconnect", rErr.Error())
				continue
			}
			t.fail(rErr)
			return rErr
		}
	}

	t.mu.Lock()
	t.checkpoint.State = StateCompleted
	t.checkpoint.LastActive = time.Now()
	cb := t.onProgress
	snapshot := t.checkpoint
	t.mu.Unlock()

	if cb != nil {
		cb(snapshot)
	}

	return nil
}

// Pause pauses an active transfer.
func (t *ResumableTransfer) Pause() {
	t.mu.Lock()
	if t.checkpoint.State == StateRunning {
		t.checkpoint.State = StatePaused
	}
	t.mu.Unlock()
}

// PauseWithReason pauses the transfer with an explanatory message (e.g. network disconnect).
func (t *ResumableTransfer) PauseWithReason(reason, detail string) {
	t.mu.Lock()
	t.checkpoint.State = StatePaused
	if detail != "" {
		t.checkpoint.Error = reason + ": " + detail
	} else {
		t.checkpoint.Error = reason
	}
	t.checkpoint.LastActive = time.Now()
	cb := t.onProgress
	snapshot := t.checkpoint
	t.mu.Unlock()

	if cb != nil {
		cb(snapshot)
	}
}

// AutoResume monitors network connectivity and automatically resumes the transfer when online.
func (t *ResumableTransfer) AutoResume(ctx context.Context, checkInterval time.Duration, isOnline func() bool) {
	if checkInterval <= 0 {
		checkInterval = 1 * time.Second
	}
	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.cancelCtx.Done():
				return
			case <-ticker.C:
				t.mu.RLock()
				state := t.checkpoint.State
				t.mu.RUnlock()

				if state == StateCompleted || state == StateCanceled || state == StateFailed {
					return
				}

				if state == StatePaused && (isOnline == nil || isOnline()) {
					t.Resume()
				}
			}
		}
	}()
}

// Resume resumes a paused transfer.
func (t *ResumableTransfer) Resume() {
	t.mu.Lock()
	if t.checkpoint.State == StatePaused {
		t.checkpoint.State = StateRunning
		t.checkpoint.Error = ""
		select {
		case t.resumeChan <- struct{}{}:
		default:
		}
	}
	t.mu.Unlock()
}

// Cancel terminates the transfer.
func (t *ResumableTransfer) Cancel() {
	t.cancelFunc()
	t.mu.Lock()
	t.checkpoint.State = StateCanceled
	t.mu.Unlock()
}

func (t *ResumableTransfer) fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checkpoint.State = StateFailed
	if err != nil {
		t.checkpoint.Error = err.Error()
	}
}

// ResumableManager manages multiple ongoing transfers with persistent checkpoints in DB.
type ResumableManager struct {
	transfers map[string]*ResumableTransfer
	db        *storage.DB
	journal   *Journal
	mu        sync.RWMutex
}

// NewResumableManager creates a transfer manager.
func NewResumableManager(db *storage.DB) *ResumableManager {
	return &ResumableManager{
		transfers: make(map[string]*ResumableTransfer),
		db:        db,
		journal:   NewJournal(db),
	}
}

// Register registers a transfer session.
func (m *ResumableManager) Register(xfer *ResumableTransfer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transfers[xfer.id] = xfer
	_ = m.journal.SaveCheckpoint(xfer.Checkpoint())
}

// Get retrieves an active transfer.
func (m *ResumableManager) Get(id string) (*ResumableTransfer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	xfer, ok := m.transfers[id]
	return xfer, ok
}

// LoadCheckpoint returns the durable metadata for a transfer that may no
// longer be active in this process.
func (m *ResumableManager) LoadCheckpoint(id string) (Checkpoint, error) {
	return m.journal.LoadCheckpoint(id)
}

// AppendChunk records an immutable transfer event.
func (m *ResumableManager) AppendChunk(record ChunkRecord) error {
	return m.journal.Append(record)
}

// LastVerifiedBoundary returns the safe resume boundary for a transfer.
func (m *ResumableManager) LastVerifiedBoundary(id string) (int64, error) {
	return m.journal.LastVerifiedBoundary(id)
}
