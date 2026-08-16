package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"freebox/vfs"
)

// MatchType defines the method of matching strings.
type MatchType string

const (
	MatchSubstring MatchType = "substring"
	MatchExact     MatchType = "exact"
	MatchRegex     MatchType = "regex"
	MatchGlob      MatchType = "glob"
	MatchPrefix    MatchType = "prefix"
	MatchSuffix    MatchType = "suffix"
)

// SearchTarget defines what should be searched.
type SearchTarget string

const (
	TargetNamesAndContent SearchTarget = "all"
	TargetNameOnly        SearchTarget = "names"
	TargetContentOnly     SearchTarget = "content"
)

// FilterOptions defines filters applied during search.
type FilterOptions struct {
	MinSize       int64     // Min file size in bytes (-1 for no minimum)
	MaxSize       int64     // Max file size in bytes (-1 for no maximum)
	ModifiedAfter time.Time // Only files modified after this time
	ModifiedBefore time.Time // Only files modified before this time
	Extensions    []string  // Allowed extensions (e.g. [".txt", ".go"])
	ExcludeExts   []string  // Excluded extensions (e.g. [".bin", ".exe"])
	SkipHidden    bool      // Skip files/dirs starting with "."
	IncludeDirs   bool      // Include directory names in search results
	SkipBinary    bool      // Detect and skip binary files during content search
	MaxDepth      int       // Max directory depth (-1 for unlimited)
}

// SearchQuery configures the search and optional replacement operation.
type SearchQuery struct {
	RootPath         string        // Root directory to search within
	NamePattern      string        // Pattern to match filenames against
	NameMatchType    MatchType     // Matching type for filenames
	ContentPattern   string        // Pattern to match inside file contents
	ContentMatchType MatchType     // Matching type for file content
	CaseSensitive    bool          // Case sensitivity for matching
	Target           SearchTarget  // Names, Content, or Both
	Filters          FilterOptions // Additional file filtering options

	// Replacement configuration
	Replacement  string // String to replace matches with
	IsReplace    bool   // True if replacement should be performed
	DryRun       bool   // If true, simulate replacements without modifying files
	CreateBackup bool   // If true, creates a .bak backup file before replacing

	// Performance tuning
	MaxWorkers int   // Number of parallel content search workers (default: NumCPU * 4)
	MaxMatches int64 // Stop search after finding N matches (0 = unlimited)
	BufferSize int   // Buffer size for reading content (default: 64KB)

	// Callback for real-time progress
	OnProgress func(stats SearchStats)
}

// ContentMatch represents a line match inside a file.
type ContentMatch struct {
	LineNumber int    `json:"line_number"`
	LineText   string `json:"line_text"`
	MatchStart int    `json:"match_start"`
	MatchEnd   int    `json:"match_end"`
	Replaced   string `json:"replaced,omitempty"`
}

// SearchResult represents a matching file and its details.
type SearchResult struct {
	Path           string         `json:"path"`
	Name           string         `json:"name"`
	IsDir          bool           `json:"is_dir"`
	Size           int64          `json:"size"`
	ModTime        time.Time      `json:"mod_time"`
	NameMatched    bool           `json:"name_matched"`
	ContentMatched bool           `json:"content_matched"`
	NewName        string         `json:"new_name,omitempty"`
	Matches        []ContentMatch `json:"matches,omitempty"`
	ReplacedCount  int            `json:"replaced_count,omitempty"`
	Error          string         `json:"error,omitempty"`
}

// SearchStats contains real-time and final statistics for a search run.
type SearchStats struct {
	FilesScanned   int64         `json:"files_scanned"`
	DirsScanned    int64         `json:"dirs_scanned"`
	FilesMatched   int64         `json:"files_matched"`
	TotalMatches   int64         `json:"total_matches"`
	Replacements   int64         `json:"replacements"`
	BytesRead      int64         `json:"bytes_read"`
	Duration       time.Duration `json:"duration"`
	CurrentPath    string        `json:"current_path,omitempty"`
	ItemsPerSecond float64       `json:"items_per_second"`
}

