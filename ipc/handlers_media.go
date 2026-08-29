package ipc

import (
	"context"
	"encoding/json"

	"freebox/thumbnail"
)

func init() {
	register("media.generateThumbnail", handleMediaGenerateThumbnail)
	register("media.extractMetadata", handleMediaExtractMetadata)
}

type mediaThumbnailParams struct {
	Path    string `json:"path"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Quality int    `json:"quality,omitempty"`
}

func handleMediaGenerateThumbnail(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p mediaThumbnailParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}

	w, h, q := p.Width, p.Height, p.Quality
	if w <= 0 {
		w = 256
	}
	if h <= 0 {
		h = 256
	}
	if q <= 0 {
		q = 80
	}

	opts := thumbnail.ThumbnailOptions{Width: w, Height: h, Quality: q, Format: thumbnail.FormatJPEG, Placeholder: true}
	data, _, err := inst.ThumbMgr.GetOrGenerate(context.Background(), fs, subPath, opts)
	if err != nil {
		return nil, err
	}
	return map[string]any{"data": data}, nil
}

func handleMediaExtractMetadata(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsPathParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	info, err := inst.MetaMgr.GetInfo(fs, subPath)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"title":  info.Name,
		"format": info.MimeType,
		"size":   info.Size,
	}, nil
}
