package archive

import (
	"context"
	"encoding/json"
	"fmt"

	"freebox/storage"
	"freebox/vfs"
)

// PreviewRemote previews archive contents located directly on a remote filesystem.
// If cache is provided, previously parsed directory listings are retrieved instantly without re-fetching from remote.
func PreviewRemote(ctx context.Context, fsys vfs.FileSystem, remoteArchivePath string, password string, cache *storage.MediaCache) ([]ArchiveEntry, error) {
	if fsys == nil {
		return nil, fmt.Errorf("filesystem is required")
	}

	remoteArchivePath = vfs.NormalizePath(remoteArchivePath)
	stat, err := fsys.Stat(remoteArchivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat remote archive: %w", err)
	}

	cacheKey := fmt.Sprintf("remote:%s:%d:%d", remoteArchivePath, stat.Size, stat.ModTime.UnixNano())

	if cache != nil {
		if cachedData, ok := cache.GetArchivePreview(cacheKey); ok && len(cachedData) > 0 {
			var entries []ArchiveEntry
			if err := json.Unmarshal(cachedData, &entries); err == nil {
				return entries, nil
			}
		}
	}

	// Preview using streaming/spooling reader
	entries, err := Preview(fsys, remoteArchivePath, password)
	if err != nil {
		return nil, err
	}

	if cache != nil && len(entries) > 0 {
		if jsonData, err := json.Marshal(entries); err == nil {
			cache.PutArchivePreview(cacheKey, jsonData)
		}
	}

	return entries, nil
}

