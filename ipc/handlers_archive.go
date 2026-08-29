package ipc

import (
	"context"
	"encoding/json"
	"strings"

	"freebox/archive"
	"freebox/engine"
)

func init() {
	register("archive.compress", handleArchiveCompress)
	register("archive.extract", handleArchiveExtract)
	register("archive.preview", handleArchivePreview)
}

type archiveCompressParams struct {
	Format   string `json:"format"` // "zip","tar","tar.gz","tar.bz2","rar","7z"
	Src      string `json:"src"`    // comma-separated source paths, all on the same mount
	Dst      string `json:"dst"`
	Password string `json:"password,omitempty"`
}

func handleArchiveCompress(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p archiveCompressParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	srcMount, srcSubPath := resolveMountPath(p.Src)
	dstMount, dstSubPath := resolveMountPath(p.Dst)

	srcFS, ok := inst.Mounts.Get(srcMount)
	if !ok {
		return nil, errMountNotFound(srcMount)
	}
	dstFS, ok := inst.Mounts.Get(dstMount)
	if !ok || dstFS == nil {
		dstSubPath = srcSubPath + ".zip"
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Compress " + p.Src + " -> " + p.Dst,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				opts := archive.CreateOptions{Type: archive.ArchiveType(p.Format), Password: p.Password}
				paths := strings.Split(srcSubPath, ",")
				return archive.Create(srcFS, dstSubPath, paths, opts)
			},
		},
	}

	handle, err := inst.Engine.Submit(t)
	if err != nil {
		return nil, err
	}
	setupTaskNotify(handle, conn)
	return map[string]string{"taskId": handle.ID()}, nil
}

type archiveExtractParams struct {
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	Password string `json:"password,omitempty"`
}

func handleArchiveExtract(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p archiveExtractParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	srcMount, srcSubPath := resolveMountPath(p.Src)
	_, dstSubPath := resolveMountPath(p.Dst)

	srcFS, ok := inst.Mounts.Get(srcMount)
	if !ok {
		return nil, errMountNotFound(srcMount)
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Extract " + p.Src + " -> " + p.Dst,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				opts := archive.ExtractOptions{Password: p.Password, Overwrite: true}
				return archive.Extract(srcFS, srcSubPath, dstSubPath, opts)
			},
		},
	}

	handle, err := inst.Engine.Submit(t)
	if err != nil {
		return nil, err
	}
	setupTaskNotify(handle, conn)
	return map[string]string{"taskId": handle.ID()}, nil
}

type archivePreviewParams struct {
	Path     string `json:"path"`
	Password string `json:"password,omitempty"`
}

func handleArchivePreview(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p archivePreviewParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	entries, err := archive.Preview(fs, subPath, p.Password)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
