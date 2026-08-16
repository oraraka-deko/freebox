package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"freebox/vfs"
)

// Common proxy errors.
var (
	ErrOutOfBounds = errors.New("offset out of bounds")
	ErrReadOnly    = errors.New("source is read-only")
	ErrClosed      = errors.New("source is closed")
)

// Source represents an underlying storage backend that supports random access.
type Source interface {
	// Size returns the current size of the data source in bytes.
	Size() int64
	// ReadAt reads len(p) bytes from the source starting at byte offset off.
	ReadAt(p []byte, off int64) (n int, err error)
	// WriteAt writes len(p) bytes to the source starting at byte offset off.
	WriteAt(p []byte, off int64) (n int, err error)
	// Truncate resizes the source to size bytes.
	Truncate(size int64) error
	// Close releases any resources held by the source.
	Close() error
}

// --- MemSource: In-Memory Source (useful for testing and buffer cache) ---

type MemSource struct {
	mu   sync.RWMutex
	data []byte
}

// NewMemSource creates a memory-backed source initialized with data.
func NewMemSource(data []byte) *MemSource {
	buf := make([]byte, len(data))
	copy(buf, data)
	return &MemSource{data: buf}
}

func (m *MemSource) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return int64(len(m.data))
}

func (m *MemSource) ReadAt(p []byte, off int64) (n int, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if off < 0 {
		return 0, ErrOutOfBounds
	}
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n = copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *MemSource) WriteAt(p []byte, off int64) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off < 0 {
		return 0, ErrOutOfBounds
	}
	required := int(off) + len(p)
	if required > len(m.data) {
		newData := make([]byte, required)
		copy(newData, m.data)
		m.data = newData
	}
	n = copy(m.data[off:], p)
	return n, nil
}

func (m *MemSource) Truncate(size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size < 0 {
		return ErrOutOfBounds
	}
	if int(size) <= len(m.data) {
		m.data = m.data[:size]
	} else {
		newData := make([]byte, size)
		copy(newData, m.data)
		m.data = newData
	}
	return nil
}

func (m *MemSource) Close() error {
	return nil
}

// --- VFSSource: Virtual File System Source ---

type VFSSource struct {
	vfs      vfs.FileSystem
	path     string
	mu       sync.RWMutex
	size     int64
	readOnly bool
}

// NewVFSSource creates a Source backed by a VFS file.
func NewVFSSource(fs vfs.FileSystem, filePath string, readOnly bool) (*VFSSource, error) {
	filePath = vfs.NormalizePath(filePath)
	var size int64
	info, err := fs.Stat(filePath)
	if err == nil {
		size = info.Size
	} else if !errors.Is(err, vfs.ErrNotFound) {
		return nil, err
	}

	return &VFSSource{
		vfs:      fs,
		path:     filePath,
		size:     size,
		readOnly: readOnly,
	}, nil
}

func (v *VFSSource) Size() int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.size
}

func (v *VFSSource) ReadAt(p []byte, off int64) (n int, err error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if off < 0 {
		return 0, ErrOutOfBounds
	}
	if off >= v.size {
		return 0, io.EOF
	}

	rc, err := v.vfs.Open(v.path)
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	remain := v.size - off
	toRead := p
	if int64(len(toRead)) > remain {
		toRead = toRead[:remain]
	}

	// If seeker available
	if rs, ok := rc.(io.ReadSeeker); ok {
		if _, err := rs.Seek(off, io.SeekStart); err != nil {
			return 0, err
		}
		return io.ReadFull(rs, toRead)
	}

	// Stream discard up to offset
	if _, err := io.CopyN(io.Discard, rc, off); err != nil {
		return 0, err
	}
	return io.ReadFull(rc, toRead)
}

func (v *VFSSource) WriteAt(p []byte, off int64) (n int, err error) {
	if v.readOnly {
		return 0, ErrReadOnly
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	// Read existing full data or stream if needed
	var current []byte
	if v.vfs.Exists(v.path) {
		data, err := v.vfs.Read(v.path)
		if err == nil {
			current = data
		}
	}

	required := int(off) + len(p)
	if required > len(current) {
		newData := make([]byte, required)
		copy(newData, current)
		current = newData
	}

	n = copy(current[off:], p)
	if err := v.vfs.Write(v.path, current); err != nil {
		return 0, err
	}
	v.size = int64(len(current))
	return n, nil
}

func (v *VFSSource) Truncate(size int64) error {
	if v.readOnly {
		return ErrReadOnly
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	var current []byte
	if v.vfs.Exists(v.path) {
		data, _ := v.vfs.Read(v.path)
		current = data
	}

	if int(size) <= len(current) {
		current = current[:size]
	} else {
		newData := make([]byte, size)
		copy(newData, current)
		current = newData
	}

	if err := v.vfs.Write(v.path, current); err != nil {
		return err
	}
	v.size = size
	return nil
}

func (v *VFSSource) Close() error {
	return nil
}

// --- HTTPSource: HTTP Byte-Range Remote Source ---

type HTTPSource struct {
	url        string
	client     *http.Client
	size       int64
	acceptsRange bool
	mu         sync.RWMutex
}

// NewHTTPSource creates an HTTP byte-range capable remote source.
func NewHTTPSource(url string, client *http.Client) (*HTTPSource, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to probe http source: %w", err)
	}
	defer resp.Body.Close()

	var size int64
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		size, _ = strconv.ParseInt(cl, 10, 64)
	}
	accepts := resp.Header.Get("Accept-Ranges") == "bytes" || resp.StatusCode == http.StatusOK

	return &HTTPSource{
		url:          url,
		client:       client,
		size:         size,
		acceptsRange: accepts,
	}, nil
}

func (h *HTTPSource) Size() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.size
}

func (h *HTTPSource) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, ErrOutOfBounds
	}
	if h.size > 0 && off >= h.size {
		return 0, io.EOF
	}

	end := off + int64(len(p)) - 1
	if h.size > 0 && end >= h.size {
		end = h.size - 1
	}

	req, err := http.NewRequest(http.MethodGet, h.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))

	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http range request returned status %d", resp.StatusCode)
	}

	// If server returned 200 OK instead of 206 Partial Content, skip prefix
	var reader io.Reader = resp.Body
	if resp.StatusCode == http.StatusOK && off > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, off); err != nil {
			return 0, err
		}
	}

	readBytes, err := io.ReadFull(reader, p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return readBytes, err
}

func (h *HTTPSource) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, ErrReadOnly
}

func (h *HTTPSource) Truncate(size int64) error {
	return ErrReadOnly
}

func (h *HTTPSource) Close() error {
	return nil
}

var (
	_ Source = (*MemSource)(nil)
	_ Source = (*VFSSource)(nil)
	_ Source = (*HTTPSource)(nil)
	_ = strings.Split
)
