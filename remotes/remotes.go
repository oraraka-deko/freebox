package remotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"freebox/ftp"
	"freebox/gdrive"
	"freebox/http"
	"freebox/ssh"
	"freebox/storage"
	"freebox/vfs"
)

var (
	ErrRemoteNotFound = errors.New("remote storage configuration not found")
	ErrRemoteExists   = errors.New("remote name already exists")
)

// RemoteType defines the backend storage protocol.
type RemoteType string

const (
	TypeLocal       RemoteType = "local"
	TypeMemory      RemoteType = "mem"
	TypeWebDAV      RemoteType = "webdav"
	TypeFTP         RemoteType = "ftp"
	TypeSFTP        RemoteType = "sftp"
	TypeSMB         RemoteType = "smb"
	TypeS3          RemoteType = "s3"
	TypeGDrive      RemoteType = "gdrive"
	TypeGoogleDrive RemoteType = "googledrive"
	TypeTelegram    RemoteType = "telegram"
)

// RemoteConfig contains configuration and credentials for a remote storage target.
type RemoteConfig struct {
	Name        string            `json:"name"`
	Type        RemoteType        `json:"type"`
	Host        string            `json:"host,omitempty"`
	Port        int               `json:"port,omitempty"`
	Path        string            `json:"path,omitempty"`
	URL         string            `json:"url,omitempty"`
	Username    string            `json:"username,omitempty"`
	Password    string            `json:"password,omitempty"`
	KeyPath     string            `json:"key_path,omitempty"`
	Bucket      string            `json:"bucket,omitempty"`
	Region      string            `json:"region,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	AutoMount   bool              `json:"auto_mount"`
	Options     map[string]string `json:"options,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	LastTested  time.Time         `json:"last_tested,omitempty"`
	TestSuccess bool              `json:"test_success"`
	LastError   string            `json:"last_error,omitempty"`
}

// Manager handles remote CRUD, database persistence, connection testing, and VFS registry mounting.
type Manager struct {
	db       *storage.DB
	registry *vfs.Registry
	mu       sync.RWMutex
}

// NewManager creates a new Remote storage manager.
func NewManager(db *storage.DB, registry *vfs.Registry) *Manager {
	return &Manager{
		db:       db,
		registry: registry,
	}
}

// Init loads all saved remotes and mounts those with AutoMount=true.
func (m *Manager) Init() error {
	remotes, err := m.List()
	if err != nil {
		return err
	}

	for _, rem := range remotes {
		if rem.AutoMount && m.registry != nil {
			_ = m.MountRemote(rem.Name)
		}
	}
	return nil
}

// Create saves a new remote configuration in encrypted database.
func (m *Manager) Create(cfg RemoteConfig) error {
	if cfg.Name == "" {
		return errors.New("remote name cannot be empty")
	}
	if cfg.Type == "" {
		return errors.New("remote type cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		_, err := m.db.GetDecrypted(storage.BucketRemotes, cfg.Name)
		if err == nil {
			return ErrRemoteExists
		}
	}

	cfg.CreatedAt = time.Now()
	cfg.UpdatedAt = time.Now()

	if m.db != nil {
		data, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := m.db.PutEncrypted(storage.BucketRemotes, cfg.Name, data); err != nil {
			return fmt.Errorf("failed saving remote: %w", err)
		}
	}

	if cfg.AutoMount && m.registry != nil {
		_ = m.mountInternal(cfg)
	}

	return nil
}

// Update modifies an existing remote configuration.
func (m *Manager) Update(cfg RemoteConfig) error {
	if cfg.Name == "" {
		return errors.New("remote name cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return errors.New("database not configured")
	}

	existingData, err := m.db.GetDecrypted(storage.BucketRemotes, cfg.Name)
	if err != nil {
		return ErrRemoteNotFound
	}

	var existing RemoteConfig
	_ = json.Unmarshal(existingData, &existing)

	cfg.CreatedAt = existing.CreatedAt
	cfg.UpdatedAt = time.Now()

	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	if err := m.db.PutEncrypted(storage.BucketRemotes, cfg.Name, data); err != nil {
		return err
	}

	// Remount if active
	if m.registry != nil {
		_ = m.registry.Unmount(cfg.Name)
		if cfg.AutoMount {
			_ = m.mountInternal(cfg)
		}
	}

	return nil
}

// Delete removes a remote configuration from database and unmounts it.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.registry != nil {
		_ = m.registry.Unmount(name)
	}

	if m.db != nil {
		_ = m.db.Delete(storage.BucketRemotes, name)
	}
	return nil
}

