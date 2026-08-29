package storage

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CachedFileInfo represents cached metadata for a virtual file or directory.
type CachedFileInfo struct {
	Path          string            `json:"path"`
	Name          string            `json:"name"`
	Size          int64             `json:"size"`
	IsDir         bool              `json:"is_dir"`
	ModTime       time.Time         `json:"mod_time"`
	MIMEType      string            `json:"mime_type,omitempty"`
	HeaderSnippet []byte            `json:"header_snippet,omitempty"` // First 512B - 4KB of file
	Metadata      map[string]string `json:"metadata,omitempty"`
	CachedAt      time.Time         `json:"cached_at"`
	ExpiresAt     time.Time         `json:"expires_at,omitempty"`
}

// TreeCache provides an in-memory, thread-safe cache and index for directory trees
// and tiny file headers. It enables instant UI rendering without repeated remote queries,
// and preserves the UI structure during transient network disconnects.
type TreeCache struct {
	mu           sync.RWMutex
	dirEntries   map[string][]CachedFileInfo // key: "mount:dirPath"
	nodeMap      map[string]CachedFileInfo   // key: "mount:filePath"
	headerMap    map[string][]byte           // key: "mount:filePath" -> tiny header snippet (512B-4KB)
	mimeMap      map[string]string           // key: "mount:filePath" -> MIME type
	defaultTTL   time.Duration
	maxHeaderLen int
}

// NewTreeCache creates a new TreeCache instance.
func NewTreeCache(defaultTTL time.Duration) *TreeCache {
	if defaultTTL <= 0 {
		defaultTTL = 10 * time.Minute
	}
	return &TreeCache{
		dirEntries:   make(map[string][]CachedFileInfo),
		nodeMap:      make(map[string]CachedFileInfo),
		headerMap:    make(map[string][]byte),
		mimeMap:      make(map[string]string),
		defaultTTL:   defaultTTL,
		maxHeaderLen: 4096, // 4KB header max
	}
}

func cacheKey(mount, p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return mount + ":" + p
}

// PutChildren caches directory entries for a given mount and directory path.
func (c *TreeCache) PutChildren(mount, dirPath string, entries []CachedFileInfo, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ttl <= 0 {
		ttl = c.defaultTTL
	}
	now := time.Now()
	expires := now.Add(ttl)

	key := cacheKey(mount, dirPath)
	storedEntries := make([]CachedFileInfo, len(entries))
	for i, e := range entries {
		e.CachedAt = now
		e.ExpiresAt = expires
		storedEntries[i] = e
		// Also store individual node lookup
		nodeKey := cacheKey(mount, e.Path)
		c.nodeMap[nodeKey] = e
	}
	c.dirEntries[key] = storedEntries
}

// GetChildren retrieves cached directory entries.
// If allowExpired is true (e.g. during a network disconnect), returns cached entries even if TTL expired.
func (c *TreeCache) GetChildren(mount, dirPath string, allowExpired bool) ([]CachedFileInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(mount, dirPath)
	entries, ok := c.dirEntries[key]
	if !ok || len(entries) == 0 {
		return nil, false
	}

	if !allowExpired && !entries[0].ExpiresAt.IsZero() && time.Now().After(entries[0].ExpiresAt) {
		return nil, false
	}

	res := make([]CachedFileInfo, len(entries))
	copy(res, entries)
	return res, true
}

// PutNode caches metadata for an individual file or directory.
func (c *TreeCache) PutNode(mount string, info CachedFileInfo, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ttl <= 0 {
		ttl = c.defaultTTL
	}
	now := time.Now()
	info.CachedAt = now
	info.ExpiresAt = now.Add(ttl)

	key := cacheKey(mount, info.Path)
	c.nodeMap[key] = info
}

// GetNode retrieves cached metadata for an individual file.
func (c *TreeCache) GetNode(mount, filePath string, allowExpired bool) (CachedFileInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(mount, filePath)
	info, ok := c.nodeMap[key]
	if !ok {
		return CachedFileInfo{}, false
	}

	if !allowExpired && !info.ExpiresAt.IsZero() && time.Now().After(info.ExpiresAt) {
		return CachedFileInfo{}, false
	}

	return info, true
}

// PutHeader caches a tiny file header (e.g. 512 bytes - 4KB) for signature and mime detection.
func (c *TreeCache) PutHeader(mount, filePath string, header []byte, mimeType string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(header) > c.maxHeaderLen {
		header = header[:c.maxHeaderLen]
	}

	key := cacheKey(mount, filePath)
	headerCopy := make([]byte, len(header))
	copy(headerCopy, header)
	c.headerMap[key] = headerCopy

	if mimeType == "" && len(header) > 0 {
		mimeType = http.DetectContentType(header)
	}
	if mimeType != "" {
		c.mimeMap[key] = mimeType
	}

	// Update node if present
	if node, ok := c.nodeMap[key]; ok {
		node.HeaderSnippet = headerCopy
		node.MIMEType = mimeType
		c.nodeMap[key] = node
	}
}

