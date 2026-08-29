package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// EditDraft represents an active in-memory or persisted working copy / draft of an edited file.
type EditDraft struct {
	SessionID      string    `json:"session_id"`
	Mount          string    `json:"mount"`
	RemotePath     string    `json:"remote_path"`
	Mode           string    `json:"mode"` // "direct" or "local_copy"
	LocalPath      string    `json:"local_path,omitempty"`
	OriginalSHA256 string    `json:"original_sha256"`
	CurrentSHA256  string    `json:"current_sha256"`
	DraftData      []byte    `json:"draft_data,omitempty"`
	IsDirty        bool      `json:"is_dirty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type memoryCacheItem struct {
	data      []byte
	mimeType  string
	category  string
	size      int64
	createdAt time.Time
	expiresAt time.Time
}

// MediaCache is a multi-tier cache for thumbnails, media proxy streams,
// remote archive previews, and in-memory editing buffers.
type MediaCache struct {
	db          *DB
	memItems    map[string]*memoryCacheItem
	drafts      map[string]*EditDraft
	archiveMeta map[string][]byte
	maxMemBytes int64
	curMemBytes int64
	mu          sync.RWMutex
}

// MediaCacheConfig provides configuration for the media cache.
type MediaCacheConfig struct {
	DB          *DB
	MaxMemoryMB int // Default: 64 MB
}

// NewMediaCache creates a new MediaCache instance.
func NewMediaCache(cfg MediaCacheConfig) *MediaCache {
	mb := cfg.MaxMemoryMB
	if mb <= 0 {
		mb = 64
	}

	return &MediaCache{
		db:          cfg.DB,
		memItems:    make(map[string]*memoryCacheItem),
		drafts:      make(map[string]*EditDraft),
		archiveMeta: make(map[string][]byte),
		maxMemBytes: int64(mb) * 1024 * 1024,
	}
}

// --- 1. Thumbnails & Media Cache ---

// PutThumbnail stores a generated thumbnail in memory and encrypted DB.
func (c *MediaCache) PutThumbnail(key string, data []byte, mimeType string) {
	c.storeMemory(key, "thumbnail", data, mimeType, 24*time.Hour)
	if c.db != nil {
		_ = c.db.PutEncrypted(BucketThumbnails, key, data)
	}
}

// GetThumbnail retrieves a thumbnail from in-memory cache or DB.
func (c *MediaCache) GetThumbnail(key string) ([]byte, string, bool) {
	if data, mime, ok := c.getMemory(key); ok {
		return data, mime, true
	}

	if c.db != nil {
		data, err := c.db.GetDecrypted(BucketThumbnails, key)
		if err == nil && len(data) > 0 {
			c.storeMemory(key, "thumbnail", data, "image/jpeg", 24*time.Hour)
			return data, "image/jpeg", true
		}
	}

	return nil, "", false
}

// --- 2. Temp Media Proxy Cache ---

// PutProxyChunk stores a temporary video/audio proxy streaming chunk.
func (c *MediaCache) PutProxyChunk(streamID string, chunkIndex int64, data []byte, ttl time.Duration) {
	key := fmt.Sprintf("proxy:%s:%d", streamID, chunkIndex)
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	c.storeMemory(key, "proxy_chunk", data, "application/octet-stream", ttl)
}

// GetProxyChunk retrieves a proxy streaming chunk from memory cache.
func (c *MediaCache) GetProxyChunk(streamID string, chunkIndex int64) ([]byte, bool) {
	key := fmt.Sprintf("proxy:%s:%d", streamID, chunkIndex)
	data, _, ok := c.getMemory(key)
	return data, ok
}

// --- 3. Temp Archive File / Header Cache ---

// PutArchivePreview caches parsed archive directory listings for remote archives.
func (c *MediaCache) PutArchivePreview(archiveKey string, entriesJSON []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.archiveMeta[archiveKey] = entriesJSON
	if c.db != nil {
		_ = c.db.PutEncrypted(BucketMediaCache, "archive:"+archiveKey, entriesJSON)
	}
}

// GetArchivePreview retrieves cached archive entries JSON.
func (c *MediaCache) GetArchivePreview(archiveKey string) ([]byte, bool) {
	c.mu.RLock()
	if data, ok := c.archiveMeta[archiveKey]; ok {
		c.mu.RUnlock()
		return data, true
	}
	c.mu.RUnlock()

	if c.db != nil {
		data, err := c.db.GetDecrypted(BucketMediaCache, "archive:"+archiveKey)
		if err == nil && len(data) > 0 {
			c.mu.Lock()
			c.archiveMeta[archiveKey] = data
			c.mu.Unlock()
			return data, true
		}
	}

	return nil, false
}

// --- 4. In-Memory & Persisted Editing Files ---

// SaveEditDraft saves an in-memory draft of an actively edited file.
func (c *MediaCache) SaveEditDraft(draft EditDraft) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if draft.SessionID == "" {
		draft.SessionID = fmt.Sprintf("edit-%d", time.Now().UnixNano())
	}
	if draft.CreatedAt.IsZero() {
		draft.CreatedAt = time.Now()
	}
	draft.UpdatedAt = time.Now()

	// Compute current SHA256 if draft data present
	if len(draft.DraftData) > 0 {
		h := sha256.Sum256(draft.DraftData)
		draft.CurrentSHA256 = hex.EncodeToString(h[:])
	}

	c.drafts[draft.SessionID] = &draft

	if c.db != nil {
		data, err := json.Marshal(draft)
		if err != nil {
			return err
		}
		return c.db.PutEncrypted(BucketEditDrafts, draft.SessionID, data)
	}

	return nil
}

// GetEditDraft retrieves an active editing session draft.
func (c *MediaCache) GetEditDraft(sessionID string) (*EditDraft, bool) {
	c.mu.RLock()
	if d, ok := c.drafts[sessionID]; ok {
		c.mu.RUnlock()
		return d, true
	}
	c.mu.RUnlock()

	if c.db != nil {
		data, err := c.db.GetDecrypted(BucketEditDrafts, sessionID)
		if err == nil {
			var d EditDraft
			if err := json.Unmarshal(data, &d); err == nil {
				c.mu.Lock()
				c.drafts[sessionID] = &d
				c.mu.Unlock()
				return &d, true
			}
		}
	}

	return nil, false
}

// ListEditDrafts returns all active or persisted editing drafts.
func (c *MediaCache) ListEditDrafts() ([]EditDraft, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	resultMap := make(map[string]EditDraft)
	for id, d := range c.drafts {
		resultMap[id] = *d
	}

	if c.db != nil {
		rawMap, err := c.db.ListDecrypted(BucketEditDrafts)
		if err == nil {
			for _, data := range rawMap {
				var d EditDraft
				if err := json.Unmarshal(data, &d); err == nil {
					if _, exists := resultMap[d.SessionID]; !exists {
						resultMap[d.SessionID] = d
					}
				}
			}
		}
	}

	results := make([]EditDraft, 0, len(resultMap))
	for _, d := range resultMap {
		results = append(results, d)
	}
	return results, nil
}

// DeleteEditDraft removes an edit session draft.
func (c *MediaCache) DeleteEditDraft(sessionID string) error {
	c.mu.Lock()
	delete(c.drafts, sessionID)
	c.mu.Unlock()

	if c.db != nil {
		return c.db.Delete(BucketEditDrafts, sessionID)
	}
	return nil
}

// --- Internal Memory Helpers ---

func (c *MediaCache) storeMemory(key, category string, data []byte, mimeType string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	size := int64(len(data))
	// Evict if over memory limit
	if c.curMemBytes+size > c.maxMemBytes && len(c.memItems) > 0 {
		c.evictHalf()
	}

	expires := time.Time{}
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}

	c.memItems[key] = &memoryCacheItem{
		data:      data,
		mimeType:  mimeType,
		category:  category,
		size:      size,
		createdAt: time.Now(),
		expiresAt: expires,
	}
	c.curMemBytes += size
}

func (c *MediaCache) getMemory(key string) ([]byte, string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.memItems[key]
	if !ok {
		return nil, "", false
	}
	if !item.expiresAt.IsZero() && time.Now().After(item.expiresAt) {
		return nil, "", false
	}

	return item.data, item.mimeType, true
}

func (c *MediaCache) evictHalf() {
	count := 0
	target := len(c.memItems) / 2
	for k, v := range c.memItems {
		c.curMemBytes -= v.size
		delete(c.memItems, k)
		count++
		if count >= target {
			break
		}
	}
}

// ClearCategory removes all cached items of a given category.
func (c *MediaCache) ClearCategory(category string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range c.memItems {
		if v.category == category {
			c.curMemBytes -= v.size
			delete(c.memItems, k)
		}
	}
}

