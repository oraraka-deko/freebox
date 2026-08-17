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
	"context"

	"freebox/dedup"
	"freebox/engine"
)

func dedupMethodFromCode(code C.FreeboxDedupMethod) dedup.DedupMethod {
	switch code {
	case C.FREEBOX_DEDUP_META:
		return dedup.MethodMeta
	case C.FREEBOX_DEDUP_QUICK:
		return dedup.MethodQuickHash
	case C.FREEBOX_DEDUP_MD5:
		return dedup.MethodMD5
	case C.FREEBOX_DEDUP_SHA256:
		return dedup.MethodSHA256
	case C.FREEBOX_DEDUP_SHA1:
		return dedup.MethodSHA1
	case C.FREEBOX_DEDUP_CRC32:
		return dedup.MethodCRC32
	default:
		return dedup.MethodQuickHash
	}
}

func dedupActionFromCode(code C.FreeboxDedupAction) dedup.DedupAction {
	switch code {
	case C.FREEBOX_DEDUP_ACTION_DELETE:
		return dedup.ActionDelete
	default:
		return dedup.ActionReportOnly
	}
}

func dedupKeepPolicyFromCode(code C.FreeboxDedupKeepPolicy) dedup.KeepPolicy {
	switch code {
	case C.FREEBOX_DEDUP_KEEP_NEWEST:
		return dedup.KeepNewest
	case C.FREEBOX_DEDUP_KEEP_SHORTEST:
		return dedup.KeepShortestPath
	case C.FREEBOX_DEDUP_KEEP_FIRST:
		return dedup.KeepFirst
	default:
		return dedup.KeepOldest
	}
}

//export Freebox_DedupScan
func Freebox_DedupScan(
	engineHandle C.uint64_t,
	rootPathPtr *C.uint8_t, rootPathLen C.int32_t,
	methodCode C.FreeboxDedupMethod,
	actionCode C.FreeboxDedupAction,
	keepPolicyCode C.FreeboxDedupKeepPolicy,
	minSize C.int64_t,
	maxWorkers C.int32_t,
	dartPort C.int64_t,
	outResult *C.FreeboxResultC,
) C.uint64_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil || inst.Engine == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(rootPathPtr, rootPathLen)
	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Dedup scan " + fullPath,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				dedupEngine := dedup.NewEngine(targetFS)
				workers := int(maxWorkers)
				if workers <= 0 {
					workers = 4
				}

				opts := dedup.DedupOptions{
					RootPath:    subPath,
					Method:      dedupMethodFromCode(methodCode),
					Action:      dedupActionFromCode(actionCode),
					KeepPolicy:  dedupKeepPolicyFromCode(keepPolicyCode),
					MinFileSize: int64(minSize),
					MaxWorkers:  workers,
					OnProgress: func(p dedup.DedupProgress) {
						handle.UpdateProgress(p.BytesScanned, 0, p.CurrentPath)
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
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	uID := getTaskUintID(handle.ID())
	targetPort := int64(dartPort)
	if targetPort <= 0 {
		targetPort = inst.DartPort
	}
	setupTaskDartCallbacks(handle, uID, targetPort)

	if outResult != nil {
		outResult.success = 1
		outResult.handle = C.uint64_t(uID)
	}
	return C.uint64_t(uID)
}
