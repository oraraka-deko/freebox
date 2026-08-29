package transfer

import (
	"errors"
	"path/filepath"
	"testing"

	"freebox/storage"
)

func TestJournalPersistsAppendOnlyVerifiedBoundary(t *testing.T) {
	db, err := storage.Open(storage.Config{Path: filepath.Join(t.TempDir(), "freebox.db"), Passphrase: "test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	journal := NewJournal(db)
	checkpoint := Checkpoint{ID: "copy-1", SrcPath: "/source", DstPath: "/dest", ChunkSize: 4}
	if err := journal.SaveCheckpoint(checkpoint); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if err := journal.Append(ChunkRecord{TransferID: "copy-1", Sequence: 1, Offset: 0, Size: 4, SHA256: "one", State: ChunkVerified}); err != nil {
		t.Fatalf("append verified chunk: %v", err)
	}
	if err := journal.Append(ChunkRecord{TransferID: "copy-1", Sequence: 2, Offset: 4, Size: 4, SHA256: "two", State: ChunkVerified}); err != nil {
		t.Fatalf("append verified chunk: %v", err)
	}
	if err := journal.Append(ChunkRecord{TransferID: "copy-1", Sequence: 3, Offset: 8, Size: 4, State: ChunkWritten}); err != nil {
		t.Fatalf("append partial chunk: %v", err)
	}
	if err := journal.Append(ChunkRecord{TransferID: "copy-1", Sequence: 3, Offset: 8, Size: 4, State: ChunkVerified}); !errors.Is(err, storage.ErrKeyExists) {
		t.Fatalf("append duplicate = %v, want ErrKeyExists", err)
	}

	boundary, err := journal.LastVerifiedBoundary("copy-1")
	if err != nil {
		t.Fatalf("verified boundary: %v", err)
	}
	if boundary != 8 {
		t.Fatalf("verified boundary = %d, want 8", boundary)
	}
	loaded, err := journal.LoadCheckpoint("copy-1")
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if loaded.DstPath != checkpoint.DstPath {
		t.Fatalf("loaded checkpoint = %+v", loaded)
	}
}
