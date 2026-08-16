package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

var (
	ErrFlowNotFound = errors.New("no active authentication session found for this phone")
	ErrFlowExpired  = errors.New("authentication session expired or cancelled")
)

// AsyncAuthenticator implements auth.UserAuthenticator driven by API channel events.
type AsyncAuthenticator struct {
	phone    string
	session  *ActiveFlowSession
	client   *telegram.Client
	codeOnce sync.Once
	passOnce sync.Once
}

func (a *AsyncAuthenticator) Phone(ctx context.Context) (string, error) {
	return a.phone, nil
}

func (a *AsyncAuthenticator) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	a.session.Mu.Lock()
	a.session.Status = StatusPendingCode
	if sentCode != nil {
		a.session.PhoneCodeHash = sentCode.PhoneCodeHash
		if sentCode.Type != nil {
			a.session.CodeType = fmt.Sprintf("%T", sentCode.Type)
		}
		a.session.Timeout = int(sentCode.Timeout)
	}
	a.session.UpdatedAt = time.Now()
	a.session.Mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code, ok := <-a.session.CodeChan:
		if !ok || code == "" {
			return "", errors.New("code entry cancelled")
		}
		return strings.TrimSpace(code), nil
	}
}

func (a *AsyncAuthenticator) Password(ctx context.Context) (string, error) {
	hint := ""
	if a.client != nil {
		pwdInfo, err := a.client.API().AccountGetPassword(ctx)
		if err == nil && pwdInfo != nil {
			hint = pwdInfo.Hint
		}
	}

	a.session.Mu.Lock()
	a.session.Status = StatusPendingPassword
	a.session.PasswordHint = hint
	a.session.UpdatedAt = time.Now()
	a.session.Mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case pass, ok := <-a.session.PassChan:
		if !ok || pass == "" {
			return "", errors.New("password entry cancelled")
		}
		return strings.TrimSpace(pass), nil
	}
}

func (a *AsyncAuthenticator) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a *AsyncAuthenticator) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{
		FirstName: "Freebox User",
	}, nil
}

// FlowCoordinator manages ongoing API-driven authentications.
type FlowCoordinator struct {
	sessions map[string]*ActiveFlowSession
	mu       sync.RWMutex
	mgr      *Manager
	stopChan chan struct{}
}

// NewFlowCoordinator creates a coordinator for REST API login flows.
func NewFlowCoordinator(mgr *Manager) *FlowCoordinator {
	fc := &FlowCoordinator{
		sessions: make(map[string]*ActiveFlowSession),
		mgr:      mgr,
		stopChan: make(chan struct{}),
	}

	// Periodic cleanup of stale sessions older than 10 minutes
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-fc.stopChan:
				return
			case <-ticker.C:
				fc.cleanupStale()
			}
		}
	}()

	return fc
}

// Close stops background cleanup goroutines.
func (fc *FlowCoordinator) Close() {
	select {
	case <-fc.stopChan:
	default:
		close(fc.stopChan)
	}
}

func (fc *FlowCoordinator) cleanupStale() {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	threshold := time.Now().Add(-10 * time.Minute)
	for phone, s := range fc.sessions {
		if s.UpdatedAt.Before(threshold) {
			if s.Cancel != nil {
				s.Cancel()
			}
			delete(fc.sessions, phone)
		}
	}
}

