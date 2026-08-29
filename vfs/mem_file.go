package vfs

import (
	"errors"
	"io"
	"time"
)

// memFile supplies native offset operations over a MemFS node. It deliberately
// updates the node in bounded writes rather than retaining a full-file buffer.
type memFile struct {
	fs       *MemFS
	path     string
	offset   int64
	readable bool
	writable bool
	closed   bool
}

func (f *memFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, errors.New("read from closed file")
	}
	if !f.readable {
		return 0, ErrUnsupported
	}
	if off < 0 {
		return 0, ErrInvalidOffset
	}
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()
	node, ok := f.fs.nodes[f.path]
	if !ok {
		return 0, ErrNotFound
	}
	if off >= int64(len(node.data)) {
		return 0, io.EOF
	}
	n := copy(p, node.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) Seek(offset int64, whence int) (int64, error) {
	f.fs.mu.RLock()
	node, ok := f.fs.nodes[f.path]
	size := int64(0)
	if ok {
		size = int64(len(node.data))
	}
	f.fs.mu.RUnlock()
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = f.offset + offset
	case io.SeekEnd:
		next = size + offset
	default:
		return 0, errors.New("invalid seek whence")
	}
	if next < 0 {
		return 0, ErrInvalidOffset
	}
	f.offset = next
	return next, nil
}

func (f *memFile) Write(p []byte) (int, error) {
	n, err := f.WriteAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}

func (f *memFile) WriteAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, errors.New("write to closed file")
	}
	if !f.writable {
		return 0, ErrUnsupported
	}
	if off < 0 {
		return 0, ErrInvalidOffset
	}
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	node, ok := f.fs.nodes[f.path]
	if !ok {
		return 0, ErrNotFound
	}
	required := off + int64(len(p))
	if required > int64(len(node.data)) {
		expanded := make([]byte, required)
		copy(expanded, node.data)
		node.data = expanded
	}
	n := copy(node.data[off:], p)
	node.modTime = time.Now()
	return n, nil
}

func (f *memFile) Truncate(size int64) error {
	if !f.writable {
		return ErrUnsupported
	}
	if size < 0 {
		return ErrInvalidOffset
	}
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	node, ok := f.fs.nodes[f.path]
	if !ok {
		return ErrNotFound
	}
	if size <= int64(len(node.data)) {
		node.data = node.data[:size]
	} else {
		expanded := make([]byte, size)
		copy(expanded, node.data)
		node.data = expanded
	}
	node.modTime = time.Now()
	return nil
}

func (f *memFile) Sync() error { return nil }

func (f *memFile) Close() error {
	f.closed = true
	return nil
}

var _ ReadAtFile = (*memFile)(nil)
var _ WriteAtFile = (*memFile)(nil)
