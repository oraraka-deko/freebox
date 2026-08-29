package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TaskHistoryRecord represents an immutable or final record of an executed task/action.
type TaskHistoryRecord struct {
	ID             string            `json:"id"`
	Sequence       uint64            `json:"sequence"`
	Type           string            `json:"type"`
	Description    string            `json:"description"`
	Status         string            `json:"status"`
	SrcPath        string            `json:"src_path,omitempty"`
	DstPath        string            `json:"dst_path,omitempty"`
	BytesProcessed int64             `json:"bytes_processed"`
	TotalBytes     int64             `json:"total_bytes"`
	Error          string            `json:"error,omitempty"`
	StartedAt      time.Time         `json:"started_at"`
	EndedAt        time.Time         `json:"ended_at"`
	DurationMs     int64             `json:"duration_ms"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	UndoAction     string            `json:"undo_action,omitempty"`
}

// TaskHistoryStore provides fast and durable access to past operations and task actions.
type TaskHistoryStore struct {
	db      *DB
	seq     uint64
	mu      sync.RWMutex
}

// NewTaskHistoryStore creates a new TaskHistoryStore.
func NewTaskHistoryStore(db *DB) *TaskHistoryStore {
	store := &TaskHistoryStore{
		db:  db,
		seq: uint64(time.Now().UnixNano()),
	}
	return store
}

func taskHistoryKey(seq uint64, id string) string {
	return fmt.Sprintf("%020d/%s", seq, id)
}

// AppendTask records a task into the persistent history log.
func (s *TaskHistoryStore) AppendTask(rec TaskHistoryRecord) error {
	if s.db == nil {
		return nil
	}

	seq := atomic.AddUint64(&s.seq, 1)
	rec.Sequence = seq
	if rec.StartedAt.IsZero() {
		rec.StartedAt = time.Now()
	}
	if rec.EndedAt.IsZero() {
		rec.EndedAt = time.Now()
	}
	if rec.DurationMs == 0 && !rec.EndedAt.Before(rec.StartedAt) {
		rec.DurationMs = rec.EndedAt.Sub(rec.StartedAt).Milliseconds()
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	key := taskHistoryKey(seq, rec.ID)
	return s.db.PutEncrypted(BucketHistory, key, data)
}

// GetRecentTasks returns the last N executed tasks in reverse chronological order (newest first).
// For example, GetRecentTasks(10) returns what we performed in the last 10 tasks.
func (s *TaskHistoryStore) GetRecentTasks(limit int) ([]TaskHistoryRecord, error) {
	if s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	rawMap, err := s.db.ListDecrypted(BucketHistory)
	if err != nil {
		return nil, err
	}

	var records []TaskHistoryRecord
	for _, data := range rawMap {
		var rec TaskHistoryRecord
		if err := json.Unmarshal(data, &rec); err == nil {
			records = append(records, rec)
		}
	}

	// Sort newest first (highest sequence first)
	sort.Slice(records, func(i, j int) bool {
		return records[i].Sequence > records[j].Sequence
	})

	if len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

// GetTask finds a task in history by its ID.
func (s *TaskHistoryStore) GetTask(id string) (*TaskHistoryRecord, error) {
	if s.db == nil {
		return nil, ErrNotFound
	}

	rawMap, err := s.db.ListDecrypted(BucketHistory)
	if err != nil {
		return nil, err
	}

	for key, data := range rawMap {
		if strings.HasSuffix(key, "/"+id) || key == id {
			var rec TaskHistoryRecord
			if err := json.Unmarshal(data, &rec); err == nil {
				return &rec, nil
			}
		}
	}

	return nil, ErrNotFound
}

// Count returns the total number of history records.
func (s *TaskHistoryStore) Count() (int, error) {
	if s.db == nil {
		return 0, nil
	}
	rawMap, err := s.db.ListDecrypted(BucketHistory)
	if err != nil {
		return 0, err
	}
	return len(rawMap), nil
}

// Clear removes all history records.
func (s *TaskHistoryStore) Clear() error {
	if s.db == nil {
		return nil
	}
	rawMap, err := s.db.List(BucketHistory)
	if err != nil {
		return err
	}
	for k := range rawMap {
		_ = s.db.Delete(BucketHistory, k)
	}
	return nil
}

// ClipboardItemRecord represents a single item staged in clipboard history.
type ClipboardItemRecord struct {
	Op       string            `json:"op"` // "COPY", "CUT", "CREATE", "DELETE"
	Mount    string            `json:"mount,omitempty"`
	Path     string            `json:"path"`
	IsDir    bool              `json:"is_dir"`
	Size     int64             `json:"size"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ClipboardStageRecord represents a snapshot of staged clipboard items.
type ClipboardStageRecord struct {
	StageID   string                `json:"stage_id"`
	Sequence  uint64                `json:"sequence"`
	Timestamp time.Time             `json:"timestamp"`
	Items     []ClipboardItemRecord `json:"items"`
}

// ClipboardHistoryStore provides persistent storage for clipboard stages across app restarts.
type ClipboardHistoryStore struct {
	db  *DB
	seq uint64
}

// NewClipboardHistoryStore creates a new ClipboardHistoryStore.
func NewClipboardHistoryStore(db *DB) *ClipboardHistoryStore {
	return &ClipboardHistoryStore{
		db:  db,
		seq: uint64(time.Now().UnixNano()),
	}
}

// SaveStage persists a staged clipboard snapshot.
func (c *ClipboardHistoryStore) SaveStage(items []ClipboardItemRecord) (string, error) {
	if c.db == nil || len(items) == 0 {
		return "", nil
	}

	seq := atomic.AddUint64(&c.seq, 1)
	stageID := fmt.Sprintf("clip-%d", time.Now().UnixNano())
	record := ClipboardStageRecord{
		StageID:   stageID,
		Sequence:  seq,
		Timestamp: time.Now(),
		Items:     items,
	}

	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}

	key := fmt.Sprintf("%020d", seq)
	if err := c.db.PutEncrypted(BucketClipboardHistory, key, data); err != nil {
		return "", err
	}

	return stageID, nil
}

// GetRecentStages returns recent clipboard stages (newest first).
func (c *ClipboardHistoryStore) GetRecentStages(limit int) ([]ClipboardStageRecord, error) {
	if c.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	rawMap, err := c.db.ListDecrypted(BucketClipboardHistory)
	if err != nil {
		return nil, err
	}

	var stages []ClipboardStageRecord
	for _, data := range rawMap {
		var st ClipboardStageRecord
		if err := json.Unmarshal(data, &st); err == nil {
			stages = append(stages, st)
		}
	}

	sort.Slice(stages, func(i, j int) bool {
		return stages[i].Sequence > stages[j].Sequence
	})

	if len(stages) > limit {
		stages = stages[:limit]
	}

	return stages, nil
}

// GetLatestStage returns the most recent clipboard stage.
func (c *ClipboardHistoryStore) GetLatestStage() (*ClipboardStageRecord, error) {
	stages, err := c.GetRecentStages(1)
	if err != nil {
		return nil, err
	}
	if len(stages) == 0 {
		return nil, ErrNotFound
	}
	return &stages[0], nil
}

// Clear clears clipboard history.
func (c *ClipboardHistoryStore) Clear() error {
	if c.db == nil {
		return nil
	}
	rawMap, err := c.db.List(BucketClipboardHistory)
	if err != nil {
		return err
	}
	for k := range rawMap {
		_ = c.db.Delete(BucketClipboardHistory, k)
	}
	return nil
}