// GetHeader retrieves the cached tiny header and mime type for a file.
func (c *TreeCache) GetHeader(mount, filePath string) ([]byte, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(mount, filePath)
	h, hasH := c.headerMap[key]
	m, hasM := c.mimeMap[key]

	if !hasH && !hasM {
		return nil, "", false
	}

	hCopy := make([]byte, len(h))
	copy(hCopy, h)
	return hCopy, m, true
}

// Invalidate clears cache entries for a specific path or sub-tree.
func (c *TreeCache) Invalidate(mount, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := cacheKey(mount, path)
	delete(c.dirEntries, prefix)
	delete(c.nodeMap, prefix)
	delete(c.headerMap, prefix)
	delete(c.mimeMap, prefix)

	subPrefix := prefix + "/"
	for k := range c.dirEntries {
		if strings.HasPrefix(k, subPrefix) {
			delete(c.dirEntries, k)
		}
	}
	for k := range c.nodeMap {
		if strings.HasPrefix(k, subPrefix) {
			delete(c.nodeMap, k)
		}
	}
	for k := range c.headerMap {
		if strings.HasPrefix(k, subPrefix) {
			delete(c.headerMap, k)
		}
	}
	for k := range c.mimeMap {
		if strings.HasPrefix(k, subPrefix) {
			delete(c.mimeMap, k)
		}
	}
}

// InvalidateMount clears all cached data for a mount.
func (c *TreeCache) InvalidateMount(mount string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := mount + ":"
	for k := range c.dirEntries {
		if strings.HasPrefix(k, prefix) {
			delete(c.dirEntries, k)
		}
	}
	for k := range c.nodeMap {
		if strings.HasPrefix(k, prefix) {
			delete(c.nodeMap, k)
		}
	}
	for k := range c.headerMap {
		if strings.HasPrefix(k, prefix) {
			delete(c.headerMap, k)
		}
	}
	for k := range c.mimeMap {
		if strings.HasPrefix(k, prefix) {
			delete(c.mimeMap, k)
		}
	}
}

// IsOfflineUsable returns true if directory entries exist in cache to serve the app UI offline.
func (c *TreeCache) IsOfflineUsable(mount, dirPath string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := cacheKey(mount, dirPath)
	entries, ok := c.dirEntries[key]
	return ok && len(entries) > 0
}

// SaveToDB persists the entire cached directory tree and header cache to database for fast offline recovery.
func (c *TreeCache) SaveToDB(db *DB, mount string) error {
	if db == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	mountPrefix := mount + ":"
	mountDirs := make(map[string][]CachedFileInfo)
	for k, entries := range c.dirEntries {
		if mount == "" || strings.HasPrefix(k, mountPrefix) {
			mountDirs[k] = entries
		}
	}

	data, err := json.Marshal(mountDirs)
	if err != nil {
		return err
	}

	treeKey := "tree_" + mount
	if mount == "" {
		treeKey = "tree_all"
	}

	BucketVFSTree := []byte("vfs_tree")
	return db.PutEncrypted(BucketVFSTree, treeKey, data)
}

// LoadFromDB restores cached directory tree from persistent database.
func (c *TreeCache) LoadFromDB(db *DB, mount string) error {
	if db == nil {
		return nil
	}

	treeKey := "tree_" + mount
	if mount == "" {
		treeKey = "tree_all"
	}

	BucketVFSTree := []byte("vfs_tree")
	data, err := db.GetDecrypted(BucketVFSTree, treeKey)
	if err != nil {
		return err
	}

	var loadedDirs map[string][]CachedFileInfo
	if err := json.Unmarshal(data, &loadedDirs); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for k, entries := range loadedDirs {
		c.dirEntries[k] = entries
		for _, e := range entries {
			nodeKey := cacheKey(mount, e.Path)
			c.nodeMap[nodeKey] = e
			if len(e.HeaderSnippet) > 0 {
				c.headerMap[nodeKey] = e.HeaderSnippet
			}
			if e.MIMEType != "" {
				c.mimeMap[nodeKey] = e.MIMEType
			}
		}
	}

	return nil
}

// DetectMIMESignature returns detected MIME type from magic headers or fallback.
func DetectMIMESignature(header []byte) string {
	if len(header) == 0 {
		return "application/octet-stream"
	}
	if bytes.HasPrefix(header, []byte("PK\x03\x04")) {
		return "application/zip"
	}
	if bytes.HasPrefix(header, []byte("7z\xbc\xaf\x27\x1c")) {
		return "application/x-7z-compressed"
	}
	if bytes.HasPrefix(header, []byte("Rar!\x1a\x07\x00")) || bytes.HasPrefix(header, []byte("Rar!\x1a\x07\x01\x00")) {
		return "application/vnd.rar"
	}
	if bytes.HasPrefix(header, []byte("\x1f\x8b")) {
		return "application/gzip"
	}
	if bytes.HasPrefix(header, []byte("BZh")) {
		return "application/x-bzip2"
	}
	if bytes.HasPrefix(header, []byte("\xff\xd8\xff")) {
		return "image/jpeg"
	}
	if bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	if bytes.HasPrefix(header, []byte("GIF87a")) || bytes.HasPrefix(header, []byte("GIF89a")) {
		return "image/gif"
	}
	if bytes.HasPrefix(header, []byte("%PDF-")) {
		return "application/pdf"
	}
	return http.DetectContentType(header)
}

