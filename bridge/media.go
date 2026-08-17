package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"context"

	"freebox/thumbnail"
)

//export Freebox_GenerateThumbnail
func Freebox_GenerateThumbnail(
	engineHandle C.uint64_t,
	srcPathPtr *C.uint8_t, srcPathLen C.int32_t,
	width C.int32_t, height C.int32_t,
	quality C.int32_t,
	outDataPtr **C.uint8_t, outDataLen *C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	if outDataPtr == nil || outDataLen == nil {
		return 0
	}
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil || inst.ThumbMgr == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(srcPathPtr, srcPathLen)
	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	w := int(width)
	if w <= 0 {
		w = 256
	}
	h := int(height)
	if h <= 0 {
		h = 256
	}
	q := int(quality)
	if q <= 0 {
		q = 80
	}

	opts := thumbnail.ThumbnailOptions{
		Width:       w,
		Height:      h,
		Quality:     q,
		Format:      thumbnail.FormatJPEG,
		Placeholder: true,
	}

	thumbBytes, _, err := inst.ThumbMgr.GetOrGenerate(context.Background(), targetFS, subPath, opts)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	buf := bytesToCBuffer(thumbBytes)
	*outDataPtr = buf.ptr
	*outDataLen = buf.len

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_ExtractMetadata
func Freebox_ExtractMetadata(
	engineHandle C.uint64_t,
	srcPathPtr *C.uint8_t, srcPathLen C.int32_t,
	outMeta *C.FreeboxMediaMetaC,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil || inst.MetaMgr == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(srcPathPtr, srcPathLen)
	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	info, err := inst.MetaMgr.GetInfo(targetFS, subPath)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outMeta != nil {
		outMeta.title = stringToCBuffer(info.Name)
		outMeta.format = stringToCBuffer(info.MimeType)
		outMeta.duration_ms = 0
		outMeta.bitrate = C.int64_t(info.Size)
	}

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}
