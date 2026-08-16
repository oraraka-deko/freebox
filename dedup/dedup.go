package dedup

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"sort"
	"sync"
	"time"

	"freebox/vfs"
)

// DedupMethod defines the algorithm used to determine if two files are duplicate.
type DedupMethod string

const (
	MethodMeta      DedupMethod = "meta"       // Match Name + Size (+ ModTime)
	MethodQuickHash DedupMethod = "quick_hash" // Sample hash (head + middle + tail + size)
	MethodMD5       DedupMethod = "md5"        // Full MD5 hash
	MethodSHA256    DedupMethod = "sha256"     // Full SHA-256 hash
	MethodSHA1      DedupMethod = "sha1"       // Full SHA-1 hash
	MethodCRC32     DedupMethod = "crc32"      // Full CRC-32 checksum
)

// KeepPolicy defines which duplicate file is preserved when deleting duplicates.
type KeepPolicy string

const (
	KeepOldest       KeepPolicy = "oldest"        // Keep file with oldest ModTime
	KeepNewest       KeepPolicy = "newest"        // Keep file with newest ModTime
	KeepShortestPath KeepPolicy = "shortest_path" // Keep file with shortest path length
	KeepFirst        KeepPolicy = "first"         // Keep first scanned file
)

// DedupAction defines what action to take on confirmed duplicates.
type DedupAction string

const (
	ActionReportOnly DedupAction = "report" // Only find and report duplicates
	ActionDelete     DedupAction = "delete" // Delete duplicate files, preserving master
)

// DedupPhase represents current processing stage.
type DedupPhase string

const (
	PhaseScanning       DedupPhase = "scanning"
	PhaseGroupingBySize DedupPhase = "grouping_by_size"
	PhaseQuickHashing   DedupPhase = "quick_hashing"
	PhaseFullHashing    DedupPhase = "full_hashing"
	PhaseActionExecution DedupPhase = "executing_actions"
	PhaseCompleted      DedupPhase = "completed"
)

// DedupProgress contains real-time progress metrics.
type DedupProgress struct {
	Phase           DedupPhase    `json:"phase"`
	FilesScanned    int64         `json:"files_scanned"`
	BytesScanned    int64         `json:"bytes_scanned"`
	PotentialGroups int64         `json:"potential_groups"`
	DuplicateGroups int64         `json:"duplicate_groups"`
	DuplicateFiles  int64         `json:"duplicate_files"`
	DuplicateBytes  int64         `json:"duplicate_bytes"`
	DeletedFiles    int64         `json:"deleted_files"`
	CurrentPath     string        `json:"current_path,omitempty"`
	Duration        time.Duration `json:"duration"`
	Percent         float64       `json:"percent"`
}

