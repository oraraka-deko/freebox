package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sync"
	"time"

	"freebox/storage"
	"freebox/vfs"
)

var (
	ErrTransferPaused   = errors.New("transfer paused")
	ErrTransferCanceled = errors.New("transfer canceled")
)

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
	ID             string        `json:"id"`
	SrcPath        string        `json:"src_path"`
	DstPath        string        `json:"dst_path"`
	TotalBytes     int64         `json:"total_bytes"`
	BytesCopied    int64         `json:"bytes_copied"`
	Offset         int64         `json:"offset"`
	State          TransferState `json:"state"`
	StartTime      time.Time     `json:"start_time"`
	LastActive     time.Time     `json:"last_active"`
	ChunkSize      int           `json:"chunk_size"`
	Error          string        `json:"error,omitempty"`
}

// ResumableTransfer orchestrates pausable, resumable file copying between VFS instances.
type ResumableTransfer struct {
	id          string
	srcFS       vfs.FileSystem
	srcPath     string
	dstFS       vfs.FileSystem
	dstPath     string
	checkpoint  Checkpoint
	chunkSize   int
	pauseChan   chan struct{}
	resumeChan  chan struct{}
	cancelCtx   context.Context
	cancelFunc  context.CancelFunc
	onProgress  func(cp Checkpoint)
	mu          sync.RWMutex
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

	srcReader, err := t.srcFS.Open(t.srcPath)
	if err != nil {
		t.fail(err)
		return fmt.Errorf("open src failed: %w", err)
	}
	defer srcReader.Close()

	// Skip bytes up to current offset if resuming
	currentOffset := t.checkpoint.Offset
	if currentOffset > 0 {
		discardBuf := make([]byte, 32*1024)
		var skipped int64
		for skipped < currentOffset {
			toRead := currentOffset - skipped
			if toRead > int64(len(discardBuf)) {
				toRead = int64(len(discardBuf))
			}
			n, err := srcReader.Read(discardBuf[:toRead])
			skipped += int64(n)
			if err != nil {
				t.fail(err)
				return fmt.Errorf("failed seeking to offset %d: %w", currentOffset, err)
			}
		}
	}

	// Buffer for copying
	buf := make([]byte, t.chunkSize)
	var totalAccumulated []byte

	// If resuming and file exists, load existing data
	if currentOffset > 0 && t.dstFS.Exists(t.dstPath) {
		existingData, _ := t.dstFS.Read(t.dstPath)
		if int64(len(existingData)) >= currentOffset {
			totalAccumulated = append(totalAccumulated, existingData[:currentOffset]...)
		}
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
			totalAccumulated = append(totalAccumulated, buf[:n]...)

			// Write accumulated chunks to destination
			if err := t.dstFS.Write(t.dstPath, totalAccumulated); err != nil {
				t.fail(err)
				return fmt.Errorf("write error: %w", err)
			}

			t.mu.Lock()
			t.checkpoint.Offset += int64(n)
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

// Resume resumes a paused transfer.
func (t *ResumableTransfer) Resume() {
	t.mu.Lock()
	if t.checkpoint.State == StatePaused {
		t.checkpoint.State = StateRunning
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
	mu        sync.RWMutex
}

// NewResumableManager creates a transfer manager.
func NewResumableManager(db *storage.DB) *ResumableManager {
	return &ResumableManager{
		transfers: make(map[string]*ResumableTransfer),
		db:        db,
	}
}

// Register registers a transfer session.
func (m *ResumableManager) Register(xfer *ResumableTransfer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transfers[xfer.id] = xfer
}

// Get retrieves an active transfer.
func (m *ResumableManager) Get(id string) (*ResumableTransfer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	xfer, ok := m.transfers[id]
	return xfer, ok
}