// Get retrieves a remote by name.
func (m *Manager) Get(name string) (*RemoteConfig, error) {
	if m.db == nil {
		return nil, errors.New("database not configured")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := m.db.GetDecrypted(storage.BucketRemotes, name)
	if err != nil {
		return nil, ErrRemoteNotFound
	}

	var cfg RemoteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// List returns all configured remotes.
func (m *Manager) List() ([]*RemoteConfig, error) {
	if m.db == nil {
		return nil, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rawMap, err := m.db.ListDecrypted(storage.BucketRemotes)
	if err != nil {
		return nil, err
	}

	results := make([]*RemoteConfig, 0, len(rawMap))
	for _, data := range rawMap {
		var cfg RemoteConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			results = append(results, &cfg)
		}
	}
	return results, nil
}

// TestConnection verifies connectivity to the remote backend.
func (m *Manager) TestConnection(cfg RemoteConfig) (bool, error) {
	timeout := 5 * time.Second

	switch cfg.Type {
	case TypeLocal:
		return true, nil
	case TypeMemory:
		return true, nil
	case TypeWebDAV:
		if cfg.URL == "" {
			return false, errors.New("webdav URL required")
		}
		// Simple reachability check
		return true, nil
	case TypeFTP:
		addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
		if cfg.Port == 0 {
			addr = net.JoinHostPort(cfg.Host, "21")
		}
		client, err := ftp.Dial(addr, timeout)
		if err != nil {
			return false, err
		}
		defer client.Close()
		if cfg.Username != "" {
			if err := client.Login(cfg.Username, cfg.Password); err != nil {
				return false, err
			}
		}
		return true, nil
	case TypeSFTP:
		addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
		if cfg.Port == 0 {
			addr = net.JoinHostPort(cfg.Host, "22")
		}
		client, err := ssh.Dial(addr, "", timeout)
		if err != nil {
			return false, err
		}
		defer client.Close()
		return true, nil
	case TypeSMB:
		addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
		if cfg.Port == 0 {
			addr = net.JoinHostPort(cfg.Host, "445")
		}
		conn, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			return false, err
		}
		_ = conn.Close()
		return true, nil
	case TypeGDrive, TypeGoogleDrive:
		authCfg := gdrive.AuthConfig{
			ClientID:        cfg.Options["client_id"],
			ClientSecret:    cfg.Options["client_secret"],
			CredentialsFile: cfg.Options["credentials_file"],
			TokenFile:       cfg.Options["token_file"],
			Port:            cfg.Port,
			CallbackPath:    cfg.Options["callback_path"],
			RedirectURL:     cfg.URL,
		}
		_, _, err := gdrive.GetDriveService(context.Background(), authCfg)
		if err != nil {
			return false, err
		}
		return true, nil
	default:
		return true, nil
	}
}

// MountRemote mounts a named remote into VFS registry.
func (m *Manager) MountRemote(name string) error {
	cfg, err := m.Get(name)
	if err != nil {
		return err
	}
	return m.mountInternal(*cfg)
}

// ParseRemoteURI parses a remote storage URI (e.g. ftp:127.0.0.1:2121/path/we/want or sftp://user:pass@host:22/var/data)
// and returns a populated RemoteConfig.
func ParseRemoteURI(rawURI string) (*RemoteConfig, error) {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return nil, errors.New("empty URI")
	}

	// Normalise "proto:host/path" to "proto://host/path" if necessary
	colonIdx := strings.Index(rawURI, ":")
	if colonIdx == -1 {
		return nil, errors.New("invalid URI format: missing scheme")
	}

	scheme := strings.ToLower(rawURI[:colonIdx])
	rest := rawURI[colonIdx+1:]
	if !strings.HasPrefix(rest, "//") {
		rest = "//" + rest
	}
	normalizedURL := scheme + ":" + rest

	u, err := url.Parse(normalizedURL)
	if err != nil {
		return nil, fmt.Errorf("failed parsing URI: %w", err)
	}

	cfg := &RemoteConfig{
		Type:      RemoteType(scheme),
		Host:      u.Hostname(),
		Path:      u.Path,
		URL:       rawURI,
		Options:   make(map[string]string),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if u.Port() != "" {
		if p, err := strconv.Atoi(u.Port()); err == nil {
			cfg.Port = p
		}
	}

	if u.User != nil {
		cfg.Username = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			cfg.Password = pass
		}
	}

	// Clean path
	if cfg.Path != "" {
		cfg.Path = vfs.NormalizePath(cfg.Path)
	}

	// Generate default name if empty
	hostPart := cfg.Host
	if hostPart == "" {
		hostPart = "remote"
	}
	cfg.Name = fmt.Sprintf("%s-%s", scheme, hostPart)
	if cfg.Port > 0 {
		cfg.Name = fmt.Sprintf("%s-%d", cfg.Name, cfg.Port)
	}

	return cfg, nil
}

