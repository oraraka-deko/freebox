package vfs

import (
	"context"
	"errors"
	"io"
)

// ErrUnsupported is returned when a backend does not advertise the requested
// operation. Callers must not emulate random access by reading a stream prefix.
var (
	ErrUnsupported   = errors.New("vfs operation is not supported by this backend")
	ErrInvalidOffset = errors.New("invalid file offset")
)

// Capability describes an operation a FileSystem can perform natively.
type Capability uint64

const (
	CapStreamRead Capability = 1 << iota
	CapRangeRead
	CapSeek
	CapStreamWrite
	CapRandomWrite
	CapTruncate
	CapAtomicRename
	CapResumableRead
	CapResumableWrite
)

// Capabilities is the native operation set for a filesystem.
type Capabilities Capability

func (c Capabilities) Has(required Capability) bool { return Capability(c)&required == required }

// OpenOptions controls how a VFS file handle is opened.
type OpenOptions struct {
	Read      bool
	Write     bool
	Create    bool
	Truncate  bool
	Append    bool
	Exclusive bool
}

// ReadOnly opens a streaming reader.
func ReadOnly() OpenOptions { return OpenOptions{Read: true} }

// WriteOnly creates or truncates a streaming writer.
func WriteOnly() OpenOptions { return OpenOptions{Write: true, Create: true, Truncate: true} }

// File is the common streaming file contract. Random access and mutation are
// deliberate optional extensions below and must be checked against Capabilities.
type File interface {
	io.Reader
	io.Closer
}

// ReadAtFile is implemented only by files with native offset reads.
type ReadAtFile interface {
	File
	io.ReaderAt
	io.Seeker
}

// WriteAtFile is implemented only by files with native offset writes.
type WriteAtFile interface {
	File
	io.Writer
	io.WriterAt
	Truncate(int64) error
	Sync() error
}

// OpenFileSystem is the new context-aware file access contract. It is embedded
// by FileSystem during the migration from the legacy convenience methods.
type OpenFileSystem interface {
	Capabilities() Capabilities
	OpenFile(ctx context.Context, p string, options OpenOptions) (File, error)
}
