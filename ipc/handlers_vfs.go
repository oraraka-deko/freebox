package ipc

import (
	"encoding/json"
	"io"

	"freebox/vfs"
)

func init() {
	register("vfs.mount", handleVFSMount)
	register("vfs.unmount", handleVFSUnmount)
	register("vfs.listDir", handleVFSListDir)
	register("vfs.readFile", handleVFSReadFile)
	register("vfs.writeFile", handleVFSWriteFile)
}

type vfsMountParams struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
}

func handleVFSMount(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsMountParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	// ReadOnly is accepted for API parity with the old bridge but is not yet
	// enforced by vfs.Registry.Mount (matches pre-existing behavior).
	cfg := vfs.MountConfig{Type: p.Type, Path: p.Path}
	if err := inst.Mounts.Mount(p.Name, cfg, true); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

type vfsUnmountParams struct {
	Name string `json:"name"`
}

func handleVFSUnmount(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsUnmountParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if err := inst.Mounts.Unmount(p.Name); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

type vfsPathParams struct {
	Path string `json:"path"`
}

type fileInfoDTO struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"` // unix nanoseconds
	IsDir   bool   `json:"isDir"`
}

func handleVFSListDir(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsPathParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	files, err := fs.ReadDir(subPath)
	if err != nil {
		return nil, err
	}
	out := make([]fileInfoDTO, len(files))
	for i, f := range files {
		out[i] = fileInfoDTO{Name: f.Name, Size: f.Size, ModTime: f.ModTime.UnixNano(), IsDir: f.IsDir}
	}
	return out, nil
}

// handleVFSReadFile returns small files inline as base64 (via json.RawMessage's
// default []byte->base64 encoding). Large files should use transfer.open instead.
func handleVFSReadFile(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsPathParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	r, err := fs.Open(subPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return map[string]any{"data": data}, nil
}

type vfsWriteFileParams struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

func handleVFSWriteFile(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsWriteFileParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	w, err := fs.Create(subPath)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Write(p.Data); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}