// Engine performs fast searches and replacements on a VFS.
type Engine struct {
	fs vfs.FileSystem
}

// NewEngine creates a new search engine for the given filesystem.
func NewEngine(fs vfs.FileSystem) *Engine {
	return &Engine{fs: fs}
}

// Search executes a search or batch replace on the filesystem.
func (e *Engine) Search(ctx context.Context, query SearchQuery) ([]*SearchResult, SearchStats, error) {
	if query.RootPath == "" {
		query.RootPath = "/"
	}
	query.RootPath = vfs.NormalizePath(query.RootPath)

	if query.MaxWorkers <= 0 {
		query.MaxWorkers = runtime.NumCPU() * 4
		if query.MaxWorkers < 4 {
			query.MaxWorkers = 4
		}
	}
	if query.BufferSize <= 0 {
		query.BufferSize = 64 * 1024
	}
	if query.Target == "" {
		query.Target = TargetNamesAndContent
	}
	if query.NameMatchType == "" {
		query.NameMatchType = MatchSubstring
	}
	if query.ContentMatchType == "" {
		query.ContentMatchType = MatchSubstring
	}

	// Compile name matcher
	nameMatcher, err := compileMatcher(query.NamePattern, query.NameMatchType, query.CaseSensitive)
	if err != nil && query.NamePattern != "" {
		return nil, SearchStats{}, fmt.Errorf("invalid name pattern: %w", err)
	}

	// Compile content matcher
	var contentMatcher *matcher
	if query.ContentPattern != "" && query.Target != TargetNameOnly {
		contentMatcher, err = compileMatcher(query.ContentPattern, query.ContentMatchType, query.CaseSensitive)
		if err != nil {
			return nil, SearchStats{}, fmt.Errorf("invalid content pattern: %w", err)
		}
	}

	var (
		stats = SearchStats{}
		start = time.Now()

		scannedFiles atomic.Int64
		scannedDirs  atomic.Int64
		matchedFiles atomic.Int64
		totalMatches atomic.Int64
		replacements atomic.Int64
		bytesRead    atomic.Int64

		resultsMu sync.Mutex
		results   []*SearchResult

		fileChan = make(chan *vfs.FileInfo, 1024)
		wg       sync.WaitGroup
	)

	// Worker pool for concurrent content processing
	for i := 0; i < query.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fi := range fileChan {
				if ctx.Err() != nil {
					return
				}
				if query.MaxMatches > 0 && matchedFiles.Load() >= query.MaxMatches {
					return
				}

				res := e.processFile(ctx, fi, query, nameMatcher, contentMatcher, &bytesRead)
				if res != nil {
					matchedFiles.Add(1)
					totalMatches.Add(int64(len(res.Matches)))
					replacements.Add(int64(res.ReplacedCount))

					resultsMu.Lock()
					results = append(results, res)
					resultsMu.Unlock()
				}
			}
		}()
	}

	// Stream directory traversal with low-overhead walk
	rootDepth := strings.Count(query.RootPath, "/")
	walkErr := e.fs.Walk(query.RootPath, func(p string, fi *vfs.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable paths without aborting entire walk
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if query.MaxMatches > 0 && matchedFiles.Load() >= query.MaxMatches {
			return errors.New("max matches reached")
		}

		// Depth filter
		if query.Filters.MaxDepth >= 0 {
			currentDepth := strings.Count(p, "/") - rootDepth
			if currentDepth > query.Filters.MaxDepth {
				if fi.IsDir {
					return nil
				}
			}
		}

		// Hidden file filter
		if query.Filters.SkipHidden && strings.HasPrefix(fi.Name, ".") && p != "/" {
			return nil
		}

		if fi.IsDir {
			scannedDirs.Add(1)
			if query.Filters.IncludeDirs && query.Target != TargetContentOnly && nameMatcher != nil && nameMatcher.MatchString(fi.Name) {
				matchedFiles.Add(1)
				resultsMu.Lock()
				results = append(results, &SearchResult{
					Path:        p,
					Name:        fi.Name,
					IsDir:       true,
					ModTime:     fi.ModTime,
					NameMatched: true,
				})
				resultsMu.Unlock()
			}
			return nil
		}

		scannedFiles.Add(1)

		// Size filter
		if query.Filters.MinSize > 0 && fi.Size < query.Filters.MinSize {
			return nil
		}
		if query.Filters.MaxSize > 0 && fi.Size > query.Filters.MaxSize {
			return nil
		}

		// Time filter
		if !query.Filters.ModifiedAfter.IsZero() && fi.ModTime.Before(query.Filters.ModifiedAfter) {
			return nil
		}
		if !query.Filters.ModifiedBefore.IsZero() && fi.ModTime.After(query.Filters.ModifiedBefore) {
			return nil
		}

		// Extension filter
		ext := strings.ToLower(path.Ext(fi.Name))
		if len(query.Filters.Extensions) > 0 {
			allowed := false
			for _, e := range query.Filters.Extensions {
				if strings.ToLower(e) == ext {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil
			}
		}
		if len(query.Filters.ExcludeExts) > 0 {
			for _, e := range query.Filters.ExcludeExts {
				if strings.ToLower(e) == ext {
					return nil
				}
			}
		}

		select {
		case fileChan <- fi:
		case <-ctx.Done():
			return ctx.Err()
		}

		// Progress reporting
		if query.OnProgress != nil && (scannedFiles.Load()%500 == 0) {
			dur := time.Since(start)
			rate := float64(scannedFiles.Load()) / dur.Seconds()
			query.OnProgress(SearchStats{
				FilesScanned:   scannedFiles.Load(),
				DirsScanned:    scannedDirs.Load(),
				FilesMatched:   matchedFiles.Load(),
				TotalMatches:   totalMatches.Load(),
				Replacements:   replacements.Load(),
				BytesRead:      bytesRead.Load(),
				Duration:       dur,
				CurrentPath:    p,
				ItemsPerSecond: rate,
			})
		}

		return nil
	})

	close(fileChan)
	wg.Wait()

	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && walkErr.Error() != "max matches reached" {
		// Non-fatal
	}

	dur := time.Since(start)
	stats.FilesScanned = scannedFiles.Load()
	stats.DirsScanned = scannedDirs.Load()
	stats.FilesMatched = matchedFiles.Load()
	stats.TotalMatches = totalMatches.Load()
	stats.Replacements = replacements.Load()
	stats.BytesRead = bytesRead.Load()
	stats.Duration = dur
	if dur.Seconds() > 0 {
		stats.ItemsPerSecond = float64(stats.FilesScanned) / dur.Seconds()
	}

	if query.OnProgress != nil {
		query.OnProgress(stats)
	}

	return results, stats, nil
}

