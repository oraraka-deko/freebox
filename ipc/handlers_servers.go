package ipc

import (
	"encoding/json"
	"net"

	"freebox/api"
	"freebox/dlna"
	"freebox/ftp"
	fbHttp "freebox/http"
)

func init() {
	register("servers.startAPI", handleServersStartAPI)
	register("servers.stopAPI", handleServersStopAPI)
	register("servers.startHTTP", handleServersStartHTTP)
	register("servers.stopHTTP", handleServersStopHTTP)
	register("servers.startFTP", handleServersStartFTP)
	register("servers.stopFTP", handleServersStopFTP)
	register("servers.startDLNA", handleServersStartDLNA)
	register("servers.stopDLNA", handleServersStopDLNA)
}

type serversStartAPIParams struct {
	Port string `json:"port,omitempty"`
}

func handleServersStartAPI(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p serversStartAPIParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	port := p.Port
	if port == "" {
		port = ":8080"
	}

	if inst.APIServer != nil {
		_ = inst.APIServer.Close()
	}

	srv := api.NewServer(api.ServerConfig{
		Addr:        port,
		AuthMgr:     inst.AuthMgr,
		StorageDB:   inst.DB,
		Mounts:      inst.Mounts,
		Engine:      inst.Engine,
		StreamServ:  inst.Engine.StreamServer(),
		MetaMgr:     inst.MetaMgr,
		RemotesMgr:  inst.RemotesMgr,
		ThumbMgr:    inst.ThumbMgr,
		CertMgr:     inst.CertMgr,
		TelegramMgr: inst.TelegramMgr,
	})
	inst.APIServer = srv
	go func() { _ = srv.Start() }()
	return map[string]bool{"ok": true}, nil
}

func handleServersStopAPI(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	if inst.APIServer == nil {
		return map[string]bool{"ok": false}, nil
	}
	_ = inst.APIServer.Close()
	inst.APIServer = nil
	return map[string]bool{"ok": true}, nil
}

type serversStartHTTPParams struct {
	Port    string `json:"port,omitempty"`
	RootDir string `json:"rootDir,omitempty"`
}

func handleServersStartHTTP(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p serversStartHTTPParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	port := p.Port
	if port == "" {
		port = ":8081"
	}
	cfg := fbHttp.DefaultConfig()
	cfg.RootDir = p.RootDir
	srv := fbHttp.NewServer(cfg)
	l, err := net.Listen("tcp", port)
	if err != nil {
		return nil, err
	}
	inst.RegisterServer("http", srv)
	go func() { _ = srv.Serve(l) }()
	return map[string]bool{"ok": true}, nil
}

func handleServersStopHTTP(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	return map[string]bool{"ok": inst.StopServer("http")}, nil
}

type serversStartFTPParams struct {
	Port    string `json:"port,omitempty"`
	RootDir string `json:"rootDir,omitempty"`
}

func handleServersStartFTP(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p serversStartFTPParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	port := p.Port
	if port == "" {
		port = ":2121"
	}
	cfg := ftp.DefaultConfig()
	cfg.RootDir = p.RootDir
	srv := ftp.NewServer(cfg)
	l, err := net.Listen("tcp", port)
	if err != nil {
		return nil, err
	}
	inst.RegisterServer("ftp", srv)
	go func() { _ = srv.Serve(l) }()
	return map[string]bool{"ok": true}, nil
}

func handleServersStopFTP(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	return map[string]bool{"ok": inst.StopServer("ftp")}, nil
}

type serversStartDLNAParams struct {
	FriendlyName string `json:"friendlyName,omitempty"`
	Port         string `json:"port,omitempty"`
	RootDir      string `json:"rootDir,omitempty"`
}

func handleServersStartDLNA(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p serversStartDLNAParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	name := p.FriendlyName
	if name == "" {
		name = "Freebox DLNA Media Server"
	}
	port := p.Port
	if port == "" {
		port = ":8200"
	}
	cfg := dlna.DefaultConfig()
	cfg.FriendlyName = name
	cfg.RootDir = p.RootDir
	srv := dlna.NewServer(cfg)
	l, err := net.Listen("tcp", port)
	if err != nil {
		return nil, err
	}
	inst.RegisterServer("dlna", srv)
	go func() { _ = srv.Serve(l) }()
	return map[string]bool{"ok": true}, nil
}

func handleServersStopDLNA(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	return map[string]bool{"ok": inst.StopServer("dlna")}, nil
}
