package ipc

import (
	"io"
	"path/filepath"
	"sync"
	"time"

	"freebox/api"
	"freebox/auth"
	"freebox/cert"
	"freebox/engine"
	"freebox/meta"
	"freebox/remotes"
	"freebox/storage"
//	"freebox/telegram"
	"freebox/thumbnail"
	"freebox/vfs"
)

// InstanceConfig configures a new Instance.
type InstanceConfig struct {
	DBPath     string
	Passphrase string
	MaxWorkers int
	StreamPort string
}

// Instance holds all core backend state for a single freeboxd daemon process.
// Unlike the old FFI bridge, there is exactly one Instance per process --
// the daemon is the unit of isolation, not a handle map.
type Instance struct {
	Engine      *engine.Engine
	DB          *storage.DB
	AuthMgr     *auth.Manager
	Mounts      *vfs.Registry
	TelegramMgr *telegram.Manager
	ThumbMgr    *thumbnail.Engine
	MetaMgr     *meta.Manager
	RemotesMgr  *remotes.Manager
	CertMgr     *cert.Manager

	// APIServer is the optional legacy REST/WebSocket server, started
	// independently of the IPC socket for backward compatibility.
	APIServer *api.Server

	// Transfers tracks pending/active fast file-transfer sessions.
	Transfers *transferManager

	serversMu sync.RWMutex
	servers   map[string]io.Closer
}

// NewInstance opens the DB and assembles all managers, mirroring the old
// Freebox_InitEngine assembly logic.
func NewInstance(cfg InstanceConfig) (*Instance, error) {
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = "./data/freebox.db"
	}
	passphrase := cfg.Passphrase
	if passphrase == "" {
		passphrase = "freebox-secret-passphrase"
	}
	streamPort := cfg.StreamPort
	if streamPort == "" {
		streamPort = ":8090"
	}
	workers := cfg.MaxWorkers
	if workers <= 0 {
		workers = 4
	}

	db, err := storage.Open(storage.Config{Path: dbPath, Passphrase: passphrase})
	if err != nil {
		return nil, err
	}

	authMgr, err := auth.NewManager(db, auth.ManagerConfig{
		SessionTTL:       7 * 24 * time.Hour,
		DefaultAdminUser: "admin",
		DefaultAdminPass: "admin123",
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	eng := engine.NewEngine(engine.EngineConfig{
		MaxWorkers: workers,
		StreamPort: streamPort,
		DB:         db,
	})

	reg := vfs.NewRegistry(db)
	_ = reg.LoadAll()
	if _, ok := reg.Get("local"); !ok {
		_ = reg.Mount("local", vfs.MountConfig{Type: "local", Path: "."}, false)
	}

	baseDir := filepath.Dir(dbPath)
	tgMgr, _ := telegram.NewManager(db, telegram.AuthConfig{
		SessionBaseDir: filepath.Join(baseDir, "telegram"),
	})

	thumbMgr := thumbnail.NewEngine(thumbnail.Config{
		CacheDir:    filepath.Join(baseDir, "thumbnails"),
		MaxMemoryMB: 64,
	})

	inst := &Instance{
		Engine:      eng,
		DB:          db,
		AuthMgr:     authMgr,
		Mounts:      reg,
		TelegramMgr: tgMgr,
		ThumbMgr:    thumbMgr,
		MetaMgr:     meta.NewManager(db),
		RemotesMgr:  remotes.NewManager(db, reg),
		CertMgr:     cert.NewManager(db),
		Transfers:   newTransferManager(),
		servers:     make(map[string]io.Closer),
	}
	return inst, nil
}

// RegisterServer tracks a named auxiliary server (http/ftp/dlna) for shutdown.
func (inst *Instance) RegisterServer(name string, c io.Closer) {
	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()
	inst.servers[name] = c
}

// GetServer returns a previously registered auxiliary server.
func (inst *Instance) GetServer(name string) (io.Closer, bool) {
	inst.serversMu.RLock()
	defer inst.serversMu.RUnlock()
	c, ok := inst.servers[name]
	return c, ok
}

// StopServer closes and forgets a previously registered auxiliary server.
func (inst *Instance) StopServer(name string) bool {
	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()
	c, ok := inst.servers[name]
	if !ok {
		return false
	}
	_ = c.Close()
	delete(inst.servers, name)
	return true
}

// Close shuts down all servers, the engine, and the database in order,
// mirroring the old Freebox_CloseEngine teardown.
func (inst *Instance) Close() {
	inst.serversMu.Lock()
	for name, c := range inst.servers {
		_ = c.Close()
		delete(inst.servers, name)
	}
	inst.serversMu.Unlock()

	if inst.APIServer != nil {
		_ = inst.APIServer.Close()
	}
	if inst.Engine != nil {
		inst.Engine.Close()
	}
	if inst.DB != nil {
		_ = inst.DB.Close()
	}
}
