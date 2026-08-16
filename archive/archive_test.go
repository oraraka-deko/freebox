package archive

import (
	"testing"

	"freebox/vfs"
)

func TestArchiveCreatePreviewExtract(t *testing.T) {
	fs := vfs.NewMemFS()

	// Prepare files
	_ = fs.MkdirAll("/data/sub")
	_ = fs.Write("/data/file1.txt", []byte("File 1 contents"))
	_ = fs.Write("/data/sub/file2.txt", []byte("File 2 contents inside subfolder"))

	// 1. Test ZIP creation
	err := Create(fs, "/backup.zip", []string{"/data"}, CreateOptions{
		Type:     TypeZip,
		Password: "secret-password",
		Comment:  "Automated backup",
	})
	if err != nil {
		t.Fatalf("Create zip failed: %v", err)
	}

	// 2. Test ZIP preview without extraction
	entries, err := Preview(fs, "/backup.zip", "secret-password")
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries in preview, got %d", len(entries))
	}
	if !entries[0].Encrypted {
		t.Errorf("expected encrypted flag on entries")
	}

	// 3. Test ZIP extraction
	err = Extract(fs, "/backup.zip", "/extracted", ExtractOptions{
		Password:  "secret-password",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Verify extracted content
	data1, err := fs.Read("/extracted/file1.txt")
	if err != nil || string(data1) != "File 1 contents" {
		t.Errorf("extracted file1 content mismatch: %v, %s", err, string(data1))
	}
}

func TestArchiveTarGz(t *testing.T) {
	fs := vfs.NewMemFS()
	_ = fs.Write("/source/doc.txt", []byte("tar gz document"))

	err := Create(fs, "/archive.tar.gz", []string{"/source/doc.txt"}, CreateOptions{
		Type: TypeTarGz,
	})
	if err != nil {
		t.Fatalf("create tar.gz failed: %v", err)
	}

	entries, err := Preview(fs, "/archive.tar.gz", "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("preview tar.gz failed: %v, entries=%d", err, len(entries))
	}

	err = Extract(fs, "/archive.tar.gz", "/tar_out", ExtractOptions{})
	if err != nil {
		t.Fatalf("extract tar.gz failed: %v", err)
	}

	data, _ := fs.Read("/tar_out/doc.txt")
	if string(data) != "tar gz document" {
		t.Errorf("extracted tar content mismatch: %s", string(data))
	}
}

func TestArchiveMultiPart(t *testing.T) {
	fs := vfs.NewMemFS()
	_ = fs.Write("/big.txt", []byte("1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"))

	// Split into 20 byte parts
	parts, err := CreateMultiPart(fs, "/big.zip", []string{"/big.txt"}, 20, CreateOptions{Type: TypeZip})
	if err != nil {
		t.Fatalf("CreateMultiPart failed: %v", err)
	}
	if len(parts) <= 1 {
		t.Fatalf("expected multiple parts, got %d", len(parts))
	}

	// Extract multi-part
	err = ExtractMultiPart(fs, parts, "/multipart_extracted", ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractMultiPart failed: %v", err)
	}

	data, err := fs.Read("/multipart_extracted/big.txt")
	if err != nil || string(data) != "1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		t.Errorf("extracted multipart content mismatch: %s", string(data))
	}
}
