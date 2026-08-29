package transfer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"freebox/storage"
)

// ChunkState records the durable state of one attempted transfer chunk.
type ChunkState string

const (
	ChunkPrepared   ChunkState = "PREPARED"
	ChunkWritten    ChunkState = "WRITTEN"
	ChunkVerified   ChunkState = "VERIFIED"
	ChunkRolledBack ChunkState = "ROLLED_BACK"
)

// ChunkRecord is an immutable, append-only event in a transfer's recovery log.
type ChunkRecord struct {
	TransferID string     `json:"transfer_id"`
	Sequence   uint64     `json:"sequence"`
	Offset     int64      `json:"offset"`
	Size       int64      `json:"size"`
	SHA256     string     `json:"sha256,omitempty"`
	State      ChunkState `json:"state"`
	Error      string     `json:"error,omitempty"`
	At         time.Time  `json:"at"`
}

// Journal persists transfer checkpoints and immutable chunk events.
type Journal struct {
	db *storage.DB
}

func NewJournal(db *storage.DB) *Journal { return &Journal{db: db} }

func checkpointKey(id string) string { return id }
func chunkKey(id string, sequence uint64) string {
	// A fixed-width sequence makes lexical BBolt key order journal order.
	return fmt.Sprintf("%s/%020d", id, sequence)
}

func chunkPrefix(id string) string { return id + "/" }

// SaveCheckpoint persists mutable transfer metadata separately from the log.
func (j *Journal) SaveCheckpoint(cp Checkpoint) error {
	if j == nil || j.db == nil {
		return nil
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return j.db.PutEncrypted(storage.BucketTransfers, checkpointKey(cp.ID), data)
}

func (j *Journal) LoadCheckpoint(id string) (Checkpoint, error) {
	if j == nil || j.db == nil {
		return Checkpoint{}, storage.ErrNotFound
	}
	data, err := j.db.GetDecrypted(storage.BucketTransfers, checkpointKey(id))
	if err != nil {
		return Checkpoint{}, err
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode transfer checkpoint: %w", err)
	}
	return checkpoint, nil
}

// Append writes an event once. Reusing a sequence returns storage.ErrKeyExists.
func (j *Journal) Append(record ChunkRecord) error {
	if j == nil || j.db == nil {
		return nil
	}
	if record.TransferID == "" {
		return fmt.Errorf("transfer ID is required")
	}
	if record.Size < 0 || record.Offset < 0 {
		return fmt.Errorf("invalid chunk range")
	}
	if record.At.IsZero() {
		record.At = time.Now().UTC()
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return j.db.PutEncryptedIfAbsent(storage.BucketTransferLog, chunkKey(record.TransferID, record.Sequence), data)
}

// Records returns one transfer's events in their durable sequence order.
func (j *Journal) Records(id string) ([]ChunkRecord, error) {
	if j == nil || j.db == nil {
		return nil, nil
	}
	records, err := j.db.ListDecrypted(storage.BucketTransferLog)
	if err != nil {
		return nil, err
	}
	prefix := chunkPrefix(id)
	result := make([]ChunkRecord, 0)
	for key, data := range records {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var record ChunkRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode transfer log %q: %w", key, err)
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, k int) bool { return result[i].Sequence < result[k].Sequence })
	return result, nil
}

// LastVerifiedBoundary returns the exclusive end offset of the contiguous
// verified prefix. A gap, partial chunk, or rollback never advances it.
func (j *Journal) LastVerifiedBoundary(id string) (int64, error) {
	records, err := j.Records(id)
	if err != nil {
		return 0, err
	}
	var boundary int64
	for _, record := range records {
		if record.State != ChunkVerified || record.Offset != boundary {
			break
		}
		boundary += record.Size
	}
	return boundary, nil
}
