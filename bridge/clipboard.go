package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"freebox/engine"
)

//export Freebox_ClipboardCopy
func Freebox_ClipboardCopy(
	engineHandle C.uint64_t,
	pathPtr *C.uint8_t, pathLen C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil || inst.Mounts == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(pathPtr, pathLen)
	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	inst.Engine.Clipboard().Copy(targetFS, subPath)
	if outResult != nil {
		outResult.success = 1
		outResult.value = C.int64_t(inst.Engine.Clipboard().Count())
	}
	return 1
}

//export Freebox_ClipboardCut
func Freebox_ClipboardCut(
	engineHandle C.uint64_t,
	pathPtr *C.uint8_t, pathLen C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil || inst.Mounts == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(pathPtr, pathLen)
	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	inst.Engine.Clipboard().Cut(targetFS, subPath)
	if outResult != nil {
		outResult.success = 1
		outResult.value = C.int64_t(inst.Engine.Clipboard().Count())
	}
	return 1
}

//export Freebox_ClipboardPaste
func Freebox_ClipboardPaste(
	engineHandle C.uint64_t,
	dstDirPtr *C.uint8_t, dstDirLen C.int32_t,
	dartPort C.int64_t,
	outResult *C.FreeboxResultC,
) C.uint64_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil || inst.Mounts == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	dstFullPath := cBytesToGoString(dstDirPtr, dstDirLen)
	dstMount, dstSubPath := resolveMountPath(dstFullPath)
	dstFS, ok := inst.Mounts.Get(dstMount)
	if !ok || dstFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("destination mount not found: " + dstMount)
		}
		return 0
	}

	plans, err := inst.Engine.Clipboard().PlanPaste(dstFS, dstSubPath)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	var firstTaskID uint64
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
			uID := getTaskUintID(handle.ID())
			if firstTaskID == 0 {
				firstTaskID = uID
			}
			targetPort := int64(dartPort)
			if targetPort <= 0 {
				targetPort = inst.DartPort
			}
			setupTaskDartCallbacks(handle, uID, targetPort)
		}
	}

	inst.Engine.Clipboard().Clear()

	if outResult != nil {
		outResult.success = 1
		outResult.handle = C.uint64_t(firstTaskID)
		outResult.value = C.int64_t(len(plans))
	}
	return C.uint64_t(firstTaskID)
}

//export Freebox_ClipboardClear
func Freebox_ClipboardClear(engineHandle C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil {
		return 0
	}
	inst.Engine.Clipboard().Clear()
	return 1
}

//export Freebox_ClipboardCount
func Freebox_ClipboardCount(engineHandle C.uint64_t) C.int32_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil {
		return 0
	}
	return C.int32_t(inst.Engine.Clipboard().Count())
}
