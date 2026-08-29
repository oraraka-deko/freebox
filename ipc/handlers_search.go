package ipc

import (
	"context"
	"encoding/json"

	"freebox/dedup"
	"freebox/engine"
	"freebox/search"
)

func init() {
	register("search.run", handleSearchRun)
	register("dedup.scan", handleDedupScan)
}

type searchRunParams struct {
	RootPath       string `json:"rootPath"`
	NamePattern    string `json:"namePattern"`
	ContentPattern string `json:"contentPattern"`
	Replacement    string `json:"replacement"`
	MatchType      string `json:"matchType"` // substring, exact, regex, glob, prefix, suffix
	Target         string `json:"target"`    // all, names, content
	IsReplace      bool   `json:"isReplace"`
	CaseSensitive  bool   `json:"caseSensitive"`
}

func handleSearchRun(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p searchRunParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.RootPath)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}

	mType := search.MatchType(p.MatchType)
	if mType == "" {
		mType = search.MatchSubstring
	}
	target := search.SearchTarget(p.Target)
	if target == "" {
		target = search.TargetNamesAndContent
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Search/Replace in " + p.RootPath,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				searchEngine := search.NewEngine(fs)
				query := search.SearchQuery{
					RootPath:         subPath,
					NamePattern:      p.NamePattern,
					NameMatchType:    mType,
					ContentPattern:   p.ContentPattern,
					ContentMatchType: mType,
					CaseSensitive:    p.CaseSensitive,
					Target:           target,
					Replacement:      p.Replacement,
					IsReplace:        p.IsReplace,
					OnProgress: func(stats search.SearchStats) {
						handle.UpdateProgress(stats.FilesScanned, 0, stats.CurrentPath)
					},
				}
				results, stats, err := searchEngine.Search(ctx, query)
				if err != nil {
					return err
				}
				handle.UpdateProgress(stats.FilesScanned, stats.FilesScanned, "Completed")
				handle.SetResult(len(results))
				return nil
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

type dedupScanParams struct {
	RootPath   string `json:"rootPath"`
	Method     string `json:"method"`     // meta, quick_hash, md5, sha256, sha1, crc32
	Action     string `json:"action"`     // report, delete
	KeepPolicy string `json:"keepPolicy"` // oldest, newest, shortest_path, first
	MinSize    int64  `json:"minSize,omitempty"`
	MaxWorkers int    `json:"maxWorkers,omitempty"`
}

func handleDedupScan(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p dedupScanParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.RootPath)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}

	workers := p.MaxWorkers
	if workers <= 0 {
		workers = 4
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Dedup scan " + p.RootPath,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				dedupEngine := dedup.NewEngine(fs)
				opts := dedup.DedupOptions{
					RootPath:    subPath,
					Method:      dedup.DedupMethod(p.Method),
					Action:      dedup.DedupAction(p.Action),
					KeepPolicy:  dedup.KeepPolicy(p.KeepPolicy),
					MinFileSize: p.MinSize,
					MaxWorkers:  workers,
					OnProgress: func(pr dedup.DedupProgress) {
						handle.UpdateProgress(pr.BytesScanned, 0, pr.CurrentPath)
					},
				}
				groups, prog, err := dedupEngine.Deduplicate(ctx, opts)
				if err != nil {
					return err
				}
				handle.UpdateProgress(prog.BytesScanned, prog.BytesScanned, "Completed")
				handle.SetResult(len(groups))
				return nil
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
