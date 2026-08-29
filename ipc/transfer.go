package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"freebox/internal/bufferpool"
	"freebox/vfs"
)

// pendingTTL is how long a transfer.open token remains redeemable before
// expiring if the client never opens the raw data connection.
const pendingTTL = 30 * time.Second

// progressInterval controls how often transfer.progress notifications are sent.
const progressInterval = 250 * time.Millisecond

type pendingTransfer struct {
	id     string
	mode   string // "read" or "write"
	fs     vfs.FileSystem
	path   string
	offset int64
	owner  *Conn
	timer  *time.Timer
}

type activeTransfer struct {
	cancel context.CancelFunc
}

type transferManager struct {
	mu      sync.Mutex
	pending map[string]*pendingTransfer // keyed by hex token
	active  map[string]*activeTransfer  // keyed by transferId
	idSeq   uint64
}

func newTransferManager() *transferManager {
	return &transferManager{
		pending: make(map[string]*pendingTransfer),
		active:  make(map[string]*activeTransfer),
	}
}

func (tm *transferManager) nextID() string {
	n := atomic.AddUint64(&tm.idSeq, 1)
	return fmt.Sprintf("t-%d-%d", time.Now().UnixNano(), n)
}

// open registers a pending transfer and returns its id and one-time token hex string.
func (tm *transferManager) open(owner *Conn, mode string, fs vfs.FileSystem, path string, offset int64) (transferID, token string, err error) {
	if mode != "read" && mode != "write" {
		return "", "", errors.New("mode must be \"read\" or \"write\"")
	}
	raw := make([]byte, TransferTokenSize)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(raw)
	transferID = tm.nextID()

	pt := &pendingTransfer{id: transferID, mode: mode, fs: fs, path: path, offset: offset, owner: owner}

	tm.mu.Lock()
	tm.pending[token] = pt
	tm.mu.Unlock()

	pt.timer = time.AfterFunc(pendingTTL, func() {
		tm.mu.Lock()
		delete(tm.pending, token)
		tm.mu.Unlock()
	})

	return transferID, token, nil
}

// close cancels an in-flight or pending transfer by id.
func (tm *transferManager) close(transferID string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if at, ok := tm.active[transferID]; ok {
		at.cancel()
		delete(tm.active, transferID)
		return true
	}
	for token, pt := range tm.pending {
		if pt.id == transferID {
			pt.timer.Stop()
			delete(tm.pending, token)
			return true
		}
	}
	return false
}

// serve handles a freshly accepted raw transfer connection after its token
// handshake has been read, streaming file bytes to/from nc.
func (tm *transferManager) serve(ctx context.Context, nc net.Conn, tokenRaw []byte) {
	token := hex.EncodeToString(tokenRaw)

	tm.mu.Lock()
	pt, ok := tm.pending[token]
	if ok {
		pt.timer.Stop()
		delete(tm.pending, token)
	}
	tm.mu.Unlock()

	if !ok {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tm.mu.Lock()
	tm.active[pt.id] = &activeTransfer{cancel: cancel}
	tm.mu.Unlock()
	defer func() {
		tm.mu.Lock()
		delete(tm.active, pt.id)
		tm.mu.Unlock()
	}()

	var (
		n   int64
		err error
	)
	if pt.mode == "read" {
		n, err = tm.serveRead(ctx, pt, nc)
	} else {
		n, err = tm.serveWrite(ctx, pt, nc)
	}

	if pt.owner != nil {
		if err != nil {
			_ = pt.owner.Notify("transfer.error", map[string]any{
				"transferId": pt.id, "bytes": n, "error": err.Error(),
			})
		} else {
			_ = pt.owner.Notify("transfer.complete", map[string]any{
				"transferId": pt.id, "bytes": n,
			})
		}
	}
}

func (tm *transferManager) serveRead(ctx context.Context, pt *pendingTransfer, nc net.Conn) (int64, error) {
	r, err := pt.fs.Open(pt.path)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	if pt.offset > 0 {
		if seeker, ok := r.(io.Seeker); ok {
			if _, err := seeker.Seek(pt.offset, io.SeekStart); err != nil {
				return 0, err
			}
		} else if _, err := io.CopyN(io.Discard, r, pt.offset); err != nil {
			return 0, err
		}
	}

	return copyWithProgress(ctx, pt, nc, r)
}

func (tm *transferManager) serveWrite(ctx context.Context, pt *pendingTransfer, nc net.Conn) (int64, error) {
	w, err := pt.fs.Create(pt.path)
	if err != nil {
		return 0, err
	}
	defer w.Close()

	return copyWithProgress(ctx, pt, w, nc)
}

// copyWithProgress copies from src to dst using a pooled buffer, pushing
// periodic transfer.progress notifications to the owning control connection.
func copyWithProgress(ctx context.Context, pt *pendingTransfer, dst io.Writer, src io.Reader) (int64, error) {
	buf := bufferpool.Acquire(bufferpool.LargeSize)
	defer bufferpool.Release(buf)

	var total int64
	lastNotify := time.Now()

	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}

		nr, rerr := src.Read(buf)
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			total += int64(nw)
			if werr != nil {
				return total, werr
			}
			if nw != nr {
				return total, io.ErrShortWrite
			}
			if time.Since(lastNotify) >= progressInterval && pt.owner != nil {
				lastNotify = time.Now()
				_ = pt.owner.Notify("transfer.progress", map[string]any{
					"transferId": pt.id, "bytes": total,
				})
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return total, nil
			}
			return total, rerr
		}
	}
}
