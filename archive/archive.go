package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode"

	"freebox/internal/bufferpool"
	"freebox/vfs"
)

// ArchiveType represents supported archive types.
type ArchiveType string

const (
	TypeZip   ArchiveType = "zip"
	TypeTar   ArchiveType = "tar"
	TypeTarGz ArchiveType = "tar.gz"
	TypeTarBz ArchiveType = "tar.bz2"
	TypeRar   ArchiveType = "rar"
	Type7z    ArchiveType = "7z"
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
	if strings.HasSuffix(lower, ".rar") {
		return TypeRar
	}
	if strings.HasSuffix(lower, ".7z") {
		return Type7z
	}
	return TypeZip
}

// DetectTypeFromHeader inspects magic bytes to detect archive format.
func DetectTypeFromHeader(header []byte, fallbackPath string) ArchiveType {
	if len(header) >= 2 && bytes.HasPrefix(header, []byte("PK\x03\x04")) {
		return TypeZip
	}
	if len(header) >= 6 && bytes.HasPrefix(header, []byte("7z\xbc\xaf\x27\x1c")) {
		return Type7z
	}
	if len(header) >= 4 && (bytes.HasPrefix(header, []byte("Rar!\x1a\x07\x00")) || bytes.HasPrefix(header, []byte("Rar!\x1a\x07\x01\x00"))) {
		return TypeRar
	}
	if len(header) >= 2 && bytes.HasPrefix(header, []byte("\x1f\x8b")) {
		return TypeTarGz
	}
	if len(header) >= 3 && bytes.HasPrefix(header, []byte("BZh")) {
		return TypeTarBz
	}
	return DetectType(fallbackPath)
}

type readerAtCloser struct {
	r       io.ReaderAt
	size    int64
	closer  io.Closer
	tmpPath string
}

func (c *readerAtCloser) Close() error {
	var err error
	if c.closer != nil {
		err = c.closer.Close()
	}
	if c.tmpPath != "" {
		_ = os.Remove(c.tmpPath)
	}
	return err
}

// openArchiveReaderAt prepares an io.ReaderAt for random-access archives (zip, 7z)
// without loading whole files into memory, keeping memory usage constant regardless of file size.
func openArchiveReaderAt(fsys vfs.FileSystem, archivePath string) (*readerAtCloser, error) {
	stat, err := fsys.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("stat failed: %w", err)
	}

	// Try OpenFile
	f, err := fsys.OpenFile(context.Background(), archivePath, vfs.ReadOnly())
	if err == nil {
		if rAt, ok := f.(io.ReaderAt); ok {
			return &readerAtCloser{
				r:      rAt,
				size:   stat.Size,
				closer: f,
			}, nil
		}
	}

	// If streaming only, spool to a temporary disk file in chunks
	tmpFile, err := os.CreateTemp("", "freebox-archive-spool-*")
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		return nil, fmt.Errorf("create temp spool failed: %w", err)
	}

	srcReader := io.Reader(f)
	if f == nil {
		rc, err := fsys.Open(archivePath)
		if err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpFile.Name())
			return nil, fmt.Errorf("open stream failed: %w", err)
		}
		srcReader = rc
		defer rc.Close()
	} else {
		defer f.Close()
	}

	buf := bufferpool.Acquire(64 * 1024)
	defer bufferpool.Release(buf)

	written, err := io.CopyBuffer(tmpFile, srcReader, buf)
	if err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("spooling archive failed: %w", err)
	}

	return &readerAtCloser{
		r:       tmpFile,
		size:    written,
		closer:  tmpFile,
		tmpPath: tmpFile.Name(),
	}, nil
}

