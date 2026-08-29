package ipc

import (
	"encoding/json"

	"freebox/engine"
)

func init() {
	register("tasks.submit", handleTasksSubmit)
	register("tasks.pause", handleTasksPause)
	register("tasks.resume", handleTasksResume)
	register("tasks.cancel", handleTasksCancel)
	register("tasks.getProgress", handleTasksGetProgress)
	register("tasks.getRecord", handleTasksGetRecord)
}

// setupTaskNotify wires a TaskHandle's progress/status callbacks to push
// "task.progress"/"task.status" notifications on the connection that
// submitted the task, mirroring the old FFI dartPort callback mechanism.
func setupTaskNotify(handle *engine.TaskHandle, conn *Conn) {
	if conn == nil {
		return
	}
	handle.OnProgress(func(p engine.TaskProgress) {
		_ = conn.Notify("task.progress", taskProgressDTO(handle.ID(), p, handle.Status()))
	})
	handle.OnStatus(func(s engine.TaskStatus) {
		_ = conn.Notify("task.status", taskProgressDTO(handle.ID(), handle.Progress(), s))
	})
}

func taskProgressDTO(taskID string, p engine.TaskProgress, status engine.TaskStatus) map[string]any {
	errStr := ""
	if p.Error != nil {
		errStr = p.Error.Error()
	}
	return map[string]any{
		"taskId":         taskID,
		"status":         status,
		"bytesProcessed": p.BytesProcessed,
		"totalBytes":     p.TotalBytes,
		"percent":        p.Percent,
		"speedBytesSec":  p.SpeedBytesSec,
		"durationMs":     p.Duration.Milliseconds(),
		"currentItem":    p.CurrentItem,
		"error":          errStr,
	}
}

type tasksSubmitParams struct {
	Type     string `json:"type"` // CREATE, DELETE, COPY, MOVE, OPEN, SERVE, PROXY, CUSTOM
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	Data     []byte `json:"data,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

func handleTasksSubmit(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p tasksSubmitParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	dstPath := p.Dst
	if dstPath == "" {
		dstPath = p.Src
	}

	srcMount, srcSubPath := resolveMountPath(p.Src)
	dstMount, dstSubPath := resolveMountPath(dstPath)

	srcFS, _ := inst.Mounts.Get(srcMount)
	dstFS, dstOK := inst.Mounts.Get(dstMount)

	taskType := engine.TaskType(p.Type)
	if taskType == engine.TaskTypeCreate && !dstOK && srcFS != nil {
		dstFS = srcFS
		dstSubPath = srcSubPath
	}

	t := &engine.Task{
		Type:        taskType,
		Description: string(taskType) + " " + p.Src,
		Priority:    p.Priority,
		Params: engine.TaskParams{
			SrcFS:   srcFS,
			DstFS:   dstFS,
			SrcPath: srcSubPath,
			DstPath: dstSubPath,
			Data:    p.Data,
		},
	}

	handle, err := inst.Engine.Submit(t)
	if err != nil {
		return nil, err
	}
	setupTaskNotify(handle, conn)
	return map[string]string{"taskId": handle.ID()}, nil
}

type taskIDParams struct {
	TaskID string `json:"taskId"`
}

func handleTasksPause(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p taskIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if err := inst.Engine.PauseTask(p.TaskID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func handleTasksResume(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p taskIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if err := inst.Engine.ResumeTask(p.TaskID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func handleTasksCancel(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p taskIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if err := inst.Engine.CancelTask(p.TaskID); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func handleTasksGetProgress(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p taskIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	handle, ok := inst.Engine.GetTask(p.TaskID)
	if !ok {
		return nil, errTaskNotFound(p.TaskID)
	}
	return taskProgressDTO(p.TaskID, handle.Progress(), handle.Status()), nil
}

func handleTasksGetRecord(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p taskIDParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	handle, ok := inst.Engine.GetTask(p.TaskID)
	if !ok {
		return nil, errTaskNotFound(p.TaskID)
	}
	return handle.ToRecord(), nil
}
