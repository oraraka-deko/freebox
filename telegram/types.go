package telegram

import (
	"context"
	"sync"
	"time"
)

// AuthStatus defines the stage of authentication.
type AuthStatus string

const (
	StatusNone            AuthStatus = "none"
	StatusPendingCode     AuthStatus = "pending_code"
	StatusPendingPassword AuthStatus = "pending_password"
	StatusAuthenticated   AuthStatus = "authenticated"
	StatusFailed          AuthStatus = "failed"
)

// Account represents a logged-in Telegram account with its metadata and session path.
type Account struct {
	ID          int64     `json:"id"`
	Phone       string    `json:"phone"`
	Username    string    `json:"username,omitempty"`
	FirstName   string    `json:"first_name,omitempty"`
	LastName    string    `json:"last_name,omitempty"`
	IsBot       bool      `json:"is_bot"`
	SessionDir  string    `json:"session_dir"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	LastActive  time.Time `json:"last_active,omitempty"`
}

// DisplayName returns a human-friendly name for the Telegram user.
func (a *Account) DisplayName() string {
	name := a.FirstName
	if a.LastName != "" {
		if name != "" {
			name += " " + a.LastName
		} else {
			name = a.LastName
		}
	}
	if a.Username != "" {
		if name != "" {
			name += fmtUsername(a.Username)
		} else {
			name = "@" + a.Username
		}
	}
	if name == "" {
		name = a.Phone
	}
	return name
}

func fmtUsername(u string) string {
	return " (@" + u + ")"
}

// AuthConfig contains Telegram application configuration.
type AuthConfig struct {
	AppID          int    `json:"app_id"`
	AppHash        string `json:"app_hash"`
	SessionBaseDir string `json:"session_base_dir"`
	TestDC         bool   `json:"test_dc"`
}

// AuthFlowState tracks the ongoing authentication state for a phone number.
type AuthFlowState struct {
	Phone         string     `json:"phone"`
	PhoneCodeHash string     `json:"phone_code_hash,omitempty"`
	Status        AuthStatus `json:"status"`
	PasswordHint  string     `json:"password_hint,omitempty"`
	CodeType      string     `json:"code_type,omitempty"`
	Timeout       int        `json:"timeout,omitempty"`
	Error         string     `json:"error,omitempty"`
	Account       *Account   `json:"account,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AuthStepRequest holds parameters for web/API authentication steps.
type AuthStepRequest struct {
	Phone    string `json:"phone"`
	Code     string `json:"code,omitempty"`
	Password string `json:"password,omitempty"`
}

// AuthStepResponse is returned to API callers after each step.
type AuthStepResponse struct {
	Phone        string     `json:"phone"`
	Status       AuthStatus `json:"status"`
	PasswordHint string     `json:"password_hint,omitempty"`
	Message      string     `json:"message,omitempty"`
	Account      *Account   `json:"account,omitempty"`
	Error        string     `json:"error,omitempty"`
}

// ActiveFlowSession manages asynchronous state machine for a single login attempt.
type ActiveFlowSession struct {
	Phone         string
	PhoneCodeHash string
	Status        AuthStatus
	PasswordHint  string
	CodeType      string
	Timeout       int
	Error         string
	Account       *Account
	UpdatedAt     time.Time
	CodeChan      chan string
	PassChan      chan string
	Cancel        context.CancelFunc
	Mu            sync.Mutex
}
