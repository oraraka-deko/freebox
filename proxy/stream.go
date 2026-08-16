package proxy

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"sync"
)

// StreamProxyConfig configures chunking and caching limits for StreamProxy.
type StreamProxyConfig struct {
	ChunkSize      int   // Size of each chunk in bytes (default: 64 KB)
	MaxMemoryChunks int   // Maximum chunks kept in memory (default: 64)
}

// DefaultStreamProxyConfig returns default configuration.
func DefaultStreamProxyConfig() StreamProxyConfig {
	return StreamProxyConfig{
		ChunkSize:      64 * 1024,
		MaxMemoryChunks: 64, // 4MB RAM cache limit
	}
}

type chunk struct {
	index int64
	data  []byte
	dirty bool
}

// StreamProxy provides a high-performance, chunk-cached, random-access stream proxy.
// Files larger than available RAM can be sought, read, and randomly written without
// loading the full file.
type StreamProxy struct {
	source    Source
	config    StreamProxyConfig
	mu        sync.Mutex
	offset    int64
	size      int64
	chunks    map[int64]*list.Element
	lruList   *list.List
	closed    bool
}

// NewStreamProxy creates a new StreamProxy wrapping the underlying Source.
func NewStreamProxy(src Source, cfg StreamProxyConfig) *StreamProxy {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 64 * 1024
	}
	if cfg.MaxMemoryChunks <= 0 {
		cfg.MaxMemoryChunks = 64
	}

	return &StreamProxy{
		source:  src,
		config:  cfg,
		size:    src.Size(),
		chunks:  make(map[int64]*list.Element),
		lruList: list.New(),
	}
}

// Size returns the total size of the stream.
func (s *StreamProxy) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

func (s *StreamProxy) getChunk(chunkIdx int64, allocateOnMissing bool) (*chunk, error) {
	if el, ok := s.chunks[chunkIdx]; ok {
		s.lruList.MoveToFront(el)
		return el.Value.(*chunk), nil
	}

	// Fetch chunk from underlying source
	chunkOffset := chunkIdx * int64(s.config.ChunkSize)
	buf := make([]byte, s.config.ChunkSize)
	n, err := s.source.ReadAt(buf, chunkOffset)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !allocateOnMissing {
		return nil, fmt.Errorf("failed to read chunk %d: %w", chunkIdx, err)
	}

	c := &chunk{
		index: chunkIdx,
		data:  buf[:n],
		dirty: false,
	}

	// If missing & allocate
	if n < s.config.ChunkSize && allocateOnMissing {
		expanded := make([]byte, s.config.ChunkSize)
		copy(expanded, c.data)
		c.data = expanded
	}

	// Evict if cache exceeds max
	for s.lruList.Len() >= s.config.MaxMemoryChunks {
		if err := s.evictOldest(); err != nil {
			return nil, err
		}
	}

	el := s.lruList.PushFront(c)
	s.chunks[chunkIdx] = el
	return c, nil
}

func (s *StreamProxy) evictOldest() error {
	oldest := s.lruList.Back()
	if oldest == nil {
		return nil
	}
	c := oldest.Value.(*chunk)
	if c.dirty {
		if err := s.flushChunk(c); err != nil {
			return err
		}
	}
	s.lruList.Remove(oldest)
	delete(s.chunks, c.index)
	return nil
}

func (s *StreamProxy) flushChunk(c *chunk) error {
	if !c.dirty {
		return nil
	}
	offset := c.index * int64(s.config.ChunkSize)
	if _, err := s.source.WriteAt(c.data, offset); err != nil {
		return fmt.Errorf("failed to write chunk %d to source: %w", c.index, err)
	}
	c.dirty = false
	return nil
}