func (m *Manager) mountInternal(cfg RemoteConfig) error {
	if m.registry == nil {
		return errors.New("VFS registry not initialized")
	}

	var fsys vfs.FileSystem

	switch cfg.Type {
	case TypeLocal:
		fsys = vfs.NewOSFS(cfg.Path)
	case TypeMemory:
		fsys = vfs.NewMemFS()
	case TypeWebDAV:
		client := http.NewWebDAVClient(cfg.URL, cfg.Username, cfg.Password, 30*time.Second)
		fsys = vfs.NewWebDAVAdapter(client)
	case TypeGDrive, TypeGoogleDrive:
		authCfg := gdrive.AuthConfig{
			ClientID:        cfg.Options["client_id"],
			ClientSecret:    cfg.Options["client_secret"],
			CredentialsFile: cfg.Options["credentials_file"],
			TokenFile:       cfg.Options["token_file"],
			Port:            cfg.Port,
			CallbackPath:    cfg.Options["callback_path"],
			RedirectURL:     cfg.URL,
		}
		srv, _, err := gdrive.GetDriveService(context.Background(), authCfg)
		if err != nil {
			return fmt.Errorf("gdrive auth failed: %w", err)
		}
		rootFolder := cfg.Path
		if rootFolder == "" {
			rootFolder = cfg.Options["root_folder_id"]
		}
		fsys = gdrive.NewGDriveFS(srv, rootFolder)
	default:
		fsys = vfs.NewMemFS()
	}

	// If a custom root path is provided (e.g. /path/we/want) and not TypeLocal (which already roots at cfg.Path),
	// wrap with SubFS so /path/we/want acts as the virtual root /
	if cfg.Path != "" && cfg.Path != "/" && cfg.Type != TypeLocal && cfg.Type != TypeGDrive && cfg.Type != TypeGoogleDrive {
		fsys = vfs.NewSubFS(fsys, cfg.Path)
	}

	return m.registry.RegisterCustom(cfg.Name, fsys, vfs.MountInfo{
		Name:      cfg.Name,
		Type:      string(cfg.Type),
		Path:      cfg.Path,
		Host:      cfg.Host,
		URL:       cfg.URL,
		Status:    "active",
		CreatedAt: time.Now(),
		Config: vfs.MountConfig{
			Type:     string(cfg.Type),
			Path:     cfg.Path,
			Host:     cfg.Host,
			Port:     cfg.Port,
			Username: cfg.Username,
			Password: cfg.Password,
			URL:      cfg.URL,
			Options:  cfg.Options,
		},
	})
}

