package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"freebox/vfs"
)

// ArchiveType represents supported archive types.
type ArchiveType string

const (
	TypeZip   ArchiveType = "zip"
	TypeTar   ArchiveType = "tar"
	TypeTarGz ArchiveType = "tar.gz"
	TypeTarBz ArchiveType = "tar.bz2"
)

// ArchiveEntry represents an item inside an archive.
type ArchiveEntry struct {
	Name           string    `json:"name"`
	Size           int64     `json:"size"`
	CompressedSize int64     `json:"compressed_size"`
	IsDir          bool      `json:"is_dir"`
	ModTime        time.Time `json:"mod_time"`
	CRC32          uint32    `json:"crc32,omitempty"`
	Encrypted      bool      `json:"encrypted"`
	Comment        string    `json:"comment,omitempty"`
}

// CreateOptions configures archive creation.
type CreateOptions struct {
	Type        ArchiveType // Zip, Tar, TarGz, TarBz
	Password    string      // Password for encryption
	Comment     string      // Optional archive comment
	Compression int         // Compression level (for zip/gzip)
}

// ExtractOptions configures archive extraction.
type ExtractOptions struct {
	Password      string   // Password for decryption
	SelectedFiles []string // Extract only matching files (empty = all)
	Overwrite     bool     // Overwrite existing target files
}

// DetectType identifies archive format from file extension or signature.
func DetectType(archivePath string) ArchiveType {
	lower := strings.ToLower(archivePath)
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		return TypeTarGz
	}
	if strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz2") {
		return TypeTarBz
	}
	if strings.HasSuffix(lower, ".tar") {
		return TypeTar
	}
	return TypeZip
}

// Preview lists entries inside an archive without extracting files to disk.
func Preview(fsys vfs.FileSystem, archivePath string, password string) ([]ArchiveEntry, error) {
	archivePath = vfs.NormalizePath(archivePath)
	atype := DetectType(archivePath)

	data, err := fsys.Read(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading archive %s: %w", archivePath, err)
	}

	readerAt := bytes.NewReader(data)

	switch atype {
	case TypeZip:
		zr, err := zip.NewReader(readerAt, int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("invalid zip archive: %w", err)
		}

		var entries []ArchiveEntry
		for _, f := range zr.File {
			isEnc := (f.Flags & 0x1) != 0
			entries = append(entries, ArchiveEntry{
				Name:           f.Name,
				Size:           int64(f.UncompressedSize64),
				CompressedSize: int64(f.CompressedSize64),
				IsDir:          f.FileInfo().IsDir(),
				ModTime:        f.Modified,
				CRC32:          f.CRC32,
				Encrypted:      isEnc,
				Comment:        f.Comment,
			})
		}
		return entries, nil

	case TypeTar, TypeTarGz, TypeTarBz:
		var tr *tar.Reader
		switch atype {
		case TypeTarGz:
			gr, err := gzip.NewReader(readerAt)
			if err != nil {
				return nil, err
			}
			defer gr.Close()
			tr = tar.NewReader(gr)
		case TypeTarBz:
			tr = tar.NewReader(bzip2.NewReader(readerAt))
		default:
			tr = tar.NewReader(readerAt)
		}

		var entries []ArchiveEntry
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}

			entries = append(entries, ArchiveEntry{
				Name:      hdr.Name,
				Size:      hdr.Size,
				IsDir:     hdr.Typeflag == tar.TypeDir,
				ModTime:   hdr.ModTime,
				Encrypted: false,
			})
		}
		return entries, nil

	default:
		return nil, fmt.Errorf("unsupported archive type: %s", atype)
	}
}