func (e *Engine) processFile(
	ctx context.Context,
	fi *vfs.FileInfo,
	query SearchQuery,
	nameMatcher *matcher,
	contentMatcher *matcher,
	bytesReadCounter *atomic.Int64,
) *SearchResult {
	nameMatches := false
	if nameMatcher != nil && nameMatcher.MatchString(fi.Name) {
		nameMatches = true
	}

	// If searching only names
	if query.Target == TargetNameOnly {
		if !nameMatches {
			return nil
		}

		res := &SearchResult{
			Path:        fi.Path,
			Name:        fi.Name,
			IsDir:       false,
			Size:        fi.Size,
			ModTime:     fi.ModTime,
			NameMatched: true,
		}

		if query.IsReplace && query.NamePattern != "" {
			newName := nameMatcher.ReplaceAllString(fi.Name, query.Replacement)
			res.NewName = newName
			if !query.DryRun && newName != fi.Name {
				newPath := path.Join(path.Dir(fi.Path), newName)
				if err := e.fs.Rename(fi.Path, newPath); err != nil {
					res.Error = err.Error()
				} else {
					res.ReplacedCount = 1
				}
			} else if query.DryRun && newName != fi.Name {
				res.ReplacedCount = 1
			}
		}
		return res
	}

	// Check if content match is needed
	if contentMatcher == nil {
		if nameMatches {
			return &SearchResult{
				Path:        fi.Path,
				Name:        fi.Name,
				IsDir:       false,
				Size:        fi.Size,
				ModTime:     fi.ModTime,
				NameMatched: true,
			}
		}
		return nil
	}

	// Read file content
	rc, err := e.fs.Open(fi.Path)
	if err != nil {
		return nil
	}
	defer rc.Close()

	reader := bufio.NewReaderSize(rc, query.BufferSize)

	// Binary file detection if requested
	if query.Filters.SkipBinary {
		peekBytes, _ := reader.Peek(512)
		if len(peekBytes) > 0 && isBinaryData(peekBytes) {
			if nameMatches && query.Target == TargetNamesAndContent {
				return &SearchResult{
					Path:        fi.Path,
					Name:        fi.Name,
					IsDir:       false,
					Size:        fi.Size,
					ModTime:     fi.ModTime,
					NameMatched: true,
				}
			}
			return nil
		}
	}

	var (
		contentMatches  []ContentMatch
		replacedBuffer  bytes.Buffer
		lineNum         = 0
		hasReplacements = false
		fileBytesRead   int64
	)

	for {
		if ctx.Err() != nil {
			return nil
		}

		line, isPrefix, err := reader.ReadLine()
		if len(line) > 0 {
			fileBytesRead += int64(len(line))
		}
		if err != nil && len(line) == 0 {
			break
		}

		lineNum++
		fullLine := line
		// If line was longer than buffer, consume remainder
		for isPrefix {
			var extra []byte
			extra, isPrefix, err = reader.ReadLine()
			fileBytesRead += int64(len(extra))
			fullLine = append(fullLine, extra...)
			if err != nil {
				break
			}
		}

		lineStr := string(fullLine)
		if contentMatcher.MatchString(lineStr) {
			locs := contentMatcher.FindAllIndex(fullLine)
			for _, loc := range locs {
				cm := ContentMatch{
					LineNumber: lineNum,
					LineText:   lineStr,
					MatchStart: loc[0],
					MatchEnd:   loc[1],
				}
				if query.IsReplace {
					cm.Replaced = contentMatcher.ReplaceAllString(lineStr, query.Replacement)
				}
				contentMatches = append(contentMatches, cm)
			}

			if query.IsReplace {
				replacedLine := contentMatcher.ReplaceAllString(lineStr, query.Replacement)
				replacedBuffer.WriteString(replacedLine)
				hasReplacements = true
			} else {
				replacedBuffer.WriteString(lineStr)
			}
		} else if query.IsReplace {
			replacedBuffer.WriteString(lineStr)
		}

		if err == nil {
			replacedBuffer.WriteByte('\n')
		}

		if err != nil {
			break
		}
	}

	bytesReadCounter.Add(fileBytesRead)

	if len(contentMatches) == 0 && !nameMatches {
		return nil
	}

	res := &SearchResult{
		Path:           fi.Path,
		Name:           fi.Name,
		IsDir:          false,
		Size:           fi.Size,
		ModTime:        fi.ModTime,
		NameMatched:    nameMatches,
		ContentMatched: len(contentMatches) > 0,
		Matches:        contentMatches,
	}

	// Handle Content & Name Replacement
	if query.IsReplace {
		if hasReplacements {
			res.ReplacedCount = len(contentMatches)
			if !query.DryRun {
				// Backup if requested
				if query.CreateBackup {
					bakPath := fi.Path + ".bak"
					if origData, readErr := e.fs.Read(fi.Path); readErr == nil {
						_ = e.fs.Write(bakPath, origData)
					}
				}
				// Write replaced content
				if writeErr := e.fs.Write(fi.Path, replacedBuffer.Bytes()); writeErr != nil {
					res.Error = writeErr.Error()
				}
			}
		}

		// Also handle filename replace if name pattern given
		if nameMatches && query.NamePattern != "" {
			newName := nameMatcher.ReplaceAllString(fi.Name, query.Replacement)
			if newName != fi.Name {
				res.NewName = newName
				if !query.DryRun {
					newPath := path.Join(path.Dir(fi.Path), newName)
					_ = e.fs.Rename(fi.Path, newPath)
				}
			}
		}
	}

	return res
}

