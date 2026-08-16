package main

import (
	"log"
	"net/url"
	"os"
	"path/filepath"

	"freebox/proxy"
	"freebox/vfs"
)

func main() {
	smbShareFile := `\\127.0.0.1\E$\Tut\Udemy - Building 2D Games with Ebitengen in Go (Golang)\1 - Introduction\1 -Introduction.mp4`

	// Check if file exists via SMB UNC path or fallback local path
	targetPath := smbShareFile
	if _, err := os.Stat(targetPath); err != nil {
		localPath := `E:\Tut\Udemy - Building 2D Games with Ebitengen in Go (Golang)\1 - Introduction\1 -Introduction.mp4`
		if _, errLocal := os.Stat(localPath); errLocal == nil {
			targetPath = localPath
		} else {
			log.Fatalf("Target file not found at %s or %s: %v", smbShareFile, localPath, err)
		}
	}

	fi, err := os.Stat(targetPath)
	if err != nil {
		log.Fatalf("Stat error on %s: %v", targetPath, err)
	}

	log.Printf("Target File Located: %s (Size: %d bytes / %.2f MB)", targetPath, fi.Size(), float64(fi.Size())/(1024*1024))

	// Create OSFS backed Source directly from the SMB file path
	dir := filepath.Dir(targetPath)
	filename := filepath.Base(targetPath)

	osfs := vfs.NewOSFS(dir)
	vfsSource, err := proxy.NewVFSSource(osfs, "/"+filename, true)
	if err != nil {
		log.Fatalf("Failed to create VFS source: %v", err)
	}

	server := proxy.NewStreamServer()
	server.RegisterSource(filename, vfsSource)
	server.RegisterSource("intro.mp4", vfsSource) // convenient alias

	port := ":8090"
	encodedName := url.PathEscape(filename)
	log.Printf("Starting File Stream Proxy on %s...", port)
	log.Printf("Direct Stream URL: http://127.0.0.1%s/stream/%s", port, encodedName)
	log.Printf("Alias Stream URL:  http://127.0.0.1%s/stream/intro.mp4", port)

	if err := server.ListenAndServe(port); err != nil {
		log.Fatalf("Server stopped: %v", err)
	}
}
