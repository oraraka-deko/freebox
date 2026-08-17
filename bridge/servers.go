package main

/*
#include "include/freebox_bridge.h"
*/
import "C"
import (
	"net"

	"freebox/api"
	"freebox/dlna"
	"freebox/ftp"
	fbHttp "freebox/http"
)

//export Freebox_StartAPIServer
func Freebox_StartAPIServer(
	engineHandle C.uint64_t,
	portPtr *C.uint8_t, portLen C.int32_t,
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

	port := cBytesToGoString(portPtr, portLen)
	if port == "" {
		port = ":8080"
	}

	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()

	if inst.Server != nil {
		_ = inst.Server.Close()
	}

	srv := api.NewServer(api.ServerConfig{
		Addr:        port,
		AuthMgr:     inst.AuthMgr,
		StorageDB:   inst.DB,
		Mounts:      inst.Mounts,
		Engine:      inst.Engine,
		StreamServ:  inst.Engine.StreamServer(),
		MetaMgr:     inst.MetaMgr,
		RemotesMgr:  inst.RemotesMgr,
		ThumbMgr:    inst.ThumbMgr,
		CertMgr:     inst.CertMgr,
		TelegramMgr: inst.TelegramMgr,
	})

	inst.Server = srv
	go func() {
		_ = srv.Start()
	}()

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_StopAPIServer
func Freebox_StopAPIServer(engineHandle C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil || inst.Server == nil {
		return 0
	}
	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()
	_ = inst.Server.Close()
	inst.Server = nil
	return 1
}

//export Freebox_StartHTTPServer
func Freebox_StartHTTPServer(
	engineHandle C.uint64_t,
	portPtr *C.uint8_t, portLen C.int32_t,
	rootDirPtr *C.uint8_t, rootDirLen C.int32_t,
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

	port := cBytesToGoString(portPtr, portLen)
	if port == "" {
		port = ":8081"
	}
	rootDir := cBytesToGoString(rootDirPtr, rootDirLen)

	cfg := fbHttp.DefaultConfig()
	cfg.RootDir = rootDir

	srv := fbHttp.NewServer(cfg)
	l, err := net.Listen("tcp", port)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("http listen failed: " + err.Error())
		}
		return 0
	}

	inst.serversMu.Lock()
	inst.Servers[C.FREEBOX_SERVER_HTTP] = srv
	inst.serversMu.Unlock()

	go func() {
		_ = srv.Serve(l)
	}()

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_StopHTTPServer
func Freebox_StopHTTPServer(engineHandle C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()
	if srv, ok := inst.Servers[C.FREEBOX_SERVER_HTTP].(*fbHttp.Server); ok {
		_ = srv.Close()
		delete(inst.Servers, C.FREEBOX_SERVER_HTTP)
		return 1
	}
	return 0
}

//export Freebox_StartFTPServer
func Freebox_StartFTPServer(
	engineHandle C.uint64_t,
	portPtr *C.uint8_t, portLen C.int32_t,
	rootDirPtr *C.uint8_t, rootDirLen C.int32_t,
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

	port := cBytesToGoString(portPtr, portLen)
	if port == "" {
		port = ":2121"
	}
	rootDir := cBytesToGoString(rootDirPtr, rootDirLen)

	cfg := ftp.DefaultConfig()
	cfg.RootDir = rootDir

	srv := ftp.NewServer(cfg)
	l, err := net.Listen("tcp", port)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("ftp listen failed: " + err.Error())
		}
		return 0
	}

	inst.serversMu.Lock()
	inst.Servers[C.FREEBOX_SERVER_FTP] = srv
	inst.serversMu.Unlock()

	go func() {
		_ = srv.Serve(l)
	}()

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_StopFTPServer
func Freebox_StopFTPServer(engineHandle C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()
	if srv, ok := inst.Servers[C.FREEBOX_SERVER_FTP].(*ftp.Server); ok {
		_ = srv.Close()
		delete(inst.Servers, C.FREEBOX_SERVER_FTP)
		return 1
	}
	return 0
}

//export Freebox_StartDLNAServer
func Freebox_StartDLNAServer(
	engineHandle C.uint64_t,
	friendlyNamePtr *C.uint8_t, friendlyNameLen C.int32_t,
	portPtr *C.uint8_t, portLen C.int32_t,
	rootDirPtr *C.uint8_t, rootDirLen C.int32_t,
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

	name := cBytesToGoString(friendlyNamePtr, friendlyNameLen)
	if name == "" {
		name = "Freebox DLNA Media Server"
	}
	port := cBytesToGoString(portPtr, portLen)
	if port == "" {
		port = ":8200"
	}
	rootDir := cBytesToGoString(rootDirPtr, rootDirLen)

	cfg := dlna.DefaultConfig()
	cfg.FriendlyName = name
	cfg.RootDir = rootDir

	srv := dlna.NewServer(cfg)
	l, err := net.Listen("tcp", port)
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("dlna listen failed: " + err.Error())
		}
		return 0
	}

	inst.serversMu.Lock()
	inst.Servers[C.FREEBOX_SERVER_DLNA] = srv
	inst.serversMu.Unlock()

	go func() {
		_ = srv.Serve(l)
	}()

	if outResult != nil {
		outResult.success = 1
	}
	return 1
}

//export Freebox_StopDLNAServer
func Freebox_StopDLNAServer(engineHandle C.uint64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	inst.serversMu.Lock()
	defer inst.serversMu.Unlock()
	if srv, ok := inst.Servers[C.FREEBOX_SERVER_DLNA].(*dlna.Server); ok {
		_ = srv.Close()
		delete(inst.Servers, C.FREEBOX_SERVER_DLNA)
		return 1
	}
	return 0
}