func isBinaryData(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	if bytes.IndexByte(b, 0) != -1 {
		return true
	}
	nonPrintable := 0
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			nonPrintable++
		} else if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			nonPrintable++
		}
		i += size
	}
	return (float64(nonPrintable) / float64(len(b))) > 0.3
}

// Internal flexible regex/literal matcher
type matcher struct {
	re *regexp.Regexp
}

func compileMatcher(pattern string, mtype MatchType, caseSensitive bool) (*matcher, error) {
	if pattern == "" {
		return nil, nil
	}

	var regexPattern string
	switch mtype {
	case MatchExact:
		regexPattern = "^" + regexp.QuoteMeta(pattern) + "$"
	case MatchPrefix:
		regexPattern = "^" + regexp.QuoteMeta(pattern)
	case MatchSuffix:
		regexPattern = regexp.QuoteMeta(pattern) + "$"
	case MatchGlob:
		// Convert standard glob to regex
		regexPattern = "^" + globToRegex(pattern) + "$"
	case MatchRegex:
		regexPattern = pattern
	case MatchSubstring:
		fallthrough
	default:
		regexPattern = regexp.QuoteMeta(pattern)
	}

	if !caseSensitive {
		regexPattern = "(?i)" + regexPattern
	}

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return nil, err
	}
	return &matcher{re: re}, nil
}

