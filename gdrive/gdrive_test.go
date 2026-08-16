package gdrive

import (
	"context"
	"testing"
)

func TestBuildOAuth2Config(t *testing.T) {
	// 1. Direct ClientID + Secret with custom redirect URL
	cfg := AuthConfig{
		ClientID:     "79515qneaoldai7tu8nnb0kba5f.apps.googleusercontent.com",
		ClientSecret: "GH_DKarN77c66ddq",
		Port:         22816,
		CallbackPath: "/freebox",
		RedirectURL:  "http://localhost:22816/freebox",
	}

	oConfig, err := BuildOAuth2Config(cfg)
	if err != nil {
		t.Fatalf("BuildOAuth2Config failed: %v", err)
	}

	if oConfig.ClientID != cfg.ClientID {
		t.Errorf("ClientID mismatch: expected %s, got %s", cfg.ClientID, oConfig.ClientID)
	}
	if oConfig.ClientSecret != cfg.ClientSecret {
		t.Errorf("ClientSecret mismatch")
	}
	if oConfig.RedirectURL != "http://localhost:22816/freebox" {
		t.Errorf("RedirectURL mismatch: expected http://localhost:22816/freebox, got %s", oConfig.RedirectURL)
	}

	// 2. Test JSON credentials
	sampleJSON := []byte(`{
		"installed": {
			"client_id": "json-client-id.apps.googleusercontent.com",
			"client_secret": "json-client-secret",
			"auth_uri": "https://accounts.google.com/o/oauth2/auth",
			"token_uri": "https://oauth2.googleapis.com/token"
		}
	}`)

	cfgJSON := AuthConfig{
		CredentialsJSON: sampleJSON,
		Port:            22816,
		CallbackPath:    "/freebox",
	}

	oConfigJSON, err := BuildOAuth2Config(cfgJSON)
	if err != nil {
		t.Fatalf("BuildOAuth2Config from JSON failed: %v", err)
	}
	if oConfigJSON.ClientID != "json-client-id.apps.googleusercontent.com" {
		t.Errorf("JSON ClientID mismatch: %s", oConfigJSON.ClientID)
	}
	if oConfigJSON.RedirectURL != "http://localhost:22816/freebox" {
		t.Errorf("RedirectURL mismatch: expected http://localhost:22816/freebox, got %s", oConfigJSON.RedirectURL)
	}

	// 3. Test missing credentials error
	_, err = BuildOAuth2Config(AuthConfig{})
	if err == nil {
		t.Errorf("expected error when no credentials provided")
	}
}

func TestGDriveFSOperations(t *testing.T) {
	// Test basic GDriveFS struct creation and path caching
	fs := NewGDriveFS(nil, "custom_root_folder_id")
	if fs.rootFolder != "custom_root_folder_id" {
		t.Errorf("rootFolder mismatch: %s", fs.rootFolder)
	}
	if fs.pathCache["/"] != "custom_root_folder_id" {
		t.Errorf("pathCache['/'] mismatch")
	}

	// Stat of root should succeed immediately without network call
	info, err := fs.Stat("/")
	if err != nil {
		t.Fatalf("root Stat failed: %v", err)
	}
	if !info.IsDir || info.Path != "/" {
		t.Errorf("unexpected root info: %+v", info)
	}

	// Context generation with timeout
	ctx, cancel := fs.ctx()
	defer cancel()
	if ctx == nil {
		t.Errorf("nil context returned")
	}
}

func TestGetDriveService_MissingCredentials(t *testing.T) {
	ctx := context.Background()
	_, _, err := GetDriveService(ctx, AuthConfig{})
	if err == nil {
		t.Errorf("expected error for empty credentials")
	}
}
