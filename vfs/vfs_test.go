package vfs

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	fbFtp "freebox/ftp"
	fbHttp "freebox/http"
)

func TestMemFSOperations(t *testing.T) {
	fs := NewMemFS()

	// Test Write & Read
	if err := fs.Write("/docs/readme.txt", []byte("hello vfs")); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	data, err := fs.Read("/docs/readme.txt")
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "hello vfs" {
		t.Errorf("unexpected content: %s", string(data))
	}

	// Test Stat
	info, err := fs.Stat("/docs/readme.txt")
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Size != int64(len("hello vfs")) || info.IsDir {
		t.Errorf("unexpected stat: %+v", info)
	}

	// Test ReadDir
	entries, err := fs.ReadDir("/docs")
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "readme.txt" {
		t.Errorf("unexpected entries: %+v", entries)
	}

	// Test Rename
	if err := fs.Rename("/docs/readme.txt", "/docs/guide.txt"); err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if fs.Exists("/docs/readme.txt") {
		t.Errorf("old path should not exist")
	}
	if !fs.Exists("/docs/guide.txt") {
		t.Errorf("new path should exist")
	}

	// Test Remove
	if err := fs.Remove("/docs/guide.txt"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if fs.Exists("/docs/guide.txt") {
		t.Errorf("removed path still exists")
	}
}

func TestVFSTransfer(t *testing.T) {
	srcFS := NewMemFS()
	dstFS := NewMemFS()

	_ = srcFS.Write("/photos/vacation.jpg", []byte("photo-data-1"))
	_ = srcFS.Write("/photos/family.png", []byte("photo-data-2"))
	_ = srcFS.Write("/music/song.mp3", []byte("audio-data-1"))
	_ = srcFS.Write("/music/track.flac", []byte("audio-data-2"))
	_ = srcFS.Write("/documents/report.pdf", []byte("pdf-data"))

	// 1. Test CopyFile
	if err := CopyFile(srcFS, "/documents/report.pdf", dstFS, "/backup/report.pdf"); err != nil {
		t.Fatalf("copyfile failed: %v", err)
	}
	pdfData, err := dstFS.Read("/backup/report.pdf")
	if err != nil || string(pdfData) != "pdf-data" {
		t.Errorf("failed reading copied file: %v, content=%s", err, string(pdfData))
	}

	// 2. Test TransferBatch (choosing specific files from Server A to Server B)
	chosenFiles := []string{
		"/photos/vacation.jpg",
		"/music/song.mp3",
	}
	if err := TransferBatch(srcFS, chosenFiles, dstFS, "/selected"); err != nil {
		t.Fatalf("transfer batch failed: %v", err)
	}

	if !dstFS.Exists("/selected/vacation.jpg") || !dstFS.Exists("/selected/song.mp3") {
		t.Errorf("batch files not found in destination")
	}

	// 3. Test TransferMatching (filter any type, e.g., only music files)
	musicFilter := func(fi *FileInfo) bool {
		return strings.HasSuffix(fi.Name, ".mp3") || strings.HasSuffix(fi.Name, ".flac")
	}
	transferred, err := TransferMatching(srcFS, dstFS, "/", "/music_only", musicFilter)
	if err != nil {
		t.Fatalf("transfer matching failed: %v", err)
	}
	if len(transferred) != 2 {
		t.Errorf("expected 2 music files transferred, got %d (%+v)", len(transferred), transferred)
	}
	if !dstFS.Exists("/music_only/music/song.mp3") || !dstFS.Exists("/music_only/music/track.flac") {
		t.Errorf("matched music files missing in destination")
	}
}

func TestCrossServerVFSIntegration(t *testing.T) {
	// Server A: FTP Server
	ftpRootDir := t.TempDir()
	srcFS := NewOSFS(ftpRootDir)
	_ = srcFS.Write("/source/album.flac", []byte("flac-audio-content"))
	_ = srcFS.Write("/source/cover.jpg", []byte("cover-image-content"))
	_ = srcFS.Write("/source/notes.txt", []byte("album-notes-content"))

	ftpCfg := fbFtp.DefaultConfig()
	ftpCfg.RootDir = ftpRootDir
	ftpServer := fbFtp.NewServer(ftpCfg)

	ftpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for FTP: %v", err)
	}
	go func() { _ = ftpServer.Serve(ftpLis) }()
	defer func() {
		_ = ftpServer.Shutdown(context.Background())
	}()

	// Server B: WebDAV Server
	webdavRootDir := t.TempDir()
	webdavCfg := fbHttp.DefaultWebDAVConfig()
	webdavCfg.RootDir = webdavRootDir
	webdavCfg.Prefix = "/webdav"
	webdavServer := fbHttp.NewWebDAVServer(webdavCfg)

	webdavLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for WebDAV: %v", err)
	}
	go func() { _ = webdavServer.Serve(webdavLis) }()
	defer func() {
		_ = webdavServer.Shutdown(context.Background())
	}()

	// WebDAV Client as VFS destination
	webdavClient := fbHttp.NewWebDAVClient("http://"+webdavLis.Addr().String()+"/webdav", "", "", 2*time.Second)
	dstVFS := NewWebDAVAdapter(webdavClient)

	// Filter and transfer only audio and image files from Server A (FTP storage) to Server B (WebDAV storage)
	mediaFilter := func(fi *FileInfo) bool {
		return strings.HasSuffix(fi.Name, ".flac") || strings.HasSuffix(fi.Name, ".jpg")
	}

	copiedFiles, err := TransferMatching(srcFS, dstVFS, "/source", "/media", mediaFilter)
	if err != nil {
		t.Fatalf("cross-server transfer failed: %v", err)
	}
	if len(copiedFiles) != 2 {
		t.Errorf("expected 2 files copied, got %d", len(copiedFiles))
	}

	// Verify files arrived on Server B (WebDAV)
	ctx := context.Background()
	res, err := webdavClient.Get(ctx, "/media/cover.jpg")
	if err != nil {
		t.Fatalf("failed to get transferred file from WebDAV server: %v", err)
	}
	defer res.Close()

	// Verify excluded file (notes.txt) was not copied
	_, err = webdavClient.Get(ctx, "/media/notes.txt")
	if err == nil {
		t.Errorf("notes.txt should not have been transferred")
	}
}