func (m *matcher) MatchString(s string) bool {
	if m == nil || m.re == nil {
		return true
	}
	return m.re.MatchString(s)
}

func (m *matcher) ReplaceAllString(src, repl string) string {
	if m == nil || m.re == nil {
		return src
	}
	return m.re.ReplaceAllString(src, repl)
}

func (m *matcher) FindAllIndex(b []byte) [][]int {
	if m == nil || m.re == nil {
		return nil
	}
	return m.re.FindAllIndex(b, -1)
}

func globToRegex(glob string) string {
	var sb strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		case '.', '+', '(', ')', '|', '^', '$', '[', ']', '{', '}', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// Convenience package-level functions
func Search(fs vfs.FileSystem, query SearchQuery) ([]*SearchResult, SearchStats, error) {
	return NewEngine(fs).Search(context.Background(), query)
}

func SearchFiles(fs vfs.FileSystem, rootPath, pattern string) ([]*SearchResult, error) {
	results, _, err := NewEngine(fs).Search(context.Background(), SearchQuery{
		RootPath:      rootPath,
		NamePattern:   pattern,
		NameMatchType: MatchGlob,
		Target:        TargetNameOnly,
	})
	return results, err
}

func ReplaceInFiles(fs vfs.FileSystem, rootPath, searchPattern, replacement string, dryRun bool) (SearchStats, error) {
	_, stats, err := NewEngine(fs).Search(context.Background(), SearchQuery{
		RootPath:         rootPath,
		ContentPattern:   searchPattern,
		ContentMatchType: MatchSubstring,
		Replacement:      replacement,
		IsReplace:        true,
		DryRun:           dryRun,
		Target:           TargetContentOnly,
	})
	return stats, err
}