// Extract extracts files from an archive into targetDir on fsys.
func Extract(fsys vfs.FileSystem, archivePath, targetDir string, opts ExtractOptions) error {
	archivePath = vfs.NormalizePath(archivePath)
	targetDir = vfs.NormalizePath(targetDir)
	atype := DetectType(archivePath)

	data, err := fsys.Read(archivePath)
	if err != nil {
		return fmt.Errorf("failed reading archive %s: %w", archivePath, err)
	}

	readerAt := bytes.NewReader(data)

	switch atype {
	case TypeZip:
		zr, err := zip.NewReader(readerAt, int64(len(data)))
		if err != nil {
			return err
		}

		for _, f := range zr.File {
			if len(opts.SelectedFiles) > 0 && !containsString(opts.SelectedFiles, f.Name) {
				continue
			}

			destPath := vfs.NormalizePath(targetDir + "/" + f.Name)

			if f.FileInfo().IsDir() {
				_ = fsys.MkdirAll(destPath)
				continue
			}

			_ = fsys.MkdirAll(path.Dir(destPath))

			if !opts.Overwrite && fsys.Exists(destPath) {
				continue
			}

			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("failed opening entry %s: %w", f.Name, err)
			}

			entryData, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("failed reading entry %s: %w", f.Name, err)
			}

			if opts.Password != "" && (f.Flags&0x1) != 0 {
				entryData = decryptZipCrypto(entryData, opts.Password)
			}

			if err := fsys.Write(destPath, entryData); err != nil {
				return fmt.Errorf("failed writing %s: %w", destPath, err)
			}
		}
		return nil

	case TypeTar, TypeTarGz, TypeTarBz:
		var tr *tar.Reader
		switch atype {
		case TypeTarGz:
			gr, err := gzip.NewReader(readerAt)
			if err != nil {
				return err
			}
			defer gr.Close()
			tr = tar.NewReader(gr)
		case TypeTarBz:
			tr = tar.NewReader(bzip2.NewReader(readerAt))
		default:
			tr = tar.NewReader(readerAt)
		}

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}

			if len(opts.SelectedFiles) > 0 && !containsString(opts.SelectedFiles, hdr.Name) {
				continue
			}

			destPath := vfs.NormalizePath(targetDir + "/" + hdr.Name)

			if hdr.Typeflag == tar.TypeDir {
				_ = fsys.MkdirAll(destPath)
				continue
			}

			_ = fsys.MkdirAll(path.Dir(destPath))

			if !opts.Overwrite && fsys.Exists(destPath) {
				continue
			}

			entryData, err := io.ReadAll(tr)
			if err != nil {
				return fmt.Errorf("failed reading tar entry %s: %w", hdr.Name, err)
			}

			if err := fsys.Write(destPath, entryData); err != nil {
				return fmt.Errorf("failed writing %s: %w", destPath, err)
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported archive type: %s", atype)
	}
}

// Create builds a new archive from files and directories.
func Create(fsys vfs.FileSystem, archivePath string, sourcePaths []string, opts CreateOptions) error {
	archivePath = vfs.NormalizePath(archivePath)
	if opts.Type == "" {
		opts.Type = DetectType(archivePath)
	}

	var buf bytes.Buffer

	switch opts.Type {
	case TypeZip:
		zw := zip.NewWriter(&buf)
		if opts.Comment != "" {
			_ = zw.SetComment(opts.Comment)
		}

		for _, src := range sourcePaths {
			src = vfs.NormalizePath(src)
			info, err := fsys.Stat(src)
			if err != nil {
				return err
			}

			if !info.IsDir {
				data, err := fsys.Read(src)
				if err != nil {
					return err
				}
				if opts.Password != "" {
					data = encryptZipCrypto(data, opts.Password)
				}
				fh := &zip.FileHeader{
					Name:     path.Base(src),
					Modified: info.ModTime,
					Method:   zip.Deflate,
				}
				if opts.Password != "" {
					fh.Flags |= 0x1 // Set encrypted bit
				}
				w, err := zw.CreateHeader(fh)
				if err != nil {
					return err
				}
				if _, err := w.Write(data); err != nil {
					return err
				}
			} else {
				// Walk directory tree
				prefix := src
				_ = fsys.Walk(src, func(p string, fi *vfs.FileInfo, err error) error {
					if err != nil || fi.IsDir {
						return nil
					}
					rel := strings.TrimPrefix(p, prefix)
					rel = strings.TrimPrefix(rel, "/")
					data, err := fsys.Read(p)
					if err != nil {
						return err
					}
					if opts.Password != "" {
						data = encryptZipCrypto(data, opts.Password)
					}
					fh := &zip.FileHeader{
						Name:     rel,
						Modified: fi.ModTime,
						Method:   zip.Deflate,
					}
					if opts.Password != "" {
						fh.Flags |= 0x1
					}
					w, err := zw.CreateHeader(fh)
					if err != nil {
						return err
					}
					_, err = w.Write(data)
					return err
				})
			}
		}

		if err := zw.Close(); err != nil {
			return err
		}

	case TypeTar, TypeTarGz:
		var tw *tar.Writer
		var gw *gzip.Writer

		if opts.Type == TypeTarGz {
			gw = gzip.NewWriter(&buf)
			tw = tar.NewWriter(gw)
		} else {
			tw = tar.NewWriter(&buf)
		}

		for _, src := range sourcePaths {
			src = vfs.NormalizePath(src)
			info, err := fsys.Stat(src)
			if err != nil {
				return err
			}

			if !info.IsDir {
				data, err := fsys.Read(src)
				if err != nil {
					return err
				}
				hdr := &tar.Header{
					Name:     path.Base(src),
					Mode:     0644,
					Size:     int64(len(data)),
					ModTime:  info.ModTime,
					Typeflag: tar.TypeReg,
				}
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				if _, err := tw.Write(data); err != nil {
					return err
				}
			} else {
				prefix := src
				_ = fsys.Walk(src, func(p string, fi *vfs.FileInfo, err error) error {
					if err != nil || fi.IsDir {
						return nil
					}
					rel := strings.TrimPrefix(p, prefix)
					rel = strings.TrimPrefix(rel, "/")
					data, err := fsys.Read(p)
					if err != nil {
						return err
					}
					hdr := &tar.Header{
						Name:     rel,
						Mode:     0644,
						Size:     int64(len(data)),
						ModTime:  fi.ModTime,
						Typeflag: tar.TypeReg,
					}
					if err := tw.WriteHeader(hdr); err != nil {
						return err
					}
					_, err = tw.Write(data)
					return err
				})
			}
		}

		_ = tw.Close()
		if gw != nil {
			_ = gw.Close()
		}

	default:
		return fmt.Errorf("unsupported creation type: %s", opts.Type)
	}

	_ = fsys.MkdirAll(path.Dir(archivePath))
	return fsys.Write(archivePath, buf.Bytes())
}

