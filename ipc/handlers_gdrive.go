package ipc

import (
	"context"
	"encoding/json"

	"freebox/gdrive"
	"freebox/vfs"
)

func init() {
	register("gdrive.authorize", handleGDriveAuthorize)
	register("gdrive.mount", handleGDriveMount)
}

type gdriveAuthorizeParams struct {
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret"`
	CredentialsJSON []byte `json:"credentialsJson,omitempty"`
	Port            int    `json:"port,omitempty"`
	CallbackPath    string `json:"callbackPath,omitempty"`
}

// handleGDriveAuthorize runs the interactive OAuth2 flow (opens a local
// callback listener on Port) and returns the resulting token JSON.
func handleGDriveAuthorize(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p gdriveAuthorizeParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	cfg := gdrive.AuthConfig{
		ClientID:        p.ClientID,
		ClientSecret:    p.ClientSecret,
		CredentialsJSON: p.CredentialsJSON,
		Port:            p.Port,
		CallbackPath:    p.CallbackPath,
	}
	_, tok, err := gdrive.GetDriveService(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return map[string]any{"token": tok}, nil
}

type gdriveMountParams struct {
	MountName    string `json:"mountName"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	TokenJSON    []byte `json:"tokenJson"`
	RootFolderID string `json:"rootFolderId,omitempty"`
}

func handleGDriveMount(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p gdriveMountParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	rootFolderID := p.RootFolderID
	if rootFolderID == "" {
		rootFolderID = "root"
	}

	cfg := gdrive.AuthConfig{ClientID: p.ClientID, ClientSecret: p.ClientSecret, TokenJSON: p.TokenJSON}
	srv, _, err := gdrive.GetDriveService(context.Background(), cfg)
	if err != nil {
		return nil, err
	}

	gdriveFS := gdrive.NewGDriveFS(srv, rootFolderID)
	info := vfs.MountInfo{Name: p.MountName, Type: "gdrive", Path: rootFolderID, Status: "active"}
	if err := inst.Mounts.RegisterCustom(p.MountName, gdriveFS, info); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
