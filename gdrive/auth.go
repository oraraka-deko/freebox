package gdrive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const (
	// DefaultPort is the dedicated OAuth2 loopback port for Freebox.
	DefaultPort = 22816
	// DefaultCallbackPath is the dedicated OAuth2 redirect path for Freebox.
	DefaultCallbackPath = "/freebox"
	// DefaultRedirectURL is the full loopback redirect URL.
	DefaultRedirectURL = "http://localhost:22816/freebox"
	// DefaultTokenFile is the fallback local token cache file.
	DefaultTokenFile = "token.json"
	// DefaultCredentialsFile is the standard client credentials file name.
	DefaultCredentialsFile = "credentials.json"
)

// AuthConfig provides credentials and settings for Google Drive authentication.
type AuthConfig struct {
	ClientID        string `json:"client_id,omitempty"`
	ClientSecret    string `json:"client_secret,omitempty"`
	CredentialsJSON []byte `json:"credentials_json,omitempty"`
	CredentialsFile string `json:"credentials_file,omitempty"`
	TokenJSON       []byte `json:"token_json,omitempty"`
	TokenFile       string `json:"token_file,omitempty"`
	Port            int    `json:"port,omitempty"`
	CallbackPath    string `json:"callback_path,omitempty"`
	RedirectURL     string `json:"redirect_url,omitempty"`
	ReadOnly        bool   `json:"read_only,omitempty"`
}

