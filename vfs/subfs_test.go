package vfs

import (
	"testing"
)

func TestSubFS(t *testing.T) {
	mem := NewMemFS()
	_ = mem.MkdirAll("/remote/root/subfolder")
	_ = mem.Write("/remote/root/file1.txt", []byte("hello from sub root"))
	_ = mem.Write("/remote/root/subfolder/file2.txt", []byte("inside subfolder"))
	_ = mem.Write("/other/outside.txt", []byte("outside file"))

	sub := NewSubFS(mem, "/remote/root")

	// Test root existence
	if !sub.Exists("/") {
		t.Errorf("expected root / to exist in SubFS")
	}

	// Test read relative to custom root
	data, err := sub.Read("/file1.txt")
	if err != nil || string(data) != "hello from sub root" {
		t.Fatalf("failed reading /file1.txt: %v, got %s", err, string(data))
	}

	// Test Stat
	st, err := sub.Stat("/file1.txt")
	if err != nil || st.Path != "/file1.txt" {
		t.Fatalf("expected st.Path = /file1.txt, got %v", st)
	}

	// Test ReadDir
	entries, err := sub.ReadDir("/")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	// Test Write & MkdirAll
	if err := sub.Write("/subfolder/new.txt", []byte("new content")); err != nil {
		t.Fatalf("Write in SubFS failed: %v", err)
	}

	// Verify it was written in parent at correct path
	parentData, err := mem.Read("/remote/root/subfolder/new.txt")
	if err != nil || string(parentData) != "new content" {
		t.Fatalf("parent file mismatch: %v, got %s", err, string(parentData))
	}

	// Test Walk
	var walked []string
	err = sub.Walk("/", func(p string, info *FileInfo, err error) error {
		if err == nil {
			walked = append(walked, p)
		}
		return nil
	})
	if err != nil || len(walked) < 3 {
		t.Fatalf("Walk failed: %v, walked=%v", err, walked)
	}
	if walked[0] != "/" {
		t.Errorf("expected first walked path to be /, got %s", walked[0])
	}
}

