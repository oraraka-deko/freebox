package main

/*
#include <stdlib.h>
#include <stdint.h>
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"sync"
	"unsafe"
)

// Global memory handle tracker for pointers passed to C/Dart.
var (
	memoryMu sync.Mutex
	allocMap = make(map[unsafe.Pointer]bool)
)

// trackAlloc records an allocated unsafe pointer so we can safely free it.
func trackAlloc(ptr unsafe.Pointer) unsafe.Pointer {
	if ptr == nil {
		return nil
	}
	memoryMu.Lock()
	allocMap[ptr] = true
	memoryMu.Unlock()
	return ptr
}

//export Freebox_FreeBuffer
func Freebox_FreeBuffer(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	memoryMu.Lock()
	if allocMap[ptr] {
		delete(allocMap, ptr)
		C.free(ptr)
	}
	memoryMu.Unlock()
}

// bytesToCBuffer converts a Go byte slice into a C FreeboxByteBuffer.
func bytesToCBuffer(b []byte) C.FreeboxByteBuffer {
	if len(b) == 0 {
		return C.FreeboxByteBuffer{ptr: nil, len: 0}
	}
	cPtr := C.CBytes(b)
	trackAlloc(cPtr)
	return C.FreeboxByteBuffer{
		ptr: (*C.uint8_t)(cPtr),
		len: C.int32_t(len(b)),
	}
}

// stringToCBuffer converts a Go string to a C FreeboxByteBuffer without String overhead.
func stringToCBuffer(s string) C.FreeboxByteBuffer {
	return bytesToCBuffer([]byte(s))
}

// cBufferToGoBytes converts an incoming C FreeboxByteBuffer to Go []byte using unsafe.Slice.
func cBufferToGoBytes(buf C.FreeboxByteBuffer) []byte {
	if buf.ptr == nil || buf.len <= 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(buf.ptr)), int(buf.len))
}

// cRuneBufferToGoString converts an incoming uint32_t rune buffer to a Go string using unsafe.Slice.
func cRuneBufferToGoString(ptr *C.uint32_t, len C.int32_t) string {
	if ptr == nil || len <= 0 {
		return ""
	}
	runesSlice := unsafe.Slice((*rune)(unsafe.Pointer(ptr)), int(len))
	return string(runesSlice)
}

// cBytesToGoString converts an incoming uint8_t byte buffer to a Go string using unsafe.Slice.
func cBytesToGoString(ptr *C.uint8_t, len C.int32_t) string {
	if ptr == nil || len <= 0 {
		return ""
	}
	bytesSlice := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), int(len))
	return string(bytesSlice)
}
