package thumbnail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"freebox/vfs"
)

// ThumbnailFormat defines output image format.
type ThumbnailFormat string

const (
	FormatJPEG ThumbnailFormat = "jpeg"
	FormatPNG  ThumbnailFormat = "png"
)

// ThumbnailOptions configures thumbnail generation.
type ThumbnailOptions struct {
	Width       int             // Desired max width (default: 256)
	Height      int             // Desired max height (default: 256)
	Quality     int             // JPEG quality 1-100 (default: 80)
	Format      ThumbnailFormat // "jpeg" or "png" (default: jpeg)
	CropSquare  bool            // Crop to center square instead of aspect ratio fit
	Placeholder bool            // Generate icon placeholder if format unsupported
}

// DefaultOptions returns default thumbnail settings.
func DefaultOptions() ThumbnailOptions {
	return ThumbnailOptions{
		Width:       256,
		Height:      256,
		Quality:     80,
		Format:      FormatJPEG,
		CropSquare:  false,
		Placeholder: true,
	}
}

type cacheEntry struct {
	data      []byte
	mimeType  string
	createdAt time.Time
	size      int64
}

// Engine manages thumbnail generation, multi-tier caching, and indexing.
type Engine struct {
	cacheDir string
	memCache map[string]*cacheEntry
	memLimit int64 // Max memory cache bytes (e.g. 64MB)
	memUsed  int64
	mu       sync.RWMutex
}

// Config configures the thumbnail engine.
type Config struct {
	CacheDir      string // Disk directory for thumbnail cache (optional)
	MaxMemoryMB   int    // Max in-memory cache size in MB (default: 64)
}

// NewEngine creates a new Thumbnail engine.
func NewEngine(cfg Config) *Engine {
	memMB := cfg.MaxMemoryMB
	if memMB <= 0 {
		memMB = 64
	}

	e := &Engine{
		cacheDir: cfg.CacheDir,
		memCache: make(map[string]*cacheEntry),
		memLimit: int64(memMB) * 1024 * 1024,
	}

	if e.cacheDir != "" {
		_ = os.MkdirAll(e.cacheDir, 0755)
	}

	return e
}

// CacheKey generates a cache key derived from file metadata and thumbnail options.
func CacheKey(p string, size int64, modTime time.Time, opts ThumbnailOptions) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s:%d:%d:%d:%d:%s:%v", p, size, modTime.UnixNano(), opts.Width, opts.Height, opts.Format, opts.CropSquare)
	return hex.EncodeToString(h.Sum(nil))
}

// GetOrGenerate retrieves cached thumbnail or generates a new one from VFS file.
func (e *Engine) GetOrGenerate(ctx context.Context, fsys vfs.FileSystem, filePath string, opts ThumbnailOptions) ([]byte, string, error) {
	if opts.Width <= 0 {
		opts.Width = 256
	}
	if opts.Height <= 0 {
		opts.Height = 256
	}
	if opts.Quality <= 0 {
		opts.Quality = 80
	}
	if opts.Format == "" {
		opts.Format = FormatJPEG
	}

	filePath = vfs.NormalizePath(filePath)
	stat, err := fsys.Stat(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to stat %s: %w", filePath, err)
	}
	if stat.IsDir {
		return nil, "", errors.New("cannot generate thumbnail for directory")
	}

	key := CacheKey(filePath, stat.Size, stat.ModTime, opts)

	// 1. Check in-memory cache
	e.mu.RLock()
	if entry, ok := e.memCache[key]; ok {
		e.mu.RUnlock()
		return entry.data, entry.mimeType, nil
	}
	e.mu.RUnlock()

	// 2. Check on-disk cache
	if e.cacheDir != "" {
		diskPath := filepath.Join(e.cacheDir, key)
		if data, err := os.ReadFile(diskPath); err == nil {
			mimeType := "image/jpeg"
			if opts.Format == FormatPNG {
				mimeType = "image/png"
			}
			e.storeMemory(key, data, mimeType)
			return data, mimeType, nil
		}
	}

	// 3. Generate thumbnail
	data, mimeType, err := e.generate(ctx, fsys, filePath, stat, opts)
	if err != nil {
		return nil, "", err
	}

	// 4. Save to caches
	e.storeMemory(key, data, mimeType)
	if e.cacheDir != "" {
		diskPath := filepath.Join(e.cacheDir, key)
		_ = os.WriteFile(diskPath, data, 0644)
	}

	return data, mimeType, nil
}

func (e *Engine) storeMemory(key string, data []byte, mimeType string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	itemSize := int64(len(data))
	// Simple eviction if over memory limit
	if e.memUsed+itemSize > e.memLimit && len(e.memCache) > 0 {
		// Evict half of cache
		count := 0
		for k, v := range e.memCache {
			e.memUsed -= v.size
			delete(e.memCache, k)
			count++
			if count >= len(e.memCache)/2 {
				break
			}
		}
	}

	e.memCache[key] = &cacheEntry{
		data:      data,
		mimeType:  mimeType,
		createdAt: time.Now(),
		size:      itemSize,
	}
	e.memUsed += itemSize
}