// DuplicateFile represents an individual file in a duplicate set.
type DuplicateFile struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	IsMaster bool     `json:"is_master"`
	Deleted  bool     `json:"deleted,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// DuplicateGroup represents a cluster of identical files.
type DuplicateGroup struct {
	Hash           string          `json:"hash"`
	Size           int64           `json:"size"`
	WastedBytes    int64           `json:"wasted_bytes"` // (len(Files) - 1) * Size
	Master         *DuplicateFile  `json:"master"`
	Duplicates     []*DuplicateFile `json:"duplicates"`
}

// DedupOptions configures the deduplication engine.
type DedupOptions struct {
	RootPath     string        // Path to scan for duplicates
	Method       DedupMethod   // Meta, QuickHash, MD5, SHA256, etc.
	Action       DedupAction   // Report or Delete
	KeepPolicy   KeepPolicy    // Which file to keep when deleting
	MinFileSize  int64         // Minimum file size to consider (default: 1 byte)
	MaxFileSize  int64         // Maximum file size to consider (0 = unlimited)
	MaxWorkers   int           // Parallel hashing workers
	DryRun       bool          // Simulate deletion without deleting
	OnProgress   func(p DedupProgress)
}

// Engine runs fast local and remote deduplication.
type Engine struct {
	fs vfs.FileSystem
}

// NewEngine creates a new deduplicator for fs.
func NewEngine(fs vfs.FileSystem) *Engine {
	return &Engine{fs: fs}
}

// Deduplicate scans, clusters duplicates, and optionally removes them.
func (e *Engine) Deduplicate(ctx context.Context, opts DedupOptions) ([]*DuplicateGroup, DedupProgress, error) {
	if opts.RootPath == "" {
		opts.RootPath = "/"
	}
	opts.RootPath = vfs.NormalizePath(opts.RootPath)

	if opts.Method == "" {
		opts.Method = MethodQuickHash
	}
	if opts.Action == "" {
		opts.Action = ActionReportOnly
	}
	if opts.KeepPolicy == "" {
		opts.KeepPolicy = KeepOldest
	}
	if opts.MinFileSize <= 0 {
		opts.MinFileSize = 1
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 8
	}

	var (
		start = time.Now()
		prog  = DedupProgress{
			Phase: PhaseScanning,
		}

		// Stage 1: Group by size
		sizeMap = make(map[int64][]*vfs.FileInfo)
	)

	// Step 1: Scan all files and group by file size
	err := e.fs.Walk(opts.RootPath, func(p string, fi *vfs.FileInfo, err error) error {
		if err != nil || fi.IsDir {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if fi.Size < opts.MinFileSize {
			return nil
		}
		if opts.MaxFileSize > 0 && fi.Size > opts.MaxFileSize {
			return nil
		}

		prog.FilesScanned++
		prog.BytesScanned += fi.Size
		prog.CurrentPath = p
		sizeMap[fi.Size] = append(sizeMap[fi.Size], fi)

		if opts.OnProgress != nil && prog.FilesScanned%1000 == 0 {
			prog.Duration = time.Since(start)
			opts.OnProgress(prog)
		}
		return nil
	})

	if err != nil && !errors.Is(err, context.Canceled) {
		return nil, prog, fmt.Errorf("scan error: %w", err)
	}

	// Filter out size groups with only 1 file (unique sizes can never be duplicates!)
	prog.Phase = PhaseGroupingBySize
	var potentialFiles []*vfs.FileInfo
	for _, files := range sizeMap {
		if len(files) > 1 {
			prog.PotentialGroups++
			potentialFiles = append(potentialFiles, files...)
		}
	}

	if len(potentialFiles) == 0 {
		prog.Phase = PhaseCompleted
		prog.Duration = time.Since(start)
		if opts.OnProgress != nil {
			opts.OnProgress(prog)
		}
		return nil, prog, nil
	}

	// Stage 2: Hash files in potential groups
	var groups []*DuplicateGroup

	if opts.Method == MethodMeta {
		// Group by Name + Size
		nameSizeMap := make(map[string][]*vfs.FileInfo)
		for _, fi := range potentialFiles {
			key := fmt.Sprintf("%s:%d", fi.Name, fi.Size)
			nameSizeMap[key] = append(nameSizeMap[key], fi)
		}
		for key, files := range nameSizeMap {
			if len(files) > 1 {
				g := e.buildGroup(key, files[0].Size, files, opts)
				groups = append(groups, g)
			}
		}
	} else if opts.Method == MethodQuickHash {
		// Quick sample hash first
		prog.Phase = PhaseQuickHashing
		quickMap := e.parallelHash(ctx, potentialFiles, MethodQuickHash, opts)
		
		// For clusters still > 1 in quick hash, verify with SHA-256 full hash
		prog.Phase = PhaseFullHashing
		var fullCheckFiles []*vfs.FileInfo
		for _, files := range quickMap {
			if len(files) > 1 {
				fullCheckFiles = append(fullCheckFiles, files...)
			}
		}

		fullMap := e.parallelHash(ctx, fullCheckFiles, MethodSHA256, opts)
		for hashStr, files := range fullMap {
			if len(files) > 1 {
				g := e.buildGroup(hashStr, files[0].Size, files, opts)
				groups = append(groups, g)
			}
		}
	} else {
		// Direct full hash (MD5, SHA256, SHA1, CRC32)
		prog.Phase = PhaseFullHashing
		hashMap := e.parallelHash(ctx, potentialFiles, opts.Method, opts)
		for hashStr, files := range hashMap {
			if len(files) > 1 {
				g := e.buildGroup(hashStr, files[0].Size, files, opts)
				groups = append(groups, g)
			}
		}
	}

	// Stage 3: Aggregate stats & execute actions (delete duplicates if requested)
	prog.Phase = PhaseActionExecution
	var totalDupFiles int64
	var totalDupBytes int64
	var deletedCount int64

	for _, g := range groups {
		dupCount := int64(len(g.Duplicates))
		totalDupFiles += dupCount
		totalDupBytes += g.WastedBytes

		if opts.Action == ActionDelete && !opts.DryRun {
			for _, dup := range g.Duplicates {
				if err := e.fs.Remove(dup.Path); err != nil {
					dup.Error = err.Error()
				} else {
					dup.Deleted = true
					deletedCount++
				}
			}
		} else if opts.Action == ActionDelete && opts.DryRun {
			for _, dup := range g.Duplicates {
				dup.Deleted = true
				deletedCount++
			}
		}
	}

	prog.Phase = PhaseCompleted
	prog.DuplicateGroups = int64(len(groups))
	prog.DuplicateFiles = totalDupFiles
	prog.DuplicateBytes = totalDupBytes
	prog.DeletedFiles = deletedCount
	prog.Duration = time.Since(start)
	prog.Percent = 100.0

	if opts.OnProgress != nil {
		opts.OnProgress(prog)
	}

	return groups, prog, nil
}

func (e *Engine) parallelHash(
	ctx context.Context,
	files []*vfs.FileInfo,
	method DedupMethod,
	opts DedupOptions,
) map[string][]*vfs.FileInfo {
	type hashResult struct {
		fi   *vfs.FileInfo
		hash string
		err  error
	}

	workers := opts.MaxWorkers
	if workers <= 0 {
		workers = 8
	}

	inChan := make(chan *vfs.FileInfo, len(files))
	outChan := make(chan hashResult, len(files))
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fi := range inChan {
				if ctx.Err() != nil {
					return
				}
				h, err := e.calculateHash(fi.Path, fi.Size, method)
				outChan <- hashResult{fi: fi, hash: h, err: err}
			}
		}()
	}

	for _, fi := range files {
		inChan <- fi
	}
	close(inChan)

	wg.Wait()
	close(outChan)

	hashMap := make(map[string][]*vfs.FileInfo)
	for res := range outChan {
		if res.err == nil && res.hash != "" {
			hashMap[res.hash] = append(hashMap[res.hash], res.fi)
		}
	}

	return hashMap
}

func (e *Engine) calculateHash(p string, size int64, method DedupMethod) (string, error) {
	rc, err := e.fs.Open(p)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	if method == MethodQuickHash {
		// Sample 4KB from start, 4KB from middle (if size > 16KB), 4KB from end
		sampleSize := int64(4096)
		hasher := sha256.New()
		_, _ = hasher.Write([]byte(fmt.Sprintf("%d:", size)))

		if size <= sampleSize*3 {
			// Small file: hash completely
			if _, err := io.Copy(hasher, rc); err != nil {
				return "", err
			}
			return hex.EncodeToString(hasher.Sum(nil)), nil
		}

		headBuf := make([]byte, sampleSize)
		n, _ := io.ReadFull(rc, headBuf)
		_, _ = hasher.Write(headBuf[:n])

		// Read whole stream in chunks or sample
		// Since VFS reader might not support seek directly, stream and capture mid + tail
		midTarget := size / 2
		tailTarget := size - sampleSize
		var currentOffset int64 = int64(n)

		discardBuf := make([]byte, 32*1024)
		for currentOffset < midTarget {
			toRead := midTarget - currentOffset
			if toRead > int64(len(discardBuf)) {
				toRead = int64(len(discardBuf))
			}
			nr, er := rc.Read(discardBuf[:toRead])
			currentOffset += int64(nr)
			if er != nil {
				break
			}
		}

		midBuf := make([]byte, sampleSize)
		nr, _ := io.ReadFull(rc, midBuf)
		currentOffset += int64(nr)
		_, _ = hasher.Write(midBuf[:nr])

		for currentOffset < tailTarget {
			toRead := tailTarget - currentOffset
			if toRead > int64(len(discardBuf)) {
				toRead = int64(len(discardBuf))
			}
			nr, er := rc.Read(discardBuf[:toRead])
			currentOffset += int64(nr)
			if er != nil {
				break
			}
		}

		tailBuf := make([]byte, sampleSize)
		nr, _ = io.ReadFull(rc, tailBuf)
		_, _ = hasher.Write(tailBuf[:nr])

		return hex.EncodeToString(hasher.Sum(nil)), nil
	}

	var h hash.Hash
	switch method {
	case MethodMD5:
		h = md5.New()
	case MethodSHA1:
		h = sha1.New()
	case MethodCRC32:
		h = crc32.NewIEEE()
	case MethodSHA256:
		fallthrough
	default:
		h = sha256.New()
	}

	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(h, rc, buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func (e *Engine) buildGroup(hashStr string, size int64, files []*vfs.FileInfo, opts DedupOptions) *DuplicateGroup {
	dups := make([]*DuplicateFile, len(files))
	for i, fi := range files {
		dups[i] = &DuplicateFile{
			Path:    fi.Path,
			Name:    fi.Name,
			Size:    fi.Size,
			ModTime: fi.ModTime,
		}
	}

	// Sort duplicates according to KeepPolicy to pick master
	sort.Slice(dups, func(i, j int) bool {
		switch opts.KeepPolicy {
		case KeepNewest:
			return dups[i].ModTime.After(dups[j].ModTime)
		case KeepShortestPath:
			return len(dups[i].Path) < len(dups[j].Path)
		case KeepFirst:
			return i < j
		case KeepOldest:
			fallthrough
		default:
			return dups[i].ModTime.Before(dups[j].ModTime)
		}
	})

	master := dups[0]
	master.IsMaster = true
	duplicates := dups[1:]

	return &DuplicateGroup{
		Hash:        hashStr,
		Size:        size,
		WastedBytes: int64(len(duplicates)) * size,
		Master:      master,
		Duplicates:  duplicates,
	}
}

// Deduplicate is a convenience package function.
func Deduplicate(fs vfs.FileSystem, opts DedupOptions) ([]*DuplicateGroup, DedupProgress, error) {
	return NewEngine(fs).Deduplicate(context.Background(), opts)
}
