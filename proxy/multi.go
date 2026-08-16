package proxy

import (
	"fmt"
	"io"
	"sync"
)

// SpannedPart represents one part of a multi-file composite stream.
type SpannedPart struct {
	Source   Source
	Offset   int64 // Virtual starting offset
	Size     int64 // Part size
}

// MultiSource composites multiple Sources into a continuous seamless stream.
type MultiSource struct {
	parts []SpannedPart
	size  int64
	mu    sync.RWMutex
}

// NewMultiSource creates a composite multi-file source from an array of Sources.
func NewMultiSource(sources ...Source) *MultiSource {
	m := &MultiSource{}
	var currOffset int64

	for _, src := range sources {
		partSize := src.Size()
		m.parts = append(m.parts, SpannedPart{
			Source: src,
			Offset: currOffset,
			Size:   partSize,
		})
		currOffset += partSize
	}
	m.size = currOffset
	return m
}

func (m *MultiSource) Size() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}

func (m *MultiSource) ReadAt(p []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if off < 0 {
		return 0, ErrOutOfBounds
	}
	if off >= m.size {
		return 0, io.EOF
	}

	totalRead := 0
	currOff := off

	for totalRead < len(p) && currOff < m.size {
		// Find matching part
		partIdx := -1
		for i, part := range m.parts {
			if currOff >= part.Offset && currOff < part.Offset+part.Size {
				partIdx = i
				break
			}
		}

		if partIdx == -1 {
			break
		}

		part := m.parts[partIdx]
		partInternalOff := currOff - part.Offset
		partRemaining := part.Size - partInternalOff
		needed := int64(len(p) - totalRead)

		toRead := needed
		if partRemaining < toRead {
			toRead = partRemaining
		}

		n, err := part.Source.ReadAt(p[totalRead:totalRead+int(toRead)], partInternalOff)
		if n > 0 {
			totalRead += n
			currOff += int64(n)
		}
		if err != nil && err != io.EOF {
			return totalRead, err
		}
		if n == 0 {
			break
		}
	}

	if totalRead < len(p) {
		return totalRead, io.EOF
	}
	return totalRead, nil
}

func (m *MultiSource) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if off < 0 {
		return 0, ErrOutOfBounds
	}

	totalWritten := 0
	currOff := off

	for totalWritten < len(p) {
		partIdx := -1
		for i, part := range m.parts {
			if currOff >= part.Offset && currOff < part.Offset+part.Size {
				partIdx = i
				break
			}
		}

		if partIdx == -1 {
			return totalWritten, fmt.Errorf("write offset %d exceeds multi-source parts boundary", currOff)
		}

		part := m.parts[partIdx]
		partInternalOff := currOff - part.Offset
		partSpace := part.Size - partInternalOff
		needed := int64(len(p) - totalWritten)

		toWrite := needed
		if partSpace < toWrite {
			toWrite = partSpace
		}

		n, err := part.Source.WriteAt(p[totalWritten:totalWritten+int(toWrite)], partInternalOff)
		if n > 0 {
			totalWritten += n
			currOff += int64(n)
		}
		if err != nil {
			return totalWritten, err
		}
	}

	return totalWritten, nil
}

func (m *MultiSource) Truncate(size int64) error {
	return ErrReadOnly
}

func (m *MultiSource) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for _, part := range m.parts {
		if err := part.Source.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// NewMultiStreamProxy creates a high-performance cached StreamProxy over multiple composite sources.
func NewMultiStreamProxy(cfg StreamProxyConfig, sources ...Source) *StreamProxy {
	multi := NewMultiSource(sources...)
	return NewStreamProxy(multi, cfg)
}

var (
	_ Source = (*MultiSource)(nil)
)