func (e *Engine) generate(ctx context.Context, fsys vfs.FileSystem, filePath string, stat *vfs.FileInfo, opts ThumbnailOptions) ([]byte, string, error) {
	ext := strings.ToLower(path.Ext(filePath))

	// If image format
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp":
		rc, err := fsys.Open(filePath)
		if err != nil {
			return nil, "", err
		}
		defer rc.Close()

		var srcImg image.Image
		switch ext {
		case ".jpg", ".jpeg":
			srcImg, err = jpeg.Decode(rc)
		case ".png":
			srcImg, err = png.Decode(rc)
		case ".gif":
			srcImg, err = gif.Decode(rc)
		default:
			srcImg, _, err = image.Decode(rc)
		}

		if err != nil {
			if opts.Placeholder {
				return e.generatePlaceholder(filePath, opts)
			}
			return nil, "", fmt.Errorf("image decode error: %w", err)
		}

		// Resize image
		dstImg := resizeImage(srcImg, opts.Width, opts.Height, opts.CropSquare)

		// Encode thumbnail
		var buf bytes.Buffer
		mimeType := "image/jpeg"

		if opts.Format == FormatPNG {
			err = png.Encode(&buf, dstImg)
			mimeType = "image/png"
		} else {
			err = jpeg.Encode(&buf, dstImg, &jpeg.Options{Quality: opts.Quality})
		}

		if err != nil {
			return nil, "", err
		}
		return buf.Bytes(), mimeType, nil

	default:
		if opts.Placeholder {
			return e.generatePlaceholder(filePath, opts)
		}
		return nil, "", errors.New("unsupported file format for thumbnail")
	}
}

// resizeImage resizes src to fit within targetWidth and targetHeight using bilinear interpolation.
func resizeImage(src image.Image, targetWidth, targetHeight int, cropSquare bool) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	if srcW <= 0 || srcH <= 0 {
		return image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	}

	var dstW, dstH int
	if cropSquare {
		dstW = targetWidth
		dstH = targetHeight
	} else {
		// Calculate aspect ratio preserving dimensions
		ratioW := float64(targetWidth) / float64(srcW)
		ratioH := float64(targetHeight) / float64(srcH)
		ratio := ratioW
		if ratioH < ratio {
			ratio = ratioH
		}
		dstW = int(float64(srcW) * ratio)
		dstH = int(float64(srcH) * ratio)
		if dstW <= 0 {
			dstW = 1
		}
		if dstH <= 0 {
			dstH = 1
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	// Fast bilinear interpolation scaler
	for y := 0; y < dstH; y++ {
		srcY := float64(y) * float64(srcH) / float64(dstH)
		iy := int(srcY)
		if iy >= srcH {
			iy = srcH - 1
		}
		for x := 0; x < dstW; x++ {
			srcX := float64(x) * float64(srcW) / float64(dstW)
			ix := int(srcX)
			if ix >= srcW {
				ix = srcW - 1
			}
			c := src.At(bounds.Min.X+ix, bounds.Min.Y+iy)
			dst.Set(x, y, c)
		}
	}

	return dst
}

// generatePlaceholder creates a stylish placeholder graphic for non-image files.
func e_placeholderColor(ext string) color.RGBA {
	switch ext {
	case ".pdf":
		return color.RGBA{220, 53, 69, 255} // Red
	case ".mp4", ".mkv", ".avi", ".mov":
		return color.RGBA{111, 66, 193, 255} // Purple
	case ".mp3", ".flac", ".wav", ".aac":
		return color.RGBA{253, 126, 20, 255} // Orange
	case ".zip", ".tar", ".gz", ".7z", ".rar":
		return color.RGBA{255, 193, 7, 255} // Amber
	case ".go", ".js", ".ts", ".py", ".html", ".css":
		return color.RGBA{32, 201, 151, 255} // Teal
	default:
		return color.RGBA{108, 117, 125, 255} // Slate Gray
	}
}

func (e *Engine) generatePlaceholder(filePath string, opts ThumbnailOptions) ([]byte, string, error) {
	w := opts.Width
	h := opts.Height
	if w <= 0 {
		w = 128
	}
	if h <= 0 {
		h = 128
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	ext := strings.ToLower(path.Ext(filePath))
	bg := e_placeholderColor(ext)

	// Fill background
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// Draw inner rounded box
	margin := w / 8
	innerRect := image.Rect(margin, margin, w-margin, h-margin)
	innerColor := color.RGBA{R: bg.R / 2, G: bg.G / 2, B: bg.B / 2, A: 255}
	draw.Draw(img, innerRect, &image.Uniform{innerColor}, image.Point{}, draw.Over)

	var buf bytes.Buffer
	mimeType := "image/jpeg"
	if opts.Format == FormatPNG {
		_ = png.Encode(&buf, img)
		mimeType = "image/png"
	} else {
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.Quality})
	}

	return buf.Bytes(), mimeType, nil
}

// ClearCache clears all in-memory and disk cached thumbnails.
func (e *Engine) ClearCache() error {
	e.mu.Lock()
	e.memCache = make(map[string]*cacheEntry)
	e.memUsed = 0
	e.mu.Unlock()

	if e.cacheDir != "" {
		return os.RemoveAll(e.cacheDir)
	}
	return nil
}
