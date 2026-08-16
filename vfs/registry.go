package vfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	fbHttp "freebox/http"
	"freebox/storage"
)

var (
	ErrMountNotFound = errors.New("mount not found")
	ErrMountExists   = errors.New("mount already exists")
	ErrInvalidMount  = errors.New("invalid mount configuration")
)

// MountConfig defines parameters for a storage backend mount.
type MountConfig struct {
	Type     string            `json:"type"` // "local", "mem", "webdav"
	Path     string            `json:"path,omitempty"`
	Host     string            `json:"host,omitempty"`
	Port     int               `json:"port,omitempty"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	URL      string            `json:"url,omitempty"`
	Options  map[string]string `json:"options,omitempty"`
}

// MountInfo represents metadata of an active or configured mount point.
type MountInfo struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Path      string      `json:"path,omitempty"`
	Host      string      `json:"host,omitempty"`
	URL       string      `json:"url,omitempty"`
	Status    string      `json:"status"` // "active", "error"
	CreatedAt time.Time   `json:"created_at"`
	Config    MountConfig `json:"config"`
}

// Registry manages named VFS mount points with encrypted persistence.
type Registry struct {
	db     *storage.DB
	mu     sync.RWMutex
	fsMap  map[string]FileSystem
	info   map[string]MountInfo
}

// NewRegistry creates a new MountRegistry instance.
func NewRegistry(db *storage.DB) *Registry {
	r := &Registry{
		db:    db,
		fsMap: make(map[string]FileSystem),
		info:  make(map[string]MountInfo),
	}
	return r
}

// LoadAll loads and initializes all persisted mounts from encrypted storage.
func (r *Registry) LoadAll() error {
	if r.db == nil {
		return nil
	}

	mountsRaw, err := r.db.ListDecrypted(storage.BucketMounts)
	if err != nil {
		return fmt.Errorf("failed reading mounts from storage: %w", err)
	}

	for name, data := range mountsRaw {
		var cfg MountConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		_ = r.Mount(name, cfg, false) // don't re-save to db
	}

	return nil
}

// Mount registers a named mount point and instantiates the underlying FileSystem.
func (r *Registry) Mount(name string, cfg MountConfig, persist bool) error {
	if name == "" {
		return errors.New("mount name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.fsMap[name]; exists {
		return ErrMountExists
	}

	var fs FileSystem
	var err error

	switch cfg.Type {
	case "local", "os":
		if cfg.Path == "" {
			cfg.Path = "."
		}
		fs = NewOSFS(cfg.Path)
	case "mem", "memory":
		fs = NewMemFS()
	case "webdav":
		if cfg.URL == "" {
			return errors.New("webdav URL required")
		}
		client := fbHttp.NewWebDAVClient(cfg.URL, cfg.Username, cfg.Password, 30*time.Second)
		fs = NewWebDAVAdapter(client)
	default:
		return fmt.Errorf("unsupported mount type: %s", cfg.Type)
	}

	if err != nil {
		return fmt.Errorf("failed instantiating mount %s: %w", name, err)
	}

	if persist && r.db != nil {
		data, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := r.db.PutEncrypted(storage.BucketMounts, name, data); err != nil {
			return fmt.Errorf("failed to persist mount: %w", err)
		}
	}

	r.fsMap[name] = fs
	r.info[name] = MountInfo{
		Name:      name,
		Type:      cfg.Type,
		Path:      cfg.Path,
		Host:      cfg.Host,
		URL:       cfg.URL,
		Status:    "active",
		CreatedAt: time.Now(),
		Config:    cfg,
	}

	return nil
}

// RegisterCustom registers an existing FileSystem directly with the registry.
func (r *Registry) RegisterCustom(name string, fs FileSystem, info MountInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.fsMap[name]; exists {
		return ErrMountExists
	}

	info.Name = name
	info.Status = "active"
	if info.CreatedAt.IsZero() {
		info.CreatedAt = time.Now()
	}

	r.fsMap[name] = fs
	r.info[name] = info
	return nil
}

// Unmount unregisters a mount point and removes it from storage if requested.
func (r *Registry) Unmount(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.fsMap[name]; !exists {
		return ErrMountNotFound
	}

	delete(r.fsMap, name)
	delete(r.info, name)

	if r.db != nil {
		_ = r.db.Delete(storage.BucketMounts, name)
	}

	return nil
}

// Get retrieves the FileSystem for a given mount name.
func (r *Registry) Get(name string) (FileSystem, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fs, ok := r.fsMap[name]
	return fs, ok
}

// List returns a snapshot of all active mount metadata.
func (r *Registry) List() []MountInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]MountInfo, 0, len(r.info))
	for _, m := range r.info {
		res = append(res, m)
	}
	return res
}
