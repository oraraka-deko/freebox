package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"context"
	"strings"

	"freebox/archive"
	"freebox/engine"
)

func archiveTypeFromCode(code C.FreeboxArchiveFormat) archive.ArchiveType {
	switch code {
	case C.FREEBOX_ARCHIVE_TAR:
		return archive.TypeTar
	case C.FREEBOX_ARCHIVE_TGZ:
		return archive.TypeTarGz
	case C.FREEBOX_ARCHIVE_TBZ2:
		return archive.TypeTarBz
	default:
		return archive.TypeZip
	}
}

//export Freebox_ArchiveCompress
func Freebox_ArchiveCompress(
	engineHandle C.uint64_t,
	formatCode C.FreeboxArchiveFormat,
	srcPathPtr *C.uint8_t, srcPathLen C.int32_t,
	dstPathPtr *C.uint8_t, dstPathLen C.int32_t,
	passPtr *C.uint8_t, passLen C.int32_t,
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

	srcFullPath := cBytesToGoString(srcPathPtr, srcPathLen)
	dstFullPath := cBytesToGoString(dstPathPtr, dstPathLen)
	password := cBytesToGoString(passPtr, passLen)

	srcMount, srcSubPath := resolveMountPath(srcFullPath)
	dstMount, dstSubPath := resolveMountPath(dstFullPath)

	srcFS, ok := inst.Mounts.Get(srcMount)
	if !ok || srcFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("source mount not found: " + srcMount)
		}
		return 0
	}

	dstFS, ok := inst.Mounts.Get(dstMount)
	if !ok || dstFS == nil {
		dstFS = srcFS
		dstSubPath = srcSubPath + ".zip"
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Compress " + srcFullPath + " -> " + dstFullPath,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				opts := archive.CreateOptions{
					Type:     archiveTypeFromCode(formatCode),
					Password: password,
				}
				paths := strings.Split(srcSubPath, ",")
				return archive.Create(srcFS, dstSubPath, paths, opts)
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

//export Freebox_ArchiveExtract
func Freebox_ArchiveExtract(
	engineHandle C.uint64_t,
	srcArchivePtr *C.uint8_t, srcArchiveLen C.int32_t,
	dstDirPtr *C.uint8_t, dstDirLen C.int32_t,
	passPtr *C.uint8_t, passLen C.int32_t,
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

	srcFullPath := cBytesToGoString(srcArchivePtr, srcArchiveLen)
	dstFullPath := cBytesToGoString(dstDirPtr, dstDirLen)
	password := cBytesToGoString(passPtr, passLen)

	srcMount, srcSubPath := resolveMountPath(srcFullPath)
	dstMount, dstSubPath := resolveMountPath(dstFullPath)

	srcFS, ok := inst.Mounts.Get(srcMount)
	if !ok || srcFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("source mount not found: " + srcMount)
		}
		return 0
	}

	dstFS, ok := inst.Mounts.Get(dstMount)
	if !ok || dstFS == nil {
		dstFS = srcFS
	}

	t := &engine.Task{
		Type:        engine.TaskTypeCustom,
		Description: "Extract " + srcFullPath + " -> " + dstFullPath,
		Priority:    5,
		Params: engine.TaskParams{
			Action: func(ctx context.Context, handle *engine.TaskHandle) error {
				opts := archive.ExtractOptions{
					Password:  password,
					Overwrite: true,
				}
				return archive.Extract(srcFS, srcSubPath, dstSubPath, opts)
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

//export Freebox_ArchivePreview
func Freebox_ArchivePreview(
	engineHandle C.uint64_t,
	archivePathPtr *C.uint8_t, archivePathLen C.int32_t,
	passPtr *C.uint8_t, passLen C.int32_t,
	outResult *C.FreeboxResultC,
) C.int32_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	fullPath := cBytesToGoString(archivePathPtr, archivePathLen)
	password := cBytesToGoString(passPtr, passLen)

	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	entries, err := archive.Preview(targetFS, subPath, password)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outResult != nil {
		outResult.success = 1
		outResult.value = C.int64_t(len(entries))
	}
	return C.int32_t(len(entries))
}