// ReadAt reads len(p) bytes from the stream starting at byte offset off.
func (s *StreamProxy) ReadAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrClosed
	}
	if off < 0 {
		return 0, ErrOutOfBounds
	}
	if off >= s.size {
		return 0, io.EOF
	}

	bytesToRead := len(p)
	if off+int64(bytesToRead) > s.size {
		bytesToRead = int(s.size - off)
	}

	totalRead := 0
	currOffset := off

	for totalRead < bytesToRead {
		chunkIdx := currOffset / int64(s.config.ChunkSize)
		offsetInChunk := currOffset % int64(s.config.ChunkSize)

		c, err := s.getChunk(chunkIdx, false)
		if err != nil && totalRead == 0 {
			return 0, err
		}
		if c == nil || int(offsetInChunk) >= len(c.data) {
			break
		}

		availableInChunk := len(c.data) - int(offsetInChunk)
		needed := bytesToRead - totalRead
		copySize := availableInChunk
		if needed < copySize {
			copySize = needed
		}

		copy(p[totalRead:totalRead+copySize], c.data[offsetInChunk:int(offsetInChunk)+copySize])
		totalRead += copySize
		currOffset += int64(copySize)
	}

	if totalRead < len(p) {
		return totalRead, io.EOF
	}
	return totalRead, nil
}

// WriteAt writes len(p) bytes to the stream starting at byte offset off.
func (s *StreamProxy) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrClosed
	}
	if off < 0 {
		return 0, ErrOutOfBounds
	}

	totalWritten := 0
	currOffset := off

	for totalWritten < len(p) {
		chunkIdx := currOffset / int64(s.config.ChunkSize)
		offsetInChunk := int(currOffset % int64(s.config.ChunkSize))

		c, err := s.getChunk(chunkIdx, true)
		if err != nil {
			return totalWritten, err
		}

		spaceInChunk := s.config.ChunkSize - offsetInChunk
		needed := len(p) - totalWritten
		writeSize := spaceInChunk
		if needed < writeSize {
			writeSize = needed
		}

		// Ensure chunk slice has capacity
		if offsetInChunk+writeSize > len(c.data) {
			newLen := offsetInChunk + writeSize
			if newLen > len(c.data) {
				if cap(c.data) >= newLen {
					c.data = c.data[:newLen]
				} else {
					expanded := make([]byte, newLen, s.config.ChunkSize)
					copy(expanded, c.data)
					c.data = expanded
				}
			}
		}

		copy(c.data[offsetInChunk:offsetInChunk+writeSize], p[totalWritten:totalWritten+writeSize])
		c.dirty = true

		totalWritten += writeSize
		currOffset += int64(writeSize)
	}

	if off+int64(totalWritten) > s.size {
		s.size = off + int64(totalWritten)
	}

	return totalWritten, nil
}

// Seek sets the offset for the next Read or Write on stream.
func (s *StreamProxy) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, ErrClosed
	}

	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = s.offset + offset
	case io.SeekEnd:
		newOffset = s.size + offset
	default:
		return 0, errors.New("invalid seek whence")
	}

	if newOffset < 0 {
		return 0, ErrOutOfBounds
	}
	s.offset = newOffset
	return s.offset, nil
}

// Read reads up to len(p) bytes into p from current seek position.
func (s *StreamProxy) Read(p []byte) (int, error) {
	s.mu.Lock()
	currOff := s.offset
	s.mu.Unlock()

	n, err := s.ReadAt(p, currOff)
	s.mu.Lock()
	s.offset += int64(n)
	s.mu.Unlock()
	return n, err
}

// Write writes len(p) bytes from p at current seek position.
func (s *StreamProxy) Write(p []byte) (int, error) {
	s.mu.Lock()
	currOff := s.offset
	s.mu.Unlock()

	n, err := s.WriteAt(p, currOff)
	s.mu.Lock()
	s.offset += int64(n)
	s.mu.Unlock()
	return n, err
}

// Flush writes all dirty in-memory chunks back to the underlying Source.
func (s *StreamProxy) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, el := range s.chunks {
		c := el.Value.(*chunk)
		if c.dirty {
			if err := s.flushChunk(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close flushes all dirty chunks and closes the stream and underlying Source.
func (s *StreamProxy) Close() error {
	if err := s.Flush(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.source.Close()
}

var (
	_ io.ReadSeeker = (*StreamProxy)(nil)
	_ io.ReaderAt   = (*StreamProxy)(nil)
	_ io.WriterAt   = (*StreamProxy)(nil)
	_ io.Writer     = (*StreamProxy)(nil)
	_ io.Closer     = (*StreamProxy)(nil)
)
