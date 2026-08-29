// Package bufferpool provides bounded reusable copy buffers for network paths.
package bufferpool

import (
	"io"
	"sync"
)

const (
	SmallSize = 64 * 1024
	LargeSize = 1024 * 1024
)

var small = sync.Pool{New: func() any { return make([]byte, SmallSize) }}
var large = sync.Pool{New: func() any { return make([]byte, LargeSize) }}

// Acquire returns a reusable buffer of at least size. Only standard buffer
// classes are pooled; exceptional sizes are allocated by the caller and can be
// returned through Release safely.
func Acquire(size int) []byte {
	if size <= SmallSize {
		return small.Get().([]byte)
	}
	if size <= LargeSize {
		return large.Get().([]byte)
	}
	return make([]byte, size)
}

// Release returns a standard-sized buffer to its pool. Callers must not retain
// or expose the slice after releasing it.
func Release(buf []byte) {
	switch cap(buf) {
	case SmallSize:
		small.Put(buf[:SmallSize])
	case LargeSize:
		large.Put(buf[:LargeSize])
	}
}

// Copy streams from src to dst using a 64 KiB recycled buffer.
func Copy(dst io.Writer, src io.Reader) (int64, error) {
	buf := Acquire(SmallSize)
	defer Release(buf)
	return io.CopyBuffer(dst, src, buf)
}

// CopyN streams n bytes from src to dst using a 64 KiB recycled buffer.
func CopyN(dst io.Writer, src io.Reader, n int64) (int64, error) {
	buf := Acquire(SmallSize)
	defer Release(buf)
	return io.CopyBuffer(dst, io.LimitReader(src, n), buf)
}
