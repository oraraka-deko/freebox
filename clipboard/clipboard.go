package clipboard

import (
	"errors"
	"path"
	"sync"

	"freebox/vfs"
)

// OperationType identifies the staged operation in the clipboard.
type OperationType string

const (
	// OpCopy indicates items will be copied to destination upon paste.
	OpCopy OperationType = "COPY"
	// OpCut indicates items will be moved to destination upon paste.
	OpCut OperationType = "CUT"
	// OpDelete indicates items are staged for deletion upon paste.
	OpDelete OperationType = "DELETE"
	// OpCreate indicates new item is staged for creation upon paste.
	OpCreate OperationType = "CREATE"
)

// Item represents a single entry in the clipboard.
type Item struct {
	Op       OperationType
	SourceFS vfs.FileSystem
	Path     string
	Data     []byte
	IsDir    bool
	Metadata map[string]string
}

// Clipboard manages staged file and task operations before execution.
type Clipboard struct {
	items     []Item
	mu        sync.RWMutex
	listeners []func(items []Item)
}

// NewClipboard creates a new clipboard instance.
func NewClipboard() *Clipboard {
	return &Clipboard{
		items: make([]Item, 0),
	}
}

// Copy stages one or more paths from srcFS for copying.
func (c *Clipboard) Copy(srcFS vfs.FileSystem, paths ...string) {
	c.mu.Lock()
	c.items = make([]Item, 0, len(paths))
	for _, p := range paths {
		c.items = append(c.items, Item{
			Op:       OpCopy,
			SourceFS: srcFS,
			Path:     vfs.NormalizePath(p),
		})
	}
	snapshot := c.cloneItems()
	listeners := c.listeners
	c.mu.Unlock()

	c.notify(listeners, snapshot)
}

// Cut stages one or more paths from srcFS for moving (cut & paste).
func (c *Clipboard) Cut(srcFS vfs.FileSystem, paths ...string) {
	c.mu.Lock()
	c.items = make([]Item, 0, len(paths))
	for _, p := range paths {
		c.items = append(c.items, Item{
			Op:       OpCut,
			SourceFS: srcFS,
			Path:     vfs.NormalizePath(p),
		})
	}
	snapshot := c.cloneItems()
	listeners := c.listeners
	c.mu.Unlock()

	c.notify(listeners, snapshot)
}

// StageCreate stages a new file or directory creation.
func (c *Clipboard) StageCreate(targetPath string, data []byte, isDir bool) {
	c.mu.Lock()
	c.items = []Item{
		{
			Op:    OpCreate,
			Path:  vfs.NormalizePath(targetPath),
			Data:  data,
			IsDir: isDir,
		},
	}
	snapshot := c.cloneItems()
	listeners := c.listeners
	c.mu.Unlock()

	c.notify(listeners, snapshot)
}

// StageDelete stages one or more files for deletion.
func (c *Clipboard) StageDelete(srcFS vfs.FileSystem, paths ...string) {
	c.mu.Lock()
	c.items = make([]Item, 0, len(paths))
	for _, p := range paths {
		c.items = append(c.items, Item{
			Op:       OpDelete,
			SourceFS: srcFS,
			Path:     vfs.NormalizePath(p),
		})
	}
	snapshot := c.cloneItems()
	listeners := c.listeners
	c.mu.Unlock()

	c.notify(listeners, snapshot)
}

// Items returns a copy of current staged items.
func (c *Clipboard) Items() []Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cloneItems()
}

// Count returns the number of staged items.
func (c *Clipboard) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// IsEmpty returns true if clipboard contains no items.
func (c *Clipboard) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items) == 0
}

// Clear empties the clipboard.
func (c *Clipboard) Clear() {
	c.mu.Lock()
	c.items = make([]Item, 0)
	snapshot := c.cloneItems()
	listeners := c.listeners
	c.mu.Unlock()

	c.notify(listeners, snapshot)
}

// OnChange registers a listener callback called whenever clipboard items change.
func (c *Clipboard) OnChange(fn func(items []Item)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

func (c *Clipboard) cloneItems() []Item {
	res := make([]Item, len(c.items))
	copy(res, c.items)
	return res
}

func (c *Clipboard) notify(listeners []func(items []Item), items []Item) {
	for _, fn := range listeners {
		if fn != nil {
			fn(items)
		}
	}
}

// PlannedOperation represents the destination resolved operation from pasting.
type PlannedOperation struct {
	Op      OperationType
	SrcFS   vfs.FileSystem
	SrcPath string
	DstFS   vfs.FileSystem
	DstPath string
	Data    []byte
	IsDir   bool
}

// PlanPaste computes the list of operations that would be executed at dstDir on dstFS.
func (c *Clipboard) PlanPaste(dstFS vfs.FileSystem, dstDir string) ([]PlannedOperation, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.items) == 0 {
		return nil, errors.New("clipboard is empty")
	}

	dstDir = vfs.NormalizePath(dstDir)
	plans := make([]PlannedOperation, 0, len(c.items))

	for _, item := range c.items {
		baseName := path.Base(item.Path)
		targetPath := vfs.NormalizePath(dstDir + "/" + baseName)

		plans = append(plans, PlannedOperation{
			Op:      item.Op,
			SrcFS:   item.SourceFS,
			SrcPath: item.Path,
			DstFS:   dstFS,
			DstPath: targetPath,
			Data:    item.Data,
			IsDir:   item.IsDir,
		})
	}

	return plans, nil
}
