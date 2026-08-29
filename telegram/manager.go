package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/contrib/middleware/ratelimit"
	"github.com/gotd/log/logzap"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/time/rate"
	lj "gopkg.in/natefinch/lumberjack.v2"

	"freebox/storage"
)

// Manager manages multi-account Telegram connections, sessions, and authentication.
type Manager struct {
	db              *storage.DB
	storage         *SessionStorageManager
	config          AuthConfig
	coordinator     *FlowCoordinator
	activeClients   map[int64]*telegram.Client
	activeClientsMu sync.RWMutex
}

// LoadConfigFromEnv reads Telegram config from environment variables or .env file.
func LoadConfigFromEnv() AuthConfig {
	loadDotEnv()

	appIDStr := os.Getenv("APP_ID")
	if appIDStr == "" {
		appIDStr = os.Getenv("TG_APP_ID")
	}
	appID, _ := strconv.Atoi(appIDStr)

	appHash := os.Getenv("APP_HASH")
	if appHash == "" {
		appHash = os.Getenv("TG_APP_HASH")
	}

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	sessionBaseDir := filepath.Join(dataDir, "telegram")

	return AuthConfig{
		AppID:          appID,
		AppHash:        appHash,
		SessionBaseDir: sessionBaseDir,
	}
}

func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'') {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

// NewManager initializes the central Telegram account and session manager.
func NewManager(db *storage.DB, cfg AuthConfig) (*Manager, error) {
	if cfg.AppID == 0 || cfg.AppHash == "" {
		// Attempt fallback from env
		envCfg := LoadConfigFromEnv()
		if cfg.AppID == 0 {
			cfg.AppID = envCfg.AppID
		}
		if cfg.AppHash == "" {
			cfg.AppHash = envCfg.AppHash
		}
		if cfg.SessionBaseDir == "" {
			cfg.SessionBaseDir = envCfg.SessionBaseDir
		}
	}

	storageMgr, err := NewSessionStorageManager(db, cfg.SessionBaseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to init session storage manager: %w", err)
	}

	m := &Manager{
		db:            db,
		storage:       storageMgr,
		config:        cfg,
		activeClients: make(map[int64]*telegram.Client),
	}
	m.coordinator = NewFlowCoordinator(m)

	return m, nil
}

// Config returns the active AuthConfig.
func (m *Manager) Config() AuthConfig {
	return m.config
}

// Storage returns the underlying SessionStorageManager.
func (m *Manager) Storage() *SessionStorageManager {
	return m.storage
}

// FlowCoordinator returns the async API flow coordinator.
func (m *Manager) FlowCoordinator() *FlowCoordinator {
	return m.coordinator
}

// createRawClient builds a new gotd telegram.Client with rate limiters and loggers.
func (m *Manager) createRawClient(sessionStorage session.Storage) *telegram.Client {
	logWriter := zapcore.AddSync(&lj.Logger{
		Filename:   filepath.Join(m.config.SessionBaseDir, "telegram.log"),
		MaxBackups: 3,
		MaxSize:    2,
		MaxAge:     7,
	})
	logCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		logWriter,
		zap.WarnLevel,
	)
	lg := zap.New(logCore)

	waiter := floodwait.NewWaiter()

	options := telegram.Options{
		Logger:         logzap.New(lg),
		SessionStorage: sessionStorage,
		Middlewares: []telegram.Middleware{
			waiter,
			ratelimit.New(rate.Every(time.Millisecond*100), 5),
		},
	}

	if m.config.TestDC {
		options.DC = 2
		options.DCList = dcs.Test()
	}

	return telegram.NewClient(m.config.AppID, m.config.AppHash, options)
}