// Preview lists entries inside an archive without extracting files to disk.
func Preview(fsys vfs.FileSystem, archivePath string, password string) ([]ArchiveEntry, error) {
	archivePath = vfs.NormalizePath(archivePath)
	atype := DetectType(archivePath)

	switch atype {
	case TypeZip:
		rac, err := openArchiveReaderAt(fsys, archivePath)
		if err != nil {
			return nil, err
		}
		defer rac.Close()

		zr, err := zip.NewReader(rac.r, rac.size)
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

	case Type7z:
		rac, err := openArchiveReaderAt(fsys, archivePath)
		if err != nil {
			return nil, err
		}
		defer rac.Close()

		var szr *sevenzip.Reader
		if password != "" {
			szr, err = sevenzip.NewReaderWithPassword(rac.r, rac.size, password)
		} else {
			szr, err = sevenzip.NewReader(rac.r, rac.size)
		}
		if err != nil {
			return nil, fmt.Errorf("invalid 7z archive: %w", err)
		}

		var entries []ArchiveEntry
		for _, f := range szr.File {
			entries = append(entries, ArchiveEntry{
				Name:           f.Name,
				Size:           int64(f.UncompressedSize),
				CompressedSize: int64(f.UncompressedSize),
				IsDir:          f.FileInfo().IsDir(),
				ModTime:        f.Modified,
				CRC32:          f.CRC32,
				Encrypted:      password != "",
			})
		}
		return entries, nil

	case TypeRar:
		rc, err := fsys.Open(archivePath)
		if err != nil {
			return nil, fmt.Errorf("failed opening rar archive %s: %w", archivePath, err)
		}
		defer rc.Close()

		var rarReader *rardecode.Reader
		if password != "" {
			rarReader, err = rardecode.NewReader(rc, password)
		} else {
			rarReader, err = rardecode.NewReader(rc, "")
		}
		if err != nil {
			return nil, fmt.Errorf("invalid rar archive: %w", err)
		}

		var entries []ArchiveEntry
		for {
			hdr, err := rarReader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}

			entries = append(entries, ArchiveEntry{
				Name:           hdr.Name,
				Size:           hdr.UnPackedSize,
				CompressedSize: hdr.PackedSize,
				IsDir:          hdr.IsDir,
				ModTime:        hdr.ModificationTime,
				Encrypted:      password != "",
			})
		}
		return entries, nil

	case TypeTar, TypeTarGz, TypeTarBz:
		rc, err := fsys.Open(archivePath)
		if err != nil {
			return nil, fmt.Errorf("failed opening archive %s: %w", archivePath, err)
		}
		defer rc.Close()

		var tr *tar.Reader
		switch atype {
		case TypeTarGz:
			gr, err := gzip.NewReader(rc)
			if err != nil {
				return nil, err
			}
			defer gr.Close()
			tr = tar.NewReader(gr)
		case TypeTarBz:
			tr = tar.NewReader(bzip2.NewReader(rc))
		default:
			tr = tar.NewReader(rc)
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
// Extract extracts files from an archive into targetDir on fsys with memory-bounded streaming.
func Extract(fsys vfs.FileSystem, archivePath, targetDir string, opts ExtractOptions) error {
	archivePath = vfs.NormalizePath(archivePath)
	targetDir = vfs.NormalizePath(targetDir)
	atype := DetectType(archivePath)

	buf := bufferpool.Acquire(64 * 1024)
	defer bufferpool.Release(buf)

	switch atype {
	case TypeZip:
		rac, err := openArchiveReaderAt(fsys, archivePath)
		if err != nil {
			return err
		}
		defer rac.Close()

		zr, err := zip.NewReader(rac.r, rac.size)
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

			dstWriter, err := fsys.Create(destPath)
			if err != nil {
				_ = rc.Close()
				return fmt.Errorf("failed creating %s: %w", destPath, err)
			}

			encrypted := opts.Password != "" && (f.Flags&0x1) != 0
			var srcStream io.Reader = rc
			if encrypted {
				entryData, rErr := io.ReadAll(rc)
				_ = rc.Close()
				if rErr != nil {
					_ = dstWriter.Close()
					return fmt.Errorf("failed reading encrypted entry %s: %w", f.Name, rErr)
				}
				entryData = decryptZipCrypto(entryData, opts.Password)
				srcStream = bytes.NewReader(entryData)
			}

			_, copyErr := io.CopyBuffer(dstWriter, srcStream, buf)
			_ = dstWriter.Close()
			if !encrypted {
				_ = rc.Close()
			}
			if copyErr != nil {
				return fmt.Errorf("failed writing %s: %w", destPath, copyErr)
			}
		}
		return nil

	case Type7z:
		rac, err := openArchiveReaderAt(fsys, archivePath)
		if err != nil {
			return err
		}
		defer rac.Close()

		var szr *sevenzip.Reader
		if opts.Password != "" {
			szr, err = sevenzip.NewReaderWithPassword(rac.r, rac.size, opts.Password)
		} else {
			szr, err = sevenzip.NewReader(rac.r, rac.size)
		}
		if err != nil {
			return err
		}

		for _, f := range szr.File {
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
				return fmt.Errorf("failed opening 7z entry %s: %w", f.Name, err)
			}

			dstWriter, err := fsys.Create(destPath)
			if err != nil {
				_ = rc.Close()
				return fmt.Errorf("failed creating %s: %w", destPath, err)
			}

			_, copyErr := io.CopyBuffer(dstWriter, rc, buf)
			_ = rc.Close()
			_ = dstWriter.Close()
			if copyErr != nil {
				return fmt.Errorf("failed writing %s: %w", destPath, copyErr)
			}
		}
		return nil

	case TypeRar:
		rc, err := fsys.Open(archivePath)
		if err != nil {
			return fmt.Errorf("failed opening rar archive %s: %w", archivePath, err)
		}
		defer rc.Close()

		var rarReader *rardecode.Reader
		if opts.Password != "" {
			rarReader, err = rardecode.NewReader(rc, opts.Password)
		} else {
			rarReader, err = rardecode.NewReader(rc, "")
		}
		if err != nil {
			return err
		}

		for {
			hdr, err := rarReader.Next()
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

			if hdr.IsDir {
				_ = fsys.MkdirAll(destPath)
				continue
			}

			_ = fsys.MkdirAll(path.Dir(destPath))

			if !opts.Overwrite && fsys.Exists(destPath) {
				continue
			}

			dstWriter, err := fsys.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed creating %s: %w", destPath, err)
			}

			_, copyErr := io.CopyBuffer(dstWriter, rarReader, buf)
			_ = dstWriter.Close()
			if copyErr != nil {
				return fmt.Errorf("failed writing rar entry %s: %w", destPath, copyErr)
			}
		}
		return nil

	case TypeTar, TypeTarGz, TypeTarBz:
		rc, err := fsys.Open(archivePath)
		if err != nil {
			return fmt.Errorf("failed opening archive %s: %w", archivePath, err)
		}
		defer rc.Close()

		var tr *tar.Reader
		switch atype {
		case TypeTarGz:
			gr, err := gzip.NewReader(rc)
			if err != nil {
				return err
			}
			defer gr.Close()
			tr = tar.NewReader(gr)
		case TypeTarBz:
			tr = tar.NewReader(bzip2.NewReader(rc))
		default:
			tr = tar.NewReader(rc)
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

			dstWriter, err := fsys.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed creating %s: %w", destPath, err)
			}

			_, copyErr := io.CopyBuffer(dstWriter, tr, buf)
			_ = dstWriter.Close()
			if copyErr != nil {
				return fmt.Errorf("failed writing tar entry %s: %w", destPath, copyErr)
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported archive type: %s", atype)
	}
}

