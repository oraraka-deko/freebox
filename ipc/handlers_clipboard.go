package ipc

import (
	"encoding/json"

	"freebox/engine"
)

func init() {
	register("clipboard.copy", handleClipboardCopy)
	register("clipboard.cut", handleClipboardCut)
	register("clipboard.paste", handleClipboardPaste)
	register("clipboard.clear", handleClipboardClear)
	register("clipboard.count", handleClipboardCount)
}

func handleClipboardCopy(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsPathParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	inst.Engine.Clipboard().Copy(fs, subPath)
	return map[string]any{"count": inst.Engine.Clipboard().Count()}, nil
}

func handleClipboardCut(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p vfsPathParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	inst.Engine.Clipboard().Cut(fs, subPath)
	return map[string]any{"count": inst.Engine.Clipboard().Count()}, nil
}

type clipboardPasteParams struct {
	Dst string `json:"dst"`
}

func handleClipboardPaste(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p clipboardPasteParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	dstMount, dstSubPath := resolveMountPath(p.Dst)
	dstFS, ok := inst.Mounts.Get(dstMount)
	if !ok {
		return nil, errMountNotFound(dstMount)
	}

	plans, err := inst.Engine.Clipboard().PlanPaste(dstFS, dstSubPath)
	if err != nil {
		return nil, err
	}

	taskIDs := make([]string, 0, len(plans))
	for _, plan := range plans {
		var taskType engine.TaskType
		switch plan.Op {
		case "COPY":
			taskType = engine.TaskTypeCopy
		case "CUT":
			taskType = engine.TaskTypeMove
		case "DELETE":
			taskType = engine.TaskTypeDelete
		default:
			taskType = engine.TaskTypeCopy
		}

		t := &engine.Task{
			Type:        taskType,
			Description: string(taskType) + " " + plan.SrcPath + " -> " + plan.DstPath,
			Priority:    10,
			Params: engine.TaskParams{
				SrcFS:   plan.SrcFS,
				DstFS:   plan.DstFS,
				SrcPath: plan.SrcPath,
				DstPath: plan.DstPath,
			},
		}
		handle, err := inst.Engine.Submit(t)
		if err == nil {
			setupTaskNotify(handle, conn)
			taskIDs = append(taskIDs, handle.ID())
		}
	}
	inst.Engine.Clipboard().Clear()

	return map[string]any{"taskIds": taskIDs}, nil
}

func handleClipboardClear(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	inst.Engine.Clipboard().Clear()
	return map[string]bool{"ok": true}, nil
}

func handleClipboardCount(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	return map[string]any{"count": inst.Engine.Clipboard().Count()}, nil
}
