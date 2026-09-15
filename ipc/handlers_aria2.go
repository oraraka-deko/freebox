package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"freebox/aria2"
)

func init() {
	register("aria2.connect", handleAria2Connect)
	register("aria2.addUri", handleAria2AddURI)
	register("aria2.addTorrent", handleAria2AddTorrent)
	register("aria2.addMetalink", handleAria2AddMetalink)
	register("aria2.remove", handleAria2Remove)
	register("aria2.pause", handleAria2Pause)
	register("aria2.pauseAll", handleAria2PauseAll)
	register("aria2.unpause", handleAria2Unpause)
	register("aria2.unpauseAll", handleAria2UnpauseAll)
	register("aria2.tellStatus", handleAria2TellStatus)
	register("aria2.tellActive", handleAria2TellActive)
	register("aria2.tellWaiting", handleAria2TellWaiting)
	register("aria2.tellStopped", handleAria2TellStopped)
	register("aria2.getGlobalStat", handleAria2GetGlobalStat)
	register("aria2.getVersion", handleAria2GetVersion)
	register("aria2.purgeDownloadResult", handleAria2PurgeDownloadResult)
	register("aria2.changeOption", handleAria2ChangeOption)
	register("aria2.getOption", handleAria2GetOption)
}

type aria2BaseParams struct {
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"`
}

type aria2ConnectParams struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

func handleAria2Connect(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2ConnectParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.URL == "" {
		return nil, errors.New("url is required")
	}

	client, err := aria2.NewClient(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ver, err := client.GetVersion(ctx)
	if err != nil {
		return nil, err
	}

	inst.SetAria2Client(client)
	return map[string]any{"ok": true, "version": ver.Version, "enabledFeatures": ver.EnabledFeatures}, nil
}

type aria2AddURIParams struct {
	aria2BaseParams
	URIs    []string      `json:"uris"`
	Options aria2.Options `json:"options,omitempty"`
}

func handleAria2AddURI(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2AddURIParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if len(p.URIs) == 0 {
		return nil, errors.New("uris cannot be empty")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	gid, err := client.AddURI(ctx, p.URIs, p.Options)
	if err != nil {
		return nil, err
	}
	return map[string]string{"gid": gid}, nil
}

type aria2AddTorrentParams struct {
	aria2BaseParams
	Torrent []byte        `json:"torrent"`
	URIs    []string      `json:"uris,omitempty"`
	Options aria2.Options `json:"options,omitempty"`
}

func handleAria2AddTorrent(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2AddTorrentParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if len(p.Torrent) == 0 {
		return nil, errors.New("torrent data cannot be empty")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	gid, err := client.AddTorrent(ctx, p.Torrent, p.URIs, p.Options)
	if err != nil {
		return nil, err
	}
	return map[string]string{"gid": gid}, nil
}

type aria2AddMetalinkParams struct {
	aria2BaseParams
	Metalink []byte        `json:"metalink"`
	Options  aria2.Options `json:"options,omitempty"`
}

func handleAria2AddMetalink(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2AddMetalinkParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if len(p.Metalink) == 0 {
		return nil, errors.New("metalink data cannot be empty")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	gids, err := client.AddMetalink(ctx, p.Metalink, p.Options)
	if err != nil {
		return nil, err
	}
	return map[string]any{"gids": gids}, nil
}

type aria2GIDParams struct {
	aria2BaseParams
	GID   string `json:"gid"`
	Force bool   `json:"force,omitempty"`
}

func handleAria2Remove(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2GIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.GID == "" {
		return nil, errors.New("gid is required")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var gid string
	if p.Force {
		gid, err = client.ForceRemove(ctx, p.GID)
	} else {
		gid, err = client.Remove(ctx, p.GID)
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"gid": gid}, nil
}

func handleAria2Pause(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2GIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.GID == "" {
		return nil, errors.New("gid is required")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var gid string
	if p.Force {
		gid, err = client.ForcePause(ctx, p.GID)
	} else {
		gid, err = client.Pause(ctx, p.GID)
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"gid": gid}, nil
}

type aria2PauseAllParams struct {
	aria2BaseParams
	Force bool `json:"force,omitempty"`
}

func handleAria2PauseAll(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2PauseAllParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var res string
	if p.Force {
		res, err = client.ForcePauseAll(ctx)
	} else {
		res, err = client.PauseAll(ctx)
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"result": res}, nil
}

func handleAria2Unpause(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2GIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.GID == "" {
		return nil, errors.New("gid is required")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gid, err := client.Unpause(ctx, p.GID)
	if err != nil {
		return nil, err
	}
	return map[string]string{"gid": gid}, nil
}

func handleAria2UnpauseAll(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2BaseParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := client.UnpauseAll(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"result": res}, nil
}

type aria2TellStatusParams struct {
	aria2BaseParams
	GID  string   `json:"gid"`
	Keys []string `json:"keys,omitempty"`
}

func handleAria2TellStatus(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2TellStatusParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.GID == "" {
		return nil, errors.New("gid is required")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := client.TellStatus(ctx, p.GID, p.Keys...)
	if err != nil {
		return nil, err
	}
	return status, nil
}

type aria2TellListParams struct {
	aria2BaseParams
	Offset int      `json:"offset,omitempty"`
	Num    int      `json:"num,omitempty"`
	Keys   []string `json:"keys,omitempty"`
}

func handleAria2TellActive(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2TellListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	statuses, err := client.TellActive(ctx, p.Keys...)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

func handleAria2TellWaiting(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2TellListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Num <= 0 {
		p.Num = 100
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	statuses, err := client.TellWaiting(ctx, p.Offset, p.Num, p.Keys...)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

func handleAria2TellStopped(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2TellListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Num <= 0 {
		p.Num = 100
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	statuses, err := client.TellStopped(ctx, p.Offset, p.Num, p.Keys...)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

func handleAria2GetGlobalStat(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2BaseParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stat, err := client.GetGlobalStat(ctx)
	if err != nil {
		return nil, err
	}
	return stat, nil
}

func handleAria2GetVersion(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2BaseParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ver, err := client.GetVersion(ctx)
	if err != nil {
		return nil, err
	}
	return ver, nil
}

func handleAria2PurgeDownloadResult(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2BaseParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := client.PurgeDownloadResult(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"result": res}, nil
}

type aria2ChangeOptionParams struct {
	aria2BaseParams
	GID     string        `json:"gid"`
	Options aria2.Options `json:"options"`
}

func handleAria2ChangeOption(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2ChangeOptionParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.GID == "" {
		return nil, errors.New("gid is required")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := client.ChangeOption(ctx, p.GID, p.Options)
	if err != nil {
		return nil, err
	}
	return map[string]string{"result": res}, nil
}

type aria2GetOptionParams struct {
	aria2BaseParams
	GID string `json:"gid"`
}

func handleAria2GetOption(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p aria2GetOptionParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.GID == "" {
		return nil, errors.New("gid is required")
	}

	client, err := inst.GetAria2Client(p.URL, p.Secret)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts, err := client.GetOption(ctx, p.GID)
	if err != nil {
		return nil, err
	}
	return opts, nil
}