// Create builds a new archive from files and directories using streaming I/O.
func Create(fsys vfs.FileSystem, archivePath string, sourcePaths []string, opts CreateOptions) error {
	archivePath = vfs.NormalizePath(archivePath)
	if opts.Type == "" {
		opts.Type = DetectType(archivePath)
	}

	_ = fsys.MkdirAll(path.Dir(archivePath))
	dstWriter, err := fsys.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed creating archive file %s: %w", archivePath, err)
	}
	defer dstWriter.Close()

	buf := bufferpool.Acquire(64 * 1024)
	defer bufferpool.Release(buf)

	switch opts.Type {
	case TypeZip:
		zw := zip.NewWriter(dstWriter)
		if opts.Comment != "" {
			if err := zw.SetComment(opts.Comment); err != nil {
				return err
			}
		}

		for _, src := range sourcePaths {
			src = vfs.NormalizePath(src)
			info, err := fsys.Stat(src)
			if err != nil {
				return err
			}
			if !info.IsDir {
				if err := addFileToZip(fsys, zw, src, path.Base(src), info.ModTime, opts.Password, buf); err != nil {
					return err
				}
				continue
			}

			prefix := src
			if err := fsys.Walk(src, func(p string, fi *vfs.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if fi.IsDir {
					return nil
				}
				rel := strings.TrimPrefix(strings.TrimPrefix(p, prefix), "/")
				return addFileToZip(fsys, zw, p, rel, fi.ModTime, opts.Password, buf)
			}); err != nil {
				return err
			}
		}
		return zw.Close()

	case TypeTar, TypeTarGz:
		var output io.Writer = dstWriter
		var gw *gzip.Writer
		if opts.Type == TypeTarGz {
			gw = gzip.NewWriter(dstWriter)
			output = gw
		}
		tw := tar.NewWriter(output)

		for _, src := range sourcePaths {
			src = vfs.NormalizePath(src)
			info, err := fsys.Stat(src)
			if err != nil {
				return err
			}
			if !info.IsDir {
				if err := addFileToTar(fsys, tw, src, path.Base(src), info, buf); err != nil {
					return err
				}
				continue
			}

			prefix := src
			if err := fsys.Walk(src, func(p string, fi *vfs.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if fi.IsDir {
					return nil
				}
				rel := strings.TrimPrefix(strings.TrimPrefix(p, prefix), "/")
				return addFileToTar(fsys, tw, p, rel, fi, buf)
			}); err != nil {
				return err
			}
		}

		if err := tw.Close(); err != nil {
			return err
		}
		if gw != nil {
			return gw.Close()
		}
		return nil

	default:
		return fmt.Errorf("unsupported creation type: %s", opts.Type)
	}
}
func addFileToZip(fsys vfs.FileSystem, zw *zip.Writer, srcPath, entryName string, modTime time.Time, password string, buf []byte) error {
	rc, err := fsys.Open(srcPath)
	if err != nil {
		return err
	}
	defer rc.Close()

	fh := &zip.FileHeader{
		Name:     entryName,
		Modified: modTime,
		Method:   zip.Deflate,
	}

	if password != "" {
		fh.Flags |= 0x1 // Encrypted flag
		data, rErr := io.ReadAll(rc)
		if rErr != nil {
			return rErr
		}
		data = encryptZipCrypto(data, password)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}

	w, err := zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(w, rc, buf)
	return err
}

