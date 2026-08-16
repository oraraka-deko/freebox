package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"freebox/storage"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrTokenExpired        = errors.New("session token expired")
	ErrTokenInvalid        = errors.New("invalid session token")
	ErrUserExists          = errors.New("user already exists")
	ErrUserNotFound        = errors.New("user not found")
)

// User represents a registered system user.
type User struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"` // "admin", "operator", "viewer"
	CreatedAt    time.Time `json:"created_at"`
}

// Session represents an active authenticated session with a temporary API key.
type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ManagerConfig configuration for the AuthManager.
type ManagerConfig struct {
	SessionTTL       time.Duration
	DefaultAdminUser string
	DefaultAdminPass string
}

// Manager handles user authentication, encrypted credential storage, and session lifecycles.
type Manager struct {
	db         *storage.DB
	sessionTTL time.Duration
	mu         sync.RWMutex
	cache      map[string]*Session
}

// NewManager creates a new AuthManager backed by the encrypted database.
func NewManager(db *storage.DB, cfg ManagerConfig) (*Manager, error) {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 24 * time.Hour
	}

	m := &Manager{
		db:         db,
		sessionTTL: cfg.SessionTTL,
		cache:      make(map[string]*Session),
	}

	// Load existing unexpired sessions into cache
	if db != nil {
		sessionsRaw, err := db.ListDecrypted(storage.BucketSessions)
		if err == nil {
			now := time.Now()
			for _, data := range sessionsRaw {
				var s Session
				if err := json.Unmarshal(data, &s); err == nil {
					if now.Before(s.ExpiresAt) {
						m.cache[s.Token] = &s
					} else {
						_ = db.Delete(storage.BucketSessions, s.Token)
					}
				}
			}
		}

		// Ensure default admin user if no users exist
		usersRaw, err := db.ListDecrypted(storage.BucketUsers)
		if err == nil && len(usersRaw) == 0 {
			adminUser := cfg.DefaultAdminUser
			if adminUser == "" {
				adminUser = "admin"
			}
			adminPass := cfg.DefaultAdminPass
			if adminPass == "" {
				adminPass = "admin123"
			}
			_ = m.CreateUser(adminUser, adminPass, "admin")
		}
	}

	return m, nil
}

// CreateUser registers a new user with bcrypt-hashed password in encrypted storage.
func (m *Manager) CreateUser(username, password, role string) error {
	if username == "" || password == "" {
		return errors.New("username and password cannot be empty")
	}
	if role == "" {
		role = "operator"
	}

	if m.db != nil {
		_, err := m.db.GetDecrypted(storage.BucketUsers, username)
		if err == nil {
			return ErrUserExists
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	user := User{
		Username:     username,
		PasswordHash: string(hash),
		Role:         role,
		CreatedAt:    time.Now(),
	}

	if m.db != nil {
		data, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return m.db.PutEncrypted(storage.BucketUsers, username, data)
	}

	return nil
}

// Authenticate verifies user credentials and issues a new temporary session token.
func (m *Manager) Authenticate(username, password string) (*Session, error) {
	if m.db == nil {
		return nil, errors.New("storage db unavailable")
	}

	userData, err := m.db.GetDecrypted(storage.BucketUsers, username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	var user User
	if err := json.Unmarshal(userData, &user); err != nil {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	// Generate random 32-byte token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)

	now := time.Now()
	session := &Session{
		Token:     token,
		Username:  user.Username,
		Role:      user.Role,
		CreatedAt: now,
		ExpiresAt: now.Add(m.sessionTTL),
	}

	sessionData, err := json.Marshal(session)
	if err != nil {
		return nil, err
	}

	if err := m.db.PutEncrypted(storage.BucketSessions, token, sessionData); err != nil {
		return nil, fmt.Errorf("failed storing session: %w", err)
	}

	m.mu.Lock()
	m.cache[token] = session
	m.mu.Unlock()

	return session, nil
}

// ValidateToken validates a temporary session API key.
func (m *Manager) ValidateToken(token string) (*Session, error) {
	if token == "" {
		return nil, ErrTokenInvalid
	}

	m.mu.RLock()
	s, found := m.cache[token]
	m.mu.RUnlock()

	if found {
		if time.Now().After(s.ExpiresAt) {
			_ = m.RevokeToken(token)
			return nil, ErrTokenExpired
		}
		return s, nil
	}

	// Fallback to DB check
	if m.db != nil {
		data, err := m.db.GetDecrypted(storage.BucketSessions, token)
		if err != nil {
			return nil, ErrTokenInvalid
		}
		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			return nil, ErrTokenInvalid
		}
		if time.Now().After(session.ExpiresAt) {
			_ = m.db.Delete(storage.BucketSessions, token)
			return nil, ErrTokenExpired
		}

		m.mu.Lock()
		m.cache[token] = &session
		m.mu.Unlock()
		return &session, nil
	}

	return nil, ErrTokenInvalid
}

// RevokeToken removes an active session key.
func (m *Manager) RevokeToken(token string) error {
	m.mu.Lock()
	delete(m.cache, token)
	m.mu.Unlock()

	if m.db != nil {
		return m.db.Delete(storage.BucketSessions, token)
	}
	return nil
}

// ListSessions returns all active sessions for an optional username filter.
func (m *Manager) ListSessions(username string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var res []*Session
	for _, s := range m.cache {
		if now.Before(s.ExpiresAt) {
			if username == "" || s.Username == username {
				res = append(res, s)
			}
		}
	}
	return res
}
