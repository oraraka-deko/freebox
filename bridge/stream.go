package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"net/http"

	"freebox/proxy"
)

//export Freebox_RegisterStream
func Freebox_RegisterStream(
	engineHandle C.uint64_t,
	namePtr *C.uint8_t, nameLen C.int32_t,
	urlPtr *C.uint8_t, urlLen C.int32_t,
	outResult *C.FreeboxResultC,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("invalid engine handle")
		}
		return 0
	}

	streamName := cBytesToGoString(namePtr, nameLen)
	streamURL := cBytesToGoString(urlPtr, urlLen)

	proxyServer := inst.Engine.StreamServer()
	if proxyServer == nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("stream server not initialized")
		}
		return 0
	}

	httpSrc, err := proxy.NewHTTPSource(streamURL, http.DefaultClient)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("failed creating HTTP stream source: " + err.Error())
		}
		return 0
	}

	proxyServer.RegisterSource(streamName, httpSrc)
	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_UnregisterStream
func Freebox_UnregisterStream(
	engineHandle C.uint64_t,
	namePtr *C.uint8_t, nameLen C.int32_t,
) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Engine == nil {
		return 0
	}

	streamName := cBytesToGoString(namePtr, nameLen)
	proxyServer := inst.Engine.StreamServer()
	if proxyServer != nil {
		proxyServer.UnregisterSource(streamName)
	}
	return 1
}
