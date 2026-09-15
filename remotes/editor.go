package remotes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"freebox/storage"
	"freebox/vfs"
)

var (
	ErrConflict        = errors.New("remote file has been modified concurrently")
	ErrSessionClosed   = errors.New("edit session is closed")
	ErrSessionNotFound = errors.New("edit session not found")
)

// EditMode defines the editing strategy for remote files.
type EditMode string

const (
	// EditModeDirect reads and writes directly from/to remote storage.
	// Best suited for small files and high-speed, reliable networks.
	EditModeDirect EditMode = "direct"

	// EditModeLocalCopy downloads a working copy locally, performs local edits with zero latency,
	// and atomically replaces the remote file on commit.
	// Best suited for unstable networks, large files, or multi-step edits.
	EditModeLocalCopy EditMode = "local_copy"
)

// EditSession represents an active file editing session.
type EditSession struct {
	ID             string
	Mode           EditMode
	RemoteFS       vfs.FileSystem
	MountName      string
	RemotePath     string
	LocalPath      string
	OriginalSHA256 string
	CurrentSHA256  string
	IsDirty        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	closed         bool
	mediaCache     *storage.MediaCache
	mu             sync.RWMutex
}

// EditorManager coordinates remote file editing sessions across direct and local-copy modes.
type EditorManager struct {
	sessions   map[string]*EditSession
	mediaCache *storage.MediaCache
	scratchDir string
	mu         sync.RWMutex
}

// NewEditorManager creates a new remote file EditorManager.
func NewEditorManager(mediaCache *storage.MediaCache, scratchDir string) *EditorManager {
	if scratchDir == "" {
		scratchDir = filepath.Join(os.TempDir(), "freebox_editor_scratch")
	}
	_ = os.MkdirAll(scratchDir, 0700)

	return &EditorManager{
		sessions:   make(map[string]*EditSession),
		mediaCache: mediaCache,
		scratchDir: scratchDir,
	}
}

// Open initializes a new editing session for a remote file.
func (m *EditorManager) Open(ctx context.Context, fsys vfs.FileSystem, mountName, remotePath string, mode EditMode) (*EditSession, error) {
	if fsys == nil {
		return nil, errors.New("filesystem is required")
	}
	if mode == "" {
		mode = EditModeDirect
	}

	remotePath = vfs.NormalizePath(remotePath)
	sessionID := fmt.Sprintf("edit-%d-%s", time.Now().UnixNano(), filepath.Base(remotePath))

	session := &EditSession{
		ID:         sessionID,
		Mode:       mode,
		RemoteFS:   fsys,
		MountName:  mountName,
		RemotePath: remotePath,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		mediaCache: m.mediaCache,
	}

	// Fetch initial data from remote if file exists
	var initialData []byte
	if fsys.Exists(remotePath) {
		data, err := fsys.Read(remotePath)
		if err != nil {
			return nil, fmt.Errorf("failed reading remote file %s: %w", remotePath, err)
		}
		initialData = data
		h := sha256.Sum256(data)
		session.OriginalSHA256 = hex.EncodeToString(h[:])
		session.CurrentSHA256 = session.OriginalSHA256
	}

	if mode == EditModeLocalCopy {
		sessionDir := filepath.Join(m.scratchDir, sessionID)
		_ = os.MkdirAll(sessionDir, 0700)
		session.LocalPath = filepath.Join(sessionDir, filepath.Base(remotePath))
		if err := os.WriteFile(session.LocalPath, initialData, 0600); err != nil {
			return nil, fmt.Errorf("failed creating local working copy: %w", err)
		}
	}

	// Save draft metadata in cache
	if m.mediaCache != nil {
		_ = m.mediaCache.SaveEditDraft(storage.EditDraft{
			SessionID:      sessionID,
			Mount:          mountName,
			RemotePath:     remotePath,
			Mode:           string(mode),
			LocalPath:      session.LocalPath,
			OriginalSHA256: session.OriginalSHA256,
			CurrentSHA256:  session.CurrentSHA256,
			DraftData:      initialData,
			IsDirty:        false,
			CreatedAt:      session.CreatedAt,
			UpdatedAt:      session.UpdatedAt,
		})
	}

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	return session, nil
}

// GetSession retrieves an active editing session.
func (m *EditorManager) GetSession(sessionID string) (*EditSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[sessionID]
	return sess, ok
}