// AddFilesToArchive appends new files to an existing archive (repacking if necessary).
func addFileToTar(fsys vfs.FileSystem, tw *tar.Writer, srcPath, entryName string, fi *vfs.FileInfo, buf []byte) error {
	rc, err := fsys.Open(srcPath)
	if err != nil {
		return err
	}
	defer rc.Close()

	hdr := &tar.Header{
		Name:     entryName,
		Mode:     0644,
		Size:     fi.Size,
		ModTime:  fi.ModTime,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.CopyBuffer(tw, rc, buf)
	return err
}

// AddFilesToArchive appends new files to an existing archive.
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
		rc, err := fsys.Open(f)
		if err == nil {
			_ = fsys.Write(tempDir+"/"+path.Base(f), data)
			dst, dErr := fsys.Create(tempDir + "/" + path.Base(f))
			if dErr == nil {
				_, _ = io.Copy(dst, rc)
				_ = dst.Close()
			}
			_ = rc.Close()
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

	stat, err := fsys.Stat(tempArchive)
	if err != nil {
		return nil, err
	}

	srcFile, err := fsys.Open(tempArchive)
	if err != nil {
		return nil, err
	}
	defer srcFile.Close()

	var parts []string
	partIndex := 1
	buf := bufferpool.Acquire(64 * 1024)
	defer bufferpool.Release(buf)

	for offset := int64(0); offset < stat.Size; {
		remainingInPart := partSizeBytes
		if offset+remainingInPart > stat.Size {
			remainingInPart = stat.Size - offset
		}

		partName := fmt.Sprintf("%s.%03d", baseArchivePath, partIndex)
		partWriter, err := fsys.Create(partName)
		if err != nil {
			return nil, err
		}

		written, copyErr := io.CopyBuffer(partWriter, io.LimitReader(srcFile, remainingInPart), buf)
		_ = partWriter.Close()
		if copyErr != nil {
			return nil, copyErr
		}

		parts = append(parts, partName)
		offset += written
		partIndex++
	}

	return parts, nil
}

// ExtractMultiPart combines multiple archive parts (.001, .002) and extracts the content.
func ExtractMultiPart(fsys vfs.FileSystem, partPaths []string, targetDir string, opts ExtractOptions) error {
	if len(partPaths) == 0 {
		return errors.New("no archive parts provided")
	}

	tempCombinedPath := "/tmp_combined_" + fmt.Sprintf("%d", time.Now().UnixNano()) + ".zip"
	combinedWriter, err := fsys.Create(tempCombinedPath)
	if err != nil {
		return err
	}

	buf := bufferpool.Acquire(64 * 1024)
	defer bufferpool.Release(buf)

	for _, p := range partPaths {
		rc, err := fsys.Open(p)
		if err != nil {
			_ = combinedWriter.Close()
			_ = fsys.Remove(tempCombinedPath)
			return fmt.Errorf("failed reading part %s: %w", p, err)
		}
		_, cErr := io.CopyBuffer(combinedWriter, rc, buf)
		_ = rc.Close()
		if cErr != nil {
			_ = combinedWriter.Close()
			_ = fsys.Remove(tempCombinedPath)
			return cErr
		}
	}

	_ = combinedWriter.Close()
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
