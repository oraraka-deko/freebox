package main

/*
#include "include/freebox_bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"io"
	"strings"
	"unsafe"

	"freebox/vfs"
)

func resolveMountPath(fullPath string) (string, string) {
	parts := strings.SplitN(fullPath, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "local", fullPath
}

//export Freebox_Mount
func Freebox_Mount(
	engineHandle C.uint64_t,
	namePtr *C.uint8_t, nameLen C.int32_t,
	typePtr *C.uint8_t, typeLen C.int32_t,
	pathPtr *C.uint8_t, pathLen C.int32_t,
	readOnly C.uint8_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle or mounts registry")
		}
		return 0
	}

	name := cBytesToGoString(namePtr, nameLen)
	mType := cBytesToGoString(typePtr, typeLen)
	mPath := cBytesToGoString(pathPtr, pathLen)

	cfg := vfs.MountConfig{
		Type: mType,
		Path: mPath,
	}

	if err := inst.Mounts.Mount(name, cfg, true); err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_Unmount
func Freebox_Unmount(
	engineHandle C.uint64_t,
	namePtr *C.uint8_t, nameLen C.int32_t,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil {
		return 0
	}
	name := cBytesToGoString(namePtr, nameLen)
	if err := inst.Mounts.Unmount(name); err != nil {
		return 0
	}
	return 1
}

//export Freebox_ListDir
func Freebox_ListDir(
	engineHandle C.uint64_t,
	pathPtr *C.uint8_t, pathLen C.int32_t,
	outFilesArray **C.FreeboxFileInfoC,
	outCount *C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	if outFilesArray == nil || outCount == nil {
		return 0
	}
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil {
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

	files, err := targetFS.ReadDir(subPath)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	count := len(files)
	*outCount = C.int32_t(count)
	if count == 0 {
		*outFilesArray = nil
		if outResult != nil {
			outResult.success = 1
		}
		return 1
	}

	cArrSize := C.size_t(count) * C.size_t(unsafe.Sizeof(C.FreeboxFileInfoC{}))
	cArr := (*C.FreeboxFileInfoC)(C.malloc(cArrSize))
	trackAlloc(unsafe.Pointer(cArr))

	sliceHeader := unsafe.Slice(cArr, count)
	for i, f := range files {
		var isDir C.uint8_t
		if f.IsDir {
			isDir = 1
		}
		sliceHeader[i] = C.FreeboxFileInfoC{
			name:          stringToCBuffer(f.Name),
			size:          C.int64_t(f.Size),
			mod_time_unix: C.int64_t(f.ModTime.UnixNano()),
			is_dir:        isDir,
			mode:          0,
		}
	}

	*outFilesArray = cArr
	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_ReadFileBytes
func Freebox_ReadFileBytes(
	engineHandle C.uint64_t,
	pathPtr *C.uint8_t, pathLen C.int32_t,
	outDataPtr **C.uint8_t, outDataLen *C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	if outDataPtr == nil || outDataLen == nil {
		return 0
	}
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil {
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

	r, err := targetFS.Open(subPath)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}
	defer r.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	buf := bytesToCBuffer(data)
	*outDataPtr = buf.ptr
	*outDataLen = buf.len

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_WriteFileBytes
func Freebox_WriteFileBytes(
	engineHandle C.uint64_t,
	pathPtr *C.uint8_t, pathLen C.int32_t,
	dataPtr *C.uint8_t, dataLen C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Mounts == nil {
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

	w, err := targetFS.Create(subPath)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}
	defer w.Close()

	content := cBufferToGoBytes(C.FreeboxByteBuffer{ptr: dataPtr, len: dataLen})
	if len(content) > 0 {
		if _, err := w.Write(content); err != nil {
			if outResult != nil {
				outResult.success = 0
				outResult.error_msg = stringToCBuffer(err.Error())
			}
			return 0
		}
	}

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}