// CloseSession commits or discards an editing session and removes temporary scratch files.
func (m *EditorManager) CloseSession(ctx context.Context, sessionID string, commit bool) error {
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if !ok {
		return ErrSessionNotFound
	}

	if commit {
		if err := session.Commit(ctx); err != nil {
			return err
		}
	}

	return session.Discard()
}

// ListActiveSessions returns all currently open editing sessions.
func (m *EditorManager) ListActiveSessions() []*EditSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	results := make([]*EditSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		results = append(results, s)
	}
	return results
}

// --- EditSession Methods ---

// Read returns the current contents of the edited file.
func (s *EditSession) Read() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrSessionClosed
	}

	if s.Mode == EditModeLocalCopy && s.LocalPath != "" {
		return os.ReadFile(s.LocalPath)
	}

	return s.RemoteFS.Read(s.RemotePath)
}

// Write updates the content of the edited file.
func (s *EditSession) Write(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSessionClosed
	}

	h := sha256.Sum256(data)
	newHash := hex.EncodeToString(h[:])
	s.CurrentSHA256 = newHash
	s.IsDirty = (newHash != s.OriginalSHA256)
	s.UpdatedAt = time.Now()

	if s.Mode == EditModeDirect {
		// Write directly to remote
		if err := s.RemoteFS.Write(s.RemotePath, data); err != nil {
			return fmt.Errorf("direct write failed: %w", err)
		}
		s.OriginalSHA256 = newHash
		s.IsDirty = false
	} else {
		// Write to local working copy
		if s.LocalPath != "" {
			if err := os.WriteFile(s.LocalPath, data, 0600); err != nil {
				return fmt.Errorf("local working copy write failed: %w", err)
			}
		}
	}

	// Update draft store for crash / disconnect resilience
	if s.mediaCache != nil {
		_ = s.mediaCache.SaveEditDraft(storage.EditDraft{
			SessionID:      s.ID,
			Mount:          s.MountName,
			RemotePath:     s.RemotePath,
			Mode:           string(s.Mode),
			LocalPath:      s.LocalPath,
			OriginalSHA256: s.OriginalSHA256,
			CurrentSHA256:  s.CurrentSHA256,
			DraftData:      data,
			IsDirty:        s.IsDirty,
			CreatedAt:      s.CreatedAt,
			UpdatedAt:      s.UpdatedAt,
		})
	}

	return nil
}

// Commit pushes local working edits to the remote server with conflict detection.
func (s *EditSession) Commit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSessionClosed
	}

	if s.Mode == EditModeDirect {
		return nil // already synced
	}

	// Conflict Check: verify remote hasn't changed since session start
	if s.OriginalSHA256 != "" && s.RemoteFS.Exists(s.RemotePath) {
		remoteData, err := s.RemoteFS.Read(s.RemotePath)
		if err == nil {
			rh := sha256.Sum256(remoteData)
			remoteHash := hex.EncodeToString(rh[:])
			if remoteHash != s.OriginalSHA256 {
				return ErrConflict
			}
		}
	}

	// Read local copy data
	localData, err := os.ReadFile(s.LocalPath)
	if err != nil {
		return fmt.Errorf("failed reading local copy for upload: %w", err)
	}

	// Push to remote storage
	if err := s.RemoteFS.Write(s.RemotePath, localData); err != nil {
		return fmt.Errorf("remote upload failed: %w", err)
	}

	s.OriginalSHA256 = s.CurrentSHA256
	s.IsDirty = false
	s.UpdatedAt = time.Now()

	if s.mediaCache != nil {
		_ = s.mediaCache.SaveEditDraft(storage.EditDraft{
			SessionID:      s.ID,
			Mount:          s.MountName,
			RemotePath:     s.RemotePath,
			Mode:           string(s.Mode),
			LocalPath:      s.LocalPath,
			OriginalSHA256: s.OriginalSHA256,
			CurrentSHA256:  s.CurrentSHA256,
			IsDirty:        false,
			UpdatedAt:      s.UpdatedAt,
		})
	}

	return nil
}

// Discard cleans up temporary scratch files and marks the session as closed.
func (s *EditSession) Discard() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	if s.LocalPath != "" {
		_ = os.RemoveAll(filepath.Dir(s.LocalPath))
	}
	if s.mediaCache != nil {
		_ = s.mediaCache.DeleteEditDraft(s.ID)
	}
	return nil
}