// OpenBrowser opens the default web browser on Windows, macOS, and Linux.
func OpenBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	case "darwin":
		cmd = exec.Command("open", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

// BuildOAuth2Config constructs an oauth2.Config from AuthConfig.
func BuildOAuth2Config(cfg AuthConfig) (*oauth2.Config, error) {
	scope := drive.DriveScope
	if cfg.ReadOnly {
		scope = drive.DriveReadonlyScope
	}

	port := cfg.Port
	if port <= 0 {
		port = DefaultPort
	}
	cbPath := cfg.CallbackPath
	if cbPath == "" {
		cbPath = DefaultCallbackPath
	}
	redirectURL := cfg.RedirectURL
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("http://localhost:%d%s", port, cbPath)
	}

	// Helper to parse raw credentials JSON
	parseJSONCredentials := func(raw []byte) (*oauth2.Config, error) {
		oConfig, err := google.ConfigFromJSON(raw, scope)
		if err == nil {
			oConfig.RedirectURL = redirectURL
			return oConfig, nil
		}

		// Fallback: parse client_id and client_secret directly from JSON
		var creds struct {
			Installed struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			} `json:"installed"`
			Web struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			} `json:"web"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}

		if unmarshalErr := json.Unmarshal(raw, &creds); unmarshalErr == nil {
			cid := creds.Installed.ClientID
			sec := creds.Installed.ClientSecret
			if cid == "" {
				cid = creds.Web.ClientID
				sec = creds.Web.ClientSecret
			}
			if cid == "" {
				cid = creds.ClientID
				sec = creds.ClientSecret
			}
			if cid != "" && sec != "" {
				return &oauth2.Config{
					ClientID:     cid,
					ClientSecret: sec,
					Endpoint:     google.Endpoint,
					RedirectURL:  redirectURL,
					Scopes:       []string{scope},
				}, nil
			}
		}

		return nil, fmt.Errorf("unable to parse client secrets: %w", err)
	}

	// 1. Try credentials JSON bytes or file
	if len(cfg.CredentialsJSON) > 0 {
		return parseJSONCredentials(cfg.CredentialsJSON)
	}

	if cfg.CredentialsFile != "" {
		credBytes, err := os.ReadFile(cfg.CredentialsFile)
		if err == nil {
			return parseJSONCredentials(credBytes)
		}
	}

	// Fallback to default credentials.json if present
	if credBytes, err := os.ReadFile(DefaultCredentialsFile); err == nil {
		if oConfig, err := parseJSONCredentials(credBytes); err == nil {
			return oConfig, nil
		}
	}

	// 2. Try direct ClientID and ClientSecret
	clientID := cfg.ClientID
	clientSecret := cfg.ClientSecret
	if clientID == "" {
		clientID = os.Getenv("GDRIVE_CLIENT_ID")
	}
	if clientSecret == "" {
		clientSecret = os.Getenv("GDRIVE_CLIENT_SECRET")
	}

	if clientID != "" && clientSecret != "" {
		return &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  redirectURL,
			Scopes:       []string{scope},
		}, nil
	}

	return nil, errors.New("no Google Drive credentials provided. Please supply credentials.json, ClientID/ClientSecret, or set GDRIVE_CLIENT_ID / GDRIVE_CLIENT_SECRET environment variables")
}

// GetTokenViaLoopback starts a local HTTP listener on port 22816 (or custom port), opens the browser, and handles the OAuth2 redirect code exchange.
func GetTokenViaLoopback(ctx context.Context, config *oauth2.Config, port int, callbackPath string) (*oauth2.Token, error) {
	if port <= 0 {
		port = DefaultPort
	}
	if callbackPath == "" {
		callbackPath = DefaultCallbackPath
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// Fallback to any available loopback port if requested port is busy
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("failed to bind loopback listener on %s: %w", addr, err)
		}
		port = listener.Addr().(*net.TCPAddr).Port
	}
	defer listener.Close()

	config.RedirectURL = fmt.Sprintf("http://localhost:%d%s", port, callbackPath)

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, err
	}
	expectedState := hex.EncodeToString(stateBytes)

	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != expectedState {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			errChan <- errors.New("oauth state mismatch")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No code provided", http.StatusBadRequest)
			errChan <- errors.New("no authorization code returned")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
			<!DOCTYPE html>
			<html>
			<head><title>Freebox Google Drive Authentication</title></head>
			<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; height: 90vh; margin: 0;">
				<div style="background: #1e293b; padding: 40px; border-radius: 16px; border: 1px solid #334155; text-align: center; max-width: 440px; box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5);">
					<h2 style="color: #38bdf8; margin-top: 0;">Authentication Successful!</h2>
					<p style="color: #94a3b8; line-height: 1.6;">Your Google Drive storage is now connected to Freebox.</p>
					<p style="color: #64748b; font-size: 14px;">You can safely close this browser tab and return to Freebox.</p>
				</div>
			</body>
			</html>
		`)
		codeChan <- code
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	authURL := config.AuthCodeURL(
		expectedState,
		oauth2.AccessTypeOffline, // Requests refresh token
		oauth2.ApprovalForce,     // Ensures refresh token is re-issued
	)

	fmt.Printf("Freebox: Opening browser for Google Drive authorization at %s...\n", authURL)
	if err := OpenBrowser(authURL); err != nil {
		fmt.Printf("Failed to open browser automatically. Please visit this URL to authenticate:\n%s\n", authURL)
	}

	var code string
	select {
	case code = <-codeChan:
	case err := <-errChan:
		return nil, err
	case <-time.After(3 * time.Minute):
		return nil, errors.New("google drive authentication timed out (3 minutes)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	_ = srv.Shutdown(ctx)
	return config.Exchange(ctx, code)
}

// GetDriveService initializes a Google Drive v3 Service with token loading, auto-refresh, and interactive login if needed.
func GetDriveService(ctx context.Context, cfg AuthConfig) (*drive.Service, *oauth2.Token, error) {
	oConfig, err := BuildOAuth2Config(cfg)
	if err != nil {
		return nil, nil, err
	}

	tok := &oauth2.Token{}
	tokenLoaded := false

	// 1. Try provided TokenJSON
	if len(cfg.TokenJSON) > 0 {
		if err := json.Unmarshal(cfg.TokenJSON, tok); err == nil && tok.Valid() {
			tokenLoaded = true
		}
	}

	// 2. Try token file
	tokenFilePath := cfg.TokenFile
	if tokenFilePath == "" {
		tokenFilePath = DefaultTokenFile
	}
	if !tokenLoaded {
		if f, err := os.Open(tokenFilePath); err == nil {
			if err := json.NewDecoder(f).Decode(tok); err == nil && tok.Valid() {
				tokenLoaded = true
			}
			f.Close()
		}
	}

	// 3. If no valid token, launch interactive OAuth2 loopback login
	if !tokenLoaded {
		tok, err = GetTokenViaLoopback(ctx, oConfig, cfg.Port, cfg.CallbackPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to obtain Google Drive token: %w", err)
		}

		// Cache token to disk if file path specified
		if tf, createErr := os.Create(tokenFilePath); createErr == nil {
			_ = json.NewEncoder(tf).Encode(tok)
			tf.Close()
		}
	}

	// TokenSource handles transparent background token refreshes
	tokenSource := oConfig.TokenSource(ctx, tok)
	srv, err := drive.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize drive service: %w", err)
	}

	return srv, tok, nil
}