// StartPhoneAuth begins a new background auth flow for the given phone.
func (fc *FlowCoordinator) StartPhoneAuth(ctx context.Context, phone string) (*AuthStepResponse, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return nil, errors.New("phone number is required")
	}

	fc.mu.Lock()
	if existing, exists := fc.sessions[phone]; exists {
		if existing.Cancel != nil {
			existing.Cancel()
		}
		delete(fc.sessions, phone)
	}

	sessionCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	flowSession := &ActiveFlowSession{
		Phone:     phone,
		Status:    StatusPendingCode,
		CodeChan:  make(chan string, 1),
		PassChan:  make(chan string, 1),
		Cancel:    cancel,
		UpdatedAt: time.Now(),
	}
	fc.sessions[phone] = flowSession
	fc.mu.Unlock()

	// Prepare session directory
	sessionDir := fc.mgr.storage.GetAccountDir(0, phone)
	_ = os.MkdirAll(sessionDir, 0700)

	sessionStorage := &telegram.FileSessionStorage{
		Path: filepath.Join(sessionDir, "session.json"),
	}

	client := fc.mgr.createRawClient(sessionStorage)
	authenticator := &AsyncAuthenticator{
		phone:   phone,
		session: flowSession,
		client:  client,
	}

	flow := auth.NewFlow(authenticator, auth.SendCodeOptions{})

	// Launch background client worker
	go func() {
		defer cancel()
		err := client.Run(sessionCtx, func(cCtx context.Context) error {
			if err := client.Auth().IfNecessary(cCtx, flow); err != nil {
				flowSession.Mu.Lock()
				flowSession.Status = StatusFailed
				flowSession.Error = err.Error()
				flowSession.UpdatedAt = time.Now()
				flowSession.Mu.Unlock()
				return err
			}

			// Successfully authenticated!
			self, err := client.Self(cCtx)
			if err != nil {
				flowSession.Mu.Lock()
				flowSession.Status = StatusFailed
				flowSession.Error = fmt.Sprintf("failed to get self info: %v", err)
				flowSession.UpdatedAt = time.Now()
				flowSession.Mu.Unlock()
				return err
			}

			// Rename folder to account ID if needed, and save
			finalDir := fc.mgr.storage.GetAccountDir(self.ID, phone)
			if sessionDir != finalDir {
				_ = os.Rename(sessionDir, finalDir)
				sessionDir = finalDir
			}

			acc := &Account{
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

			if err := fc.mgr.storage.SaveAccount(acc); err != nil {
				flowSession.Mu.Lock()
				flowSession.Status = StatusFailed
				flowSession.Error = fmt.Sprintf("failed to save account: %v", err)
				flowSession.UpdatedAt = time.Now()
				flowSession.Mu.Unlock()
				return err
			}

			flowSession.Mu.Lock()
			flowSession.Status = StatusAuthenticated
			flowSession.Account = acc
			flowSession.UpdatedAt = time.Now()
			flowSession.Mu.Unlock()

			return nil
		})

		if err != nil && !errors.Is(err, context.Canceled) {
			flowSession.Mu.Lock()
			if flowSession.Status != StatusAuthenticated {
				flowSession.Status = StatusFailed
				flowSession.Error = err.Error()
			}
			flowSession.UpdatedAt = time.Now()
			flowSession.Mu.Unlock()
		}
	}()

	// Wait up to 3 seconds for code to be sent or failure
	waitDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(waitDeadline) {
		flowSession.Mu.Lock()
		status := flowSession.Status
		errMsg := flowSession.Error
		flowSession.Mu.Unlock()

		if status == StatusPendingCode || status == StatusPendingPassword || status == StatusAuthenticated {
			break
		}
		if status == StatusFailed {
			return nil, errors.New(errMsg)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return &AuthStepResponse{
		Phone:   phone,
		Status:  StatusPendingCode,
		Message: "Verification code sent to phone via Telegram / SMS",
	}, nil
}

// SubmitCode delivers the code entered by the user.
func (fc *FlowCoordinator) SubmitCode(ctx context.Context, phone string, code string) (*AuthStepResponse, error) {
	phone = strings.TrimSpace(phone)
	fc.mu.RLock()
	s, exists := fc.sessions[phone]
	fc.mu.RUnlock()

	if !exists {
		return nil, ErrFlowNotFound
	}

	s.Mu.Lock()
	if s.Status != StatusPendingCode {
		s.Mu.Unlock()
		return &AuthStepResponse{
			Phone:        phone,
			Status:       s.Status,
			PasswordHint: s.PasswordHint,
			Account:      s.Account,
			Error:        s.Error,
		}, nil
	}
	s.Mu.Unlock()

	// Send code into channel
	select {
	case s.CodeChan <- strings.TrimSpace(code):
	default:
	}

	// Wait up to 4 seconds for result or 2FA request
	waitDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(waitDeadline) {
		s.Mu.Lock()
		status := s.Status
		hint := s.PasswordHint
		acc := s.Account
		errMsg := s.Error
		s.Mu.Unlock()

		if status == StatusPendingPassword {
			return &AuthStepResponse{
				Phone:        phone,
				Status:       StatusPendingPassword,
				PasswordHint: hint,
				Message:      "Two-factor authentication (2FA) password required",
			}, nil
		}
		if status == StatusAuthenticated {
			return &AuthStepResponse{
				Phone:   phone,
				Status:  StatusAuthenticated,
				Account: acc,
				Message: "Successfully logged in to Telegram account",
			}, nil
		}
		if status == StatusFailed {
			return nil, fmt.Errorf("authentication failed: %s", errMsg)
		}
		time.Sleep(100 * time.Millisecond)
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()
	return &AuthStepResponse{
		Phone:        phone,
		Status:       s.Status,
		PasswordHint: s.PasswordHint,
		Account:      s.Account,
		Error:        s.Error,
	}, nil
}

// SubmitPassword delivers the 2FA password.
func (fc *FlowCoordinator) SubmitPassword(ctx context.Context, phone string, password string) (*AuthStepResponse, error) {
	phone = strings.TrimSpace(phone)
	fc.mu.RLock()
	s, exists := fc.sessions[phone]
	fc.mu.RUnlock()

	if !exists {
		return nil, ErrFlowNotFound
	}

	s.Mu.Lock()
	if s.Status != StatusPendingPassword {
		s.Mu.Unlock()
		return &AuthStepResponse{
			Phone:        phone,
			Status:       s.Status,
			PasswordHint: s.PasswordHint,
			Account:      s.Account,
			Error:        s.Error,
		}, nil
	}
	s.Mu.Unlock()

	// Send password into channel
	select {
	case s.PassChan <- strings.TrimSpace(password):
	default:
	}

	// Wait up to 4 seconds for result
	waitDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(waitDeadline) {
		s.Mu.Lock()
		status := s.Status
		acc := s.Account
		errMsg := s.Error
		s.Mu.Unlock()

		if status == StatusAuthenticated {
			return &AuthStepResponse{
				Phone:   phone,
				Status:  StatusAuthenticated,
				Account: acc,
				Message: "Successfully logged in with 2FA password",
			}, nil
		}
		if status == StatusFailed {
			return nil, fmt.Errorf("2FA authentication failed: %s", errMsg)
		}
		time.Sleep(100 * time.Millisecond)
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()
	return &AuthStepResponse{
		Phone:        phone,
		Status:       s.Status,
		PasswordHint: s.PasswordHint,
		Account:      s.Account,
		Error:        s.Error,
	}, nil
}

// GetFlowStatus queries current status of an ongoing login flow.
func (fc *FlowCoordinator) GetFlowStatus(phone string) (*AuthStepResponse, error) {
	phone = strings.TrimSpace(phone)
	fc.mu.RLock()
	s, exists := fc.sessions[phone]
	fc.mu.RUnlock()

	if !exists {
		return nil, ErrFlowNotFound
	}

	s.Mu.Lock()
	defer s.Mu.Unlock()

	return &AuthStepResponse{
		Phone:        s.Phone,
		Status:       s.Status,
		PasswordHint: s.PasswordHint,
		Account:      s.Account,
		Error:        s.Error,
	}, nil
}
