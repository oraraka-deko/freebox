package main

/*
#include "include/freebox_bridge.h"
#include <stdbool.h>

bool Freebox_PostTaskProgressToDart(
    int64_t port_id,
    uint64_t task_id,
    uint8_t status,
    int64_t bytes_processed,
    int64_t total_bytes,
    double percent,
    double speed_bytes_sec,
    int64_t duration_ms,
    const uint8_t* current_item_ptr,
    int32_t current_item_len,
    const uint8_t* error_ptr,
    int32_t error_len
);
*/
import "C"
import (
	"unsafe"

	"freebox/engine"
	"freebox/vfs"
)

func taskTypeFromCode(code C.FreeboxTaskType) engine.TaskType {
	switch code {
	case C.FREEBOX_TASK_CREATE:
		return engine.TaskTypeCreate
	case C.FREEBOX_TASK_DELETE:
		return engine.TaskTypeDelete
	case C.FREEBOX_TASK_COPY:
		return engine.TaskTypeCopy
	case C.FREEBOX_TASK_MOVE:
		return engine.TaskTypeMove
	case C.FREEBOX_TASK_OPEN:
		return engine.TaskTypeOpen
	case C.FREEBOX_TASK_SERVE:
		return engine.TaskTypeServe
	case C.FREEBOX_TASK_PROXY:
		return engine.TaskTypeProxy
	default:
		return engine.TaskTypeCustom
	}
}

func taskStatusToCode(status engine.TaskStatus) C.FreeboxTaskStatus {
	switch status {
	case engine.StatusPending:
		return C.FREEBOX_STATUS_PENDING
	case engine.StatusRunning:
		return C.FREEBOX_STATUS_RUNNING
	case engine.StatusPaused:
		return C.FREEBOX_STATUS_PAUSED
	case engine.StatusCompleted:
		return C.FREEBOX_STATUS_COMPLETED
	case engine.StatusFailed:
		return C.FREEBOX_STATUS_FAILED
	case engine.StatusCanceled:
		return C.FREEBOX_STATUS_CANCELED
	default:
		return C.FREEBOX_STATUS_PENDING
	}
}

func taskTypeToCode(t engine.TaskType) C.FreeboxTaskType {
	switch t {
	case engine.TaskTypeCreate:
		return C.FREEBOX_TASK_CREATE
	case engine.TaskTypeDelete:
		return C.FREEBOX_TASK_DELETE
	case engine.TaskTypeCopy:
		return C.FREEBOX_TASK_COPY
	case engine.TaskTypeMove:
		return C.FREEBOX_TASK_MOVE
	case engine.TaskTypeOpen:
		return C.FREEBOX_TASK_OPEN
	case engine.TaskTypeServe:
		return C.FREEBOX_TASK_SERVE
	case engine.TaskTypeProxy:
		return C.FREEBOX_TASK_PROXY
	default:
		return C.FREEBOX_TASK_CUSTOM
	}
}

func setupTaskDartCallbacks(handle *engine.TaskHandle, uintTaskID uint64, dartPort int64) {
	if dartPort <= 0 {
		return
	}

	postUpdate := func(p engine.TaskProgress, s engine.TaskStatus) {
		currItemBytes := []byte(p.CurrentItem)
		var errBytes []byte
		if p.Error != nil {
			errBytes = []byte(p.Error.Error())
		}

		var itemPtr, errPtr *C.uint8_t
		var itemLen, errLen C.int32_t

		if len(currItemBytes) > 0 {
			itemPtr = (*C.uint8_t)(unsafe.Pointer(&currItemBytes[0]))
			itemLen = C.int32_t(len(currItemBytes))
		}
		if len(errBytes) > 0 {
			errPtr = (*C.uint8_t)(unsafe.Pointer(&errBytes[0]))
			errLen = C.int32_t(len(errBytes))
		}

		C.Freebox_PostTaskProgressToDart(
			C.int64_t(dartPort),
			C.uint64_t(uintTaskID),
			C.uint8_t(taskStatusToCode(s)),
			C.int64_t(p.BytesProcessed),
			C.int64_t(p.TotalBytes),
			C.double(p.Percent),
			C.double(p.SpeedBytesSec),
			C.int64_t(p.Duration.Milliseconds()),
			itemPtr, itemLen,
			errPtr, errLen,
		)
	}

	handle.OnProgress(func(p engine.TaskProgress) {
		postUpdate(p, handle.Status())
	})

	handle.OnStatus(func(s engine.TaskStatus) {
		postUpdate(handle.Progress(), s)
	})
}

