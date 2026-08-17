package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"context"
	"encoding/json"

	"freebox/gdrive"
	"freebox/vfs"
)

//export Freebox_GDriveAuthorize
func Freebox_GDriveAuthorize(
	engineHandle C.uint64_t,
	clientIDPtr *C.uint8_t, clientIDLen C.int32_t,
	clientSecPtr *C.uint8_t, clientSecLen C.int32_t,
	credsJSONPtr *C.uint8_t, credsJSONLen C.int32_t,
	port C.int32_t,
	callbackPathPtr *C.uint8_t, callbackPathLen C.int32_t,
	outTokenJSONPtr **C.uint8_t, outTokenJSONLen *C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	cfg := gdrive.AuthConfig{
		ClientID:        cBytesToGoString(clientIDPtr, clientIDLen),
		ClientSecret:    cBytesToGoString(clientSecPtr, clientSecLen),
		CredentialsJSON: cBufferToGoBytes(C.FreeboxByteBuffer{ptr: credsJSONPtr, len: credsJSONLen}),
		Port:            int(port),
		CallbackPath:    cBytesToGoString(callbackPathPtr, callbackPathLen),
	}

	_, tok, err := gdrive.GetDriveService(context.Background(), cfg)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	tokBytes, err := json.Marshal(tok)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outTokenJSONPtr != nil && outTokenJSONLen != nil {
		cBuf := bytesToCBuffer(tokBytes)
		*outTokenJSONPtr = cBuf.ptr
		*outTokenJSONLen = cBuf.len
	}

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_GDriveMount
func Freebox_GDriveMount(
	engineHandle C.uint64_t,
	mountNamePtr *C.uint8_t, mountNameLen C.int32_t,
	clientIDPtr *C.uint8_t, clientIDLen C.int32_t,
	clientSecPtr *C.uint8_t, clientSecLen C.int32_t,
	tokenJSONPtr *C.uint8_t, tokenJSONLen C.int32_t,
	rootFolderIDPtr *C.uint8_t, rootFolderIDLen C.int32_t,
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

	mountName := cBytesToGoString(mountNamePtr, mountNameLen)
	rootFolderID := cBytesToGoString(rootFolderIDPtr, rootFolderIDLen)
	if rootFolderID == "" {
		rootFolderID = "root"
	}

	cfg := gdrive.AuthConfig{
		ClientID:     cBytesToGoString(clientIDPtr, clientIDLen),
		ClientSecret: cBytesToGoString(clientSecPtr, clientSecLen),
		TokenJSON:    cBufferToGoBytes(C.FreeboxByteBuffer{ptr: tokenJSONPtr, len: tokenJSONLen}),
	}

	srv, _, err := gdrive.GetDriveService(context.Background(), cfg)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("failed connecting Google Drive: " + err.Error())
		}
		return 0
	}

	gdriveFS := gdrive.NewGDriveFS(srv, rootFolderID)
	info := vfs.MountInfo{
		Name:   mountName,
		Type:   "gdrive",
		Path:   rootFolderID,
		Status: "active",
	}

	if err := inst.Mounts.RegisterCustom(mountName, gdriveFS, info); err != nil {
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
