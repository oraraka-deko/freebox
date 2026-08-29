package vfs

import (
	"context"
	"io"
	"testing"
)

func TestOpenFileCapabilityContract(t *testing.T) {
	tests := []struct {
		name string
		fs   FileSystem
	}{
		{name: "memory", fs: NewMemFS()},
		{name: "local", fs: NewOSFS(t.TempDir())},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.fs.Capabilities().Has(CapRangeRead | CapRandomWrite | CapTruncate) {
				t.Fatal("filesystem did not advertise required native access capabilities")
			}
			f, err := tt.fs.OpenFile(context.Background(), "/file.bin", WriteOnly())
			if err != nil {
				t.Fatalf("open for write: %v", err)
			}
			writer, ok := f.(WriteAtFile)
			if !ok {
				t.Fatal("write handle does not expose native random writes")
			}
			if _, err := writer.WriteAt([]byte("world"), 6); err != nil {
				t.Fatalf("write at: %v", err)
			}
			if _, err := writer.WriteAt([]byte("hello"), 0); err != nil {
				t.Fatalf("write at: %v", err)
			}
			if err := writer.Truncate(11); err != nil {
				t.Fatalf("truncate: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close writer: %v", err)
			}

			f, err = tt.fs.OpenFile(context.Background(), "/file.bin", ReadOnly())
			if err != nil {
				t.Fatalf("open for read: %v", err)
			}
			reader, ok := f.(ReadAtFile)
			if !ok {
				t.Fatal("read handle does not expose native random access")
			}
			buf := make([]byte, 5)
			if _, err := reader.ReadAt(buf, 6); err != nil && err != io.EOF {
				t.Fatalf("read at: %v", err)
			}
			if got := string(buf); got != "world" {
				t.Fatalf("range read = %q, want world", got)
			}
			_ = reader.Close()
		})
	}
}