// InteractiveLogin runs interactive sign-in flow on terminal/CLI.
func (m *Manager) InteractiveLogin(ctx context.Context, phone string, isQR bool) (*Account, error) {
	if m.config.AppID == 0 || m.config.AppHash == "" {
		return nil, errors.New("APP_ID and APP_HASH are required. Please set them in .env or provide flags")
	}

	if phone == "" && !isQR {
		phone = os.Getenv("TG_PHONE")
	}

	sessionDir := m.storage.GetAccountDir(0, phone)
	if isQR && phone == "" {
		sessionDir = filepath.Join(m.config.SessionBaseDir, "sessions", "qr_temp")
	}
	_ = os.MkdirAll(sessionDir, 0700)

	sessionStorage := &telegram.FileSessionStorage{
		Path: filepath.Join(sessionDir, "session.json"),
	}

	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(&dispatcher)

	client := m.createRawClient(sessionStorage)
	flow := auth.NewFlow(TerminalAuthenticator{
		PhoneNumber: phone,
		Client:      client,
	}, auth.SendCodeOptions{})

	var loggedAccount *Account

	err := client.Run(ctx, func(cCtx context.Context) error {
		if isQR {
			if err := InteractiveQRAuth(cCtx, client, loggedIn); err != nil {
				return fmt.Errorf("QR authentication failed: %w", err)
			}
		} else {
			if err := client.Auth().IfNecessary(cCtx, flow); err != nil {
				return fmt.Errorf("phone authentication failed: %w", err)
			}
		}

		self, err := client.Self(cCtx)
		if err != nil {
			return fmt.Errorf("failed to fetch user info: %w", err)
		}

		finalDir := m.storage.GetAccountDir(self.ID, phone)
		if sessionDir != finalDir {
			_ = os.Rename(sessionDir, finalDir)
			sessionDir = finalDir
		}

		loggedAccount = &Account{
			ID:         self.ID,
			Phone:      phone,
			Username:   self.Username,
			FirstName:  self.FirstName,
			LastName:   self.LastName,
			IsBot:      self.Bot,
			SessionDir: sessionDir,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			LastActive: time.Now(),
		}

		if err := m.storage.SaveAccount(loggedAccount); err != nil {
			return fmt.Errorf("failed to persist account: %w", err)
		}

		m.activeClientsMu.Lock()
		m.activeClients[self.ID] = client
		m.activeClientsMu.Unlock()

		return nil
	})

	if err != nil {
		return nil, err
	}

	return loggedAccount, nil
}

// ListAccounts returns all registered Telegram accounts.
func (m *Manager) ListAccounts() ([]*Account, error) {
	return m.storage.ListAccounts()
}

// GetAccount retrieves account by ID.
func (m *Manager) GetAccount(id int64) (*Account, error) {
	return m.storage.GetAccount(id)
}

// GetAccountByPhone retrieves account by phone.
func (m *Manager) GetAccountByPhone(phone string) (*Account, error) {
	return m.storage.GetAccountByPhone(phone)
}

// DeleteAccount removes account metadata and local session.
func (m *Manager) DeleteAccount(id int64) error {
	m.activeClientsMu.Lock()
	delete(m.activeClients, id)
	m.activeClientsMu.Unlock()

	return m.storage.DeleteAccount(id)
}

// GetClient retrieves or initializes an authenticated client for an existing account.
func (m *Manager) GetClient(accountID int64) (*telegram.Client, error) {
	m.activeClientsMu.RLock()
	client, exists := m.activeClients[accountID]
	m.activeClientsMu.RUnlock()

	if exists {
		return client, nil
	}

	acc, err := m.storage.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	sessionStorage := &telegram.FileSessionStorage{
		Path: filepath.Join(acc.SessionDir, "session.json"),
	}

	client = m.createRawClient(sessionStorage)

	m.activeClientsMu.Lock()
	m.activeClients[accountID] = client
	m.activeClientsMu.Unlock()

	return client, nil
}

// GetClientService retrieves an initialized ClientService for an account.
func (m *Manager) GetClientService(accountID int64) (*ClientService, error) {
	acc, err := m.storage.GetAccount(accountID)
	if err != nil {
		return nil, err
	}

	client, err := m.GetClient(accountID)
	if err != nil {
		return nil, err
	}

	return NewClientService(client, acc), nil
}

// RunAccount runs a function with a running authenticated client session for the account.
func (m *Manager) RunAccount(ctx context.Context, accountID int64, fn func(ctx context.Context, cs *ClientService) error) error {
	acc, err := m.storage.GetAccount(accountID)
	if err != nil {
		return err
	}

	client, err := m.GetClient(accountID)
	if err != nil {
		return err
	}

	return client.Run(ctx, func(cCtx context.Context) error {
		cs := NewClientService(client, acc)
		return fn(cCtx, cs)
	})
}