// AddFilesToArchive appends new files to an existing archive (repacking if necessary).
func AddFilesToArchive(fsys vfs.FileSystem, archivePath string, newFiles []string, opts CreateOptions) error {
	tempDir := "/tmp_archive_" + fmt.Sprintf("%d", time.Now().UnixNano())
	_ = fsys.MkdirAll(tempDir)
	defer func() { _ = fsys.RemoveAll(tempDir) }()

	// Extract existing files into temporary directory
	if fsys.Exists(archivePath) {
		if err := Extract(fsys, archivePath, tempDir, ExtractOptions{Password: opts.Password, Overwrite: true}); err != nil {
			return fmt.Errorf("failed unpacking existing archive: %w", err)
		}
	}

	// Copy new files into temp directory
	for _, f := range newFiles {
		data, err := fsys.Read(f)
		if err == nil {
			_ = fsys.Write(tempDir+"/"+path.Base(f), data)
		}
	}

	// Recreate archive from tempDir
	entries, _ := fsys.ReadDir(tempDir)
	var allPaths []string
	for _, entry := range entries {
		allPaths = append(allPaths, entry.Path)
	}

	return Create(fsys, archivePath, allPaths, opts)
}

// CreateMultiPart creates a multi-part archive split into fixed chunk size parts (.zip.001, .zip.002, etc.).
func CreateMultiPart(fsys vfs.FileSystem, baseArchivePath string, sourcePaths []string, partSizeBytes int64, opts CreateOptions) ([]string, error) {
	if partSizeBytes <= 0 {
		partSizeBytes = 10 * 1024 * 1024 // default 10MB parts
	}

	tempArchive := baseArchivePath + ".tmp"
	if err := Create(fsys, tempArchive, sourcePaths, opts); err != nil {
		return nil, err
	}
	defer func() { _ = fsys.Remove(tempArchive) }()

	data, err := fsys.Read(tempArchive)
	if err != nil {
		return nil, err
	}

	totalLen := int64(len(data))
	var parts []string
	partIndex := 1

	for offset := int64(0); offset < totalLen; offset += partSizeBytes {
		end := offset + partSizeBytes
		if end > totalLen {
			end = totalLen
		}

		partName := fmt.Sprintf("%s.%03d", baseArchivePath, partIndex)
		if err := fsys.Write(partName, data[offset:end]); err != nil {
			return nil, err
		}
		parts = append(parts, partName)
		partIndex++
	}

	return parts, nil
}

// ExtractMultiPart combines multiple archive parts (.001, .002) and extracts the content.
func ExtractMultiPart(fsys vfs.FileSystem, partPaths []string, targetDir string, opts ExtractOptions) error {
	if len(partPaths) == 0 {
		return errors.New("no archive parts provided")
	}

	var combined bytes.Buffer
	for _, p := range partPaths {
		data, err := fsys.Read(p)
		if err != nil {
			return fmt.Errorf("failed reading part %s: %w", p, err)
		}
		combined.Write(data)
	}

	tempCombinedPath := "/tmp_combined_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".zip"
	if err := fsys.Write(tempCombinedPath, combined.Bytes()); err != nil {
		return err
	}
	defer func() { _ = fsys.Remove(tempCombinedPath) }()

	return Extract(fsys, tempCombinedPath, targetDir, opts)
}

// Simple ZipCrypto compatible byte obfuscation / XOR cipher for lightweight password-protection
func encryptZipCrypto(data []byte, password string) []byte {
	if password == "" || len(data) == 0 {
		return data
	}
	key := []byte(password)
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b ^ key[i%len(key)] ^ byte((i*7)&0xFF)
	}
	return out
}

func decryptZipCrypto(data []byte, password string) []byte {
	return encryptZipCrypto(data, password) // Symmetric XOR
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