//export Freebox_SubmitTaskBytes
func Freebox_SubmitTaskBytes(
	engineHandle C.uint64_t,
	taskTypeCode C.FreeboxTaskType,
	srcPtr *C.uint8_t, srcLen C.int32_t,
	dstPtr *C.uint8_t, dstLen C.int32_t,
	dataPtr *C.uint8_t, dataLen C.int32_t,
	priority C.int32_t,
	dartPort C.int64_t,
	outResult *C.FreeboxResultC,
) C.uint64_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	srcPath := cBytesToGoString(srcPtr, srcLen)
	dstPath := cBytesToGoString(dstPtr, dstLen)
	if dstPath == "" {
		dstPath = srcPath
	}

	srcMount, srcSubPath := resolveMountPath(srcPath)
	dstMount, dstSubPath := resolveMountPath(dstPath)

	var srcFS, dstFS vfs.FileSystem
	if fs, ok := inst.Mounts.Get(srcMount); ok {
		srcFS = fs
	}
	if fs, ok := inst.Mounts.Get(dstMount); ok {
		dstFS = fs
	}

	taskType := taskTypeFromCode(taskTypeCode)

	if taskType == engine.TaskTypeCreate && dstFS == nil && srcFS != nil {
		dstFS = srcFS
		dstSubPath = srcSubPath
	}

	t := &engine.Task{
		Type:        taskType,
		Description: string(taskType) + " " + srcPath,
		Priority:    int(priority),
		Params: engine.TaskParams{
			SrcFS:   srcFS,
			DstFS:   dstFS,
			SrcPath: srcSubPath,
			DstPath: dstSubPath,
			Data:    cBufferToGoBytes(C.FreeboxByteBuffer{ptr: dataPtr, len: dataLen}),
		},
	}

	handle, err := inst.Engine.Submit(t)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	uintTaskID := getTaskUintID(handle.ID())
	targetPort := int64(dartPort)
	if targetPort <= 0 {
		targetPort = inst.DartPort
	}

	setupTaskDartCallbacks(handle, uintTaskID, targetPort)

	if outResult != nil {
		outResult.success = 1
		outResult.handle = C.uint64_t(uintTaskID)
	}

	return C.uint64_t(uintTaskID)
}

//export Freebox_SubmitTaskRunes
func Freebox_SubmitTaskRunes(
	engineHandle C.uint64_t,
	taskTypeCode C.FreeboxTaskType,
	srcRunesPtr *C.uint32_t, srcRunesLen C.int32_t,
	dstRunesPtr *C.uint32_t, dstRunesLen C.int32_t,
	priority C.int32_t,
	dartPort C.int64_t,
	outResult *C.FreeboxResultC,
) C.uint64_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	srcPath := cRuneBufferToGoString(srcRunesPtr, srcRunesLen)
	dstPath := cRuneBufferToGoString(dstRunesPtr, dstRunesLen)
	if dstPath == "" {
		dstPath = srcPath
	}

	srcMount, srcSubPath := resolveMountPath(srcPath)
	dstMount, dstSubPath := resolveMountPath(dstPath)

	var srcFS, dstFS vfs.FileSystem
	if fs, ok := inst.Mounts.Get(srcMount); ok {
		srcFS = fs
	}
	if fs, ok := inst.Mounts.Get(dstMount); ok {
		dstFS = fs
	}

	taskType := taskTypeFromCode(taskTypeCode)

	if taskType == engine.TaskTypeCreate && dstFS == nil && srcFS != nil {
		dstFS = srcFS
		dstSubPath = srcSubPath
	}

	t := &engine.Task{
		Type:        taskType,
		Description: string(taskType) + " " + srcPath,
		Priority:    int(priority),
		Params: engine.TaskParams{
			SrcFS:   srcFS,
			DstFS:   dstFS,
			SrcPath: srcSubPath,
			DstPath: dstSubPath,
		},
	}

	handle, err := inst.Engine.Submit(t)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	uintTaskID := getTaskUintID(handle.ID())
	targetPort := int64(dartPort)
	if targetPort <= 0 {
		targetPort = inst.DartPort
	}

	setupTaskDartCallbacks(handle, uintTaskID, targetPort)

	if outResult != nil {
		outResult.success = 1
		outResult.handle = C.uint64_t(uintTaskID)
	}

	return C.uint64_t(uintTaskID)
}

