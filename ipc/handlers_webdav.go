package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"freebox/vfs"
	"freebox/webdav"
)

func init() {
	register("webdav.mount", handleWebDAVMount)
	register("webdav.test", handleWebDAVTest)
}

type webdavMountParams struct {
	MountName string `json:"mountName"`
	URL       string `json:"url"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Path      string `json:"path,omitempty"`
}

func handleWebDAVMount(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p webdavMountParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.MountName == "" {
		return nil, errors.New("mountName is required")
	}
	if p.URL == "" {
		return nil, errors.New("url is required")
	}

	client := webdav.NewClient(p.URL, p.Username, p.Password, nil)
	fsys := webdav.NewWebDAVFS(client, p.Path)

	info := vfs.MountInfo{
		Name:      p.MountName,
		Type:      "webdav",
		Path:      p.Path,
		URL:       p.URL,
		Status:    "active",
		CreatedAt: time.Now(),
		Config: vfs.MountConfig{
			Type:     "webdav",
			Path:     p.Path,
			URL:      p.URL,
			Username: p.Username,
			Password: p.Password,
		},
	}

	if err := inst.Mounts.RegisterCustom(p.MountName, fsys, info); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

type webdavTestParams struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Path     string `json:"path,omitempty"`
}

func handleWebDAVTest(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p webdavTestParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.URL == "" {
		return nil, errors.New("url is required")
	}

	client := webdav.NewClient(p.URL, p.Username, p.Password, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testPath := p.Path
	if testPath == "" {
		testPath = "/"
	}
	_, err := client.Exists(ctx, testPath)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
