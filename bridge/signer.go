package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"freebox/signer"
)

//export Freebox_SignFile
func Freebox_SignFile(
	engineHandle C.uint64_t,
	filePathPtr *C.uint8_t, filePathLen C.int32_t,
	privKeyPEMPtr *C.uint8_t, privKeyPEMLen C.int32_t,
	outSigPtr **C.uint8_t, outSigLen *C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	if outSigPtr == nil || outSigLen == nil {
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

	fullPath := cBytesToGoString(filePathPtr, filePathLen)
	privKeyPEM := cBytesToGoString(privKeyPEMPtr, privKeyPEMLen)

	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	sigRes, err := signer.SignDetached(targetFS, subPath, privKeyPEM)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	buf := stringToCBuffer(sigRes.SignatureHex)
	*outSigPtr = buf.ptr
	*outSigLen = buf.len

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_VerifyFileSignature
func Freebox_VerifyFileSignature(
	engineHandle C.uint64_t,
	filePathPtr *C.uint8_t, filePathLen C.int32_t,
	sigHexPtr *C.uint8_t, sigHexLen C.int32_t,
	pubKeyPEMPtr *C.uint8_t, pubKeyPEMLen C.int32_t,
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

	fullPath := cBytesToGoString(filePathPtr, filePathLen)
	sigHex := cBytesToGoString(sigHexPtr, sigHexLen)
	pubKeyPEM := cBytesToGoString(pubKeyPEMPtr, pubKeyPEMLen)

	mountName, subPath := resolveMountPath(fullPath)
	targetFS, ok := inst.Mounts.Get(mountName)
	if !ok || targetFS == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("mount not found: " + mountName)
		}
		return 0
	}

	valid, err := signer.VerifyDetached(targetFS, subPath, sigHex, pubKeyPEM)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer(err.Error())
		}
		return 0
	}

	if outResult != nil {
		if valid {
			outResult.success = 1
		} else {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("signature verification failed")
		}
	}

	if valid {
		return 1
	}
	return 0
}