//export Freebox_PauseTask
func Freebox_PauseTask(engineHandle C.uint64_t, uintTaskID C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	stringID := getTaskStringID(uint64(uintTaskID))
	if stringID == "" {
		return 0
	}
	if err := inst.Engine.PauseTask(stringID); err != nil {
		return 0
	}
	return 1
}

//export Freebox_ResumeTask
func Freebox_ResumeTask(engineHandle C.uint64_t, uintTaskID C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	stringID := getTaskStringID(uint64(uintTaskID))
	if stringID == "" {
		return 0
	}
	if err := inst.Engine.ResumeTask(stringID); err != nil {
		return 0
	}
	return 1
}

//export Freebox_CancelTask
func Freebox_CancelTask(engineHandle C.uint64_t, uintTaskID C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	stringID := getTaskStringID(uint64(uintTaskID))
	if stringID == "" {
		return 0
	}
	if err := inst.Engine.CancelTask(stringID); err != nil {
		return 0
	}
	return 1
}

//export Freebox_GetTaskProgress
func Freebox_GetTaskProgress(engineHandle C.uint64_t, uintTaskID C.uint64_t, outProgress *C.FreeboxTaskProgressC) C.uint8_t {
	if outProgress == nil {
		return 0
	}
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	stringID := getTaskStringID(uint64(uintTaskID))
	if stringID == "" {
		return 0
	}
	handle, ok := inst.Engine.GetTask(stringID)
	if !ok || handle == nil {
		return 0
	}

	p := handle.Progress()
	s := handle.Status()

	outProgress.task_id = uintTaskID
	outProgress.bytes_processed = C.int64_t(p.BytesProcessed)
	outProgress.total_bytes = C.int64_t(p.TotalBytes)
	outProgress.percent = C.double(p.Percent)
	outProgress.speed_bytes_sec = C.double(p.SpeedBytesSec)
	outProgress.duration_ms = C.int64_t(p.Duration.Milliseconds())
	outProgress.status = taskStatusToCode(s)
	outProgress.current_item = stringToCBuffer(p.CurrentItem)

	if p.Error != nil {
		outProgress.error_msg = stringToCBuffer(p.Error.Error())
	} else {
		outProgress.error_msg = C.FreeboxByteBuffer{ptr: nil, len: 0}
	}

	return 1
}

//export Freebox_GetTaskRecord
func Freebox_GetTaskRecord(engineHandle C.uint64_t, uintTaskID C.uint64_t, outRecord *C.FreeboxTaskRecordC) C.uint8_t {
	if outRecord == nil {
		return 0
	}
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	stringID := getTaskStringID(uint64(uintTaskID))
	if stringID == "" {
		return 0
	}

	handle, ok := inst.Engine.GetTask(stringID)
	if !ok || handle == nil {
		return 0
	}
	rec := handle.ToRecord()

	outRecord.task_id = uintTaskID
	outRecord.task_type = taskTypeToCode(rec.Type)
	outRecord.status = taskStatusToCode(rec.Status)
	outRecord.priority = C.int32_t(rec.Priority)
	outRecord.bytes_processed = C.int64_t(rec.BytesProcessed)
	outRecord.total_bytes = C.int64_t(rec.TotalBytes)
	outRecord.percent = C.double(rec.Percent)
	outRecord.start_time_unix = C.int64_t(rec.StartTime.UnixNano())
	outRecord.end_time_unix = C.int64_t(rec.EndTime.UnixNano())
	outRecord.duration_ms = C.int64_t(rec.Duration.Milliseconds())
	outRecord.src_path = stringToCBuffer(rec.SrcPath)
	outRecord.dst_path = stringToCBuffer(rec.DstPath)
	outRecord.current_item = stringToCBuffer(rec.CurrentItem)
	outRecord.error_msg = stringToCBuffer(rec.Error)

	return 1
}
