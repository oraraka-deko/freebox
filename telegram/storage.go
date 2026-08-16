package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"freebox/storage"
)

var (
	ErrAccountNotFound = errors.New("telegram account not found")
	ErrAccountExists   = errors.New("telegram account already exists")
)

// SessionStorageManager manages Telegram account records and session files.
type SessionStorageManager struct {
	db      *storage.DB
	baseDir string
	mu      sync.RWMutex
}

// NewSessionStorageManager creates a new storage manager for Telegram sessions and accounts.
func NewSessionStorageManager(db *storage.DB, baseDir string) (*SessionStorageManager, error) {
	if baseDir == "" {
		baseDir = filepath.Join("data", "telegram")
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "sessions"), 0700); err != nil {
		return nil, fmt.Errorf("failed to create telegram session directory: %w", err)
	}

	return &SessionStorageManager{
		db:      db,
		baseDir: baseDir,
	}, nil
}

// SanitizePhone converts a phone number into a safe directory/file name.
func SanitizePhone(phone string) string {
	var out []rune
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "account"
	}
	return "phone-" + string(out)
}

// GetAccountDir returns the directory used to store session data and databases for an account.
func (s *SessionStorageManager) GetAccountDir(accountID int64, phone string) string {
	folder := ""
	if accountID > 0 {
		folder = fmt.Sprintf("acc-%d", accountID)
	} else if phone != "" {
		folder = SanitizePhone(phone)
	} else {
		folder = fmt.Sprintf("acc-%d", time.Now().UnixNano())
	}
	return filepath.Join(s.baseDir, "sessions", folder)
}

// SaveAccount stores account metadata in encrypted DB and on disk.
func (s *SessionStorageManager) SaveAccount(acc *Account) error {
	if acc == nil || acc.ID == 0 {
		return errors.New("invalid account data")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acc.UpdatedAt = time.Now()
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = acc.UpdatedAt
	}

	data, err := json.Marshal(acc)
	if err != nil {
		return fmt.Errorf("failed to marshal account: %w", err)
	}

	// Persist to encrypted database
	if s.db != nil {
		key := fmt.Sprintf("account:%d", acc.ID)
		if err := s.db.Put(storage.BucketTelegram, key, data); err != nil {
			return fmt.Errorf("failed to save account to db: %w", err)
		}
		// Also index by phone for fast lookup
		if acc.Phone != "" {
			phoneKey := fmt.Sprintf("phone:%s", acc.Phone)
			_ = s.db.Put(storage.BucketTelegram, phoneKey, []byte(strconv.FormatInt(acc.ID, 10)))
		}
	}

	// Also write meta.json into account session directory
	if acc.SessionDir != "" {
		_ = os.MkdirAll(acc.SessionDir, 0700)
		_ = os.WriteFile(filepath.Join(acc.SessionDir, "account.json"), data, 0600)
	}

	return nil
}

// GetAccount retrieves account by ID.
func (s *SessionStorageManager) GetAccount(id int64) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getAccountLocked(id)
}

func (s *SessionStorageManager) getAccountLocked(id int64) (*Account, error) {
	if s.db != nil {
		key := fmt.Sprintf("account:%d", id)
		data, err := s.db.Get(storage.BucketTelegram, key)
		if err == nil {
			var acc Account
			if err := json.Unmarshal(data, &acc); err == nil {
				return &acc, nil
			}
		}
	}

	// Fallback to searching disk directories
	accounts, err := s.listAccountsFromDisk()
	if err == nil {
		for _, acc := range accounts {
			if acc.ID == id {
				return acc, nil
			}
		}
	}

	return nil, ErrAccountNotFound
}

// GetAccountByPhone retrieves account by phone number.
func (s *SessionStorageManager) GetAccountByPhone(phone string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db != nil && phone != "" {
		phoneKey := fmt.Sprintf("phone:%s", phone)
		idBytes, err := s.db.Get(storage.BucketTelegram, phoneKey)
		if err == nil {
			id, err := strconv.ParseInt(string(idBytes), 10, 64)
			if err == nil {
				key := fmt.Sprintf("account:%d", id)
				data, err := s.db.Get(storage.BucketTelegram, key)
				if err == nil {
					var acc Account
					if err := json.Unmarshal(data, &acc); err == nil {
						return &acc, nil
					}
				}
			}
		}
	}

	// Fallback to disk
	accounts, err := s.listAccountsFromDisk()
	if err == nil {
		for _, acc := range accounts {
			if acc.Phone == phone {
				return acc, nil
			}
		}
	}

	return nil, ErrAccountNotFound
}

// ListAccounts returns all registered Telegram accounts.
func (s *SessionStorageManager) ListAccounts() ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	accountMap := make(map[int64]*Account)

	// Read from encrypted DB
	if s.db != nil {
		entries, err := s.db.List(storage.BucketTelegram)
		if err == nil {
			for k, v := range entries {
				if len(k) > 8 && k[:8] == "account:" {
					var acc Account
					if err := json.Unmarshal(v, &acc); err == nil && acc.ID != 0 {
						accountMap[acc.ID] = &acc
					}
				}
			}
		}
	}

	// Also merge from disk if any
	diskAccounts, _ := s.listAccountsFromDisk()
	for _, acc := range diskAccounts {
		if _, exists := accountMap[acc.ID]; !exists {
			accountMap[acc.ID] = acc
		}
	}

	result := make([]*Account, 0, len(accountMap))
	for _, acc := range accountMap {
		result = append(result, acc)
	}

	return result, nil
}

// DeleteAccount removes an account from DB and deletes its session folder.
func (s *SessionStorageManager) DeleteAccount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	acc, err := s.getAccountLocked(id)
	if err != nil && !errors.Is(err, ErrAccountNotFound) {
		return err
	}

	if s.db != nil {
		_ = s.db.Delete(storage.BucketTelegram, fmt.Sprintf("account:%d", id))
		if acc != nil && acc.Phone != "" {
			_ = s.db.Delete(storage.BucketTelegram, fmt.Sprintf("phone:%s", acc.Phone))
		}
	}

	if acc != nil && acc.SessionDir != "" {
		_ = os.RemoveAll(acc.SessionDir)
	}

	return nil
}

func (s *SessionStorageManager) listAccountsFromDisk() ([]*Account, error) {
	sessionsDir := filepath.Join(s.baseDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, err
	}

	var accounts []*Account
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(sessionsDir, entry.Name(), "account.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var acc Account
		if err := json.Unmarshal(data, &acc); err == nil && acc.ID != 0 {
			if acc.SessionDir == "" {
				acc.SessionDir = filepath.Join(sessionsDir, entry.Name())
			}
			accounts = append(accounts, &acc)
		}
	}
	return accounts, nil
}
