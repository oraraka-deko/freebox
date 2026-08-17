package main

/*
#cgo CFLAGS: -I${SRCDIR}
#include "include/freebox_bridge.h"
#include <stdbool.h>

int32_t Freebox_InitDartApiDL(void* data);
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
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"freebox/api"
	"freebox/auth"
	"freebox/cert"
	"freebox/engine"
	"freebox/meta"
	"freebox/remotes"
	"freebox/storage"
	"freebox/telegram"
	"freebox/thumbnail"
	"freebox/vfs"
)

// FreeboxInstance encapsulates all core state for a bridge session.
type FreeboxInstance struct {
	Handle      uint64
	Engine      *engine.Engine
	DB          *storage.DB
	AuthMgr     *auth.Manager
	Mounts      *vfs.Registry
	TelegramMgr *telegram.Manager
	ThumbMgr    *thumbnail.Engine
	MetaMgr     *meta.Manager
	RemotesMgr  *remotes.Manager
	CertMgr     *cert.Manager
	Server      *api.Server
	Servers     map[uint8]any
	serversMu   sync.RWMutex
	DartPort    int64
}

var (
	instanceMap  = make(map[uint64]*FreeboxInstance)
	instanceMu   sync.RWMutex
	handleSeq    uint64
	taskIDToUint = make(map[string]uint64)
	uintToTaskID = make(map[uint64]string)
	idMapMu      sync.RWMutex
	taskIDSeq    uint64
)

func registerInstance(inst *FreeboxInstance) uint64 {
	h := atomic.AddUint64(&handleSeq, 1)
	inst.Handle = h
	if inst.Servers == nil {
		inst.Servers = make(map[uint8]any)
	}
	instanceMu.Lock()
	instanceMap[h] = inst
	instanceMu.Unlock()
	return h
}

func getInstance(h uint64) *FreeboxInstance {
	instanceMu.RLock()
	defer instanceMu.RUnlock()
	return instanceMap[h]
}

func unregisterInstance(h uint64) *FreeboxInstance {
	instanceMu.Lock()
	defer instanceMu.Unlock()
	inst := instanceMap[h]
	delete(instanceMap, h)
	return inst
}

func getTaskUintID(stringID string) uint64 {
	idMapMu.Lock()
	defer idMapMu.Unlock()
	if id, exists := taskIDToUint[stringID]; exists {
		return id
	}
	id := atomic.AddUint64(&taskIDSeq, 1)
	taskIDToUint[stringID] = id
	uintToTaskID[id] = stringID
	return id
}

func getTaskStringID(uintID uint64) string {
	idMapMu.RLock()
	defer idMapMu.RUnlock()
	return uintToTaskID[uintID]
}

//export Freebox_InitDartApi
func Freebox_InitDartApi(data unsafe.Pointer) int32 {
	return int32(C.Freebox_InitDartApiDL(data))
}

//export Freebox_InitEngine
func Freebox_InitEngine(
	dbPathPtr *C.uint8_t, dbPathLen C.int32_t,
	passphrasePtr *C.uint8_t, passphraseLen C.int32_t,
	maxWorkers C.int32_t,
	streamPortPtr *C.uint8_t, streamPortLen C.int32_t,
	outResult *C.FreeboxResultC,
) uint64 {
	dbPath := cBytesToGoString(dbPathPtr, dbPathLen)
	if dbPath == "" {
		dbPath = "./data/freebox.db"
	}
	passphrase := cBytesToGoString(passphrasePtr, passphraseLen)
	if passphrase == "" {
		passphrase = "freebox-secret-passphrase"
	}
	streamPort := cBytesToGoString(streamPortPtr, streamPortLen)
	if streamPort == "" {
		streamPort = ":8090"
	}

	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: passphrase,
	})
	if err != nil {
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("failed opening DB: " + err.Error())
		}
		return 0
	}

	authMgr, err := auth.NewManager(db, auth.ManagerConfig{
		SessionTTL:       7 * 24 * time.Hour,
		DefaultAdminUser: "admin",
		DefaultAdminPass: "admin123",
	})
	if err != nil {
		db.Close()
		if outResult != nil {
			outResult.success = 0
			outResult.error_msg = stringToCBuffer("failed initializing auth manager: " + err.Error())
		}
		return 0
	}

	workers := int(maxWorkers)
	if workers <= 0 {
		workers = 4
	}

	eng := engine.NewEngine(engine.EngineConfig{
		MaxWorkers: workers,
		StreamPort: streamPort,
		DB:         db,
	})

	reg := vfs.NewRegistry(db)
	_ = reg.LoadAll()
	if _, ok := reg.Get("local"); !ok {
		_ = reg.Mount("local", vfs.MountConfig{Type: "local", Path: "."}, false)
	}

	baseDir := filepath.Dir(dbPath)
	tgDir := filepath.Join(baseDir, "telegram")
	tgMgr, _ := telegram.NewManager(db, telegram.AuthConfig{
		SessionBaseDir: tgDir,
	})

	thumbDir := filepath.Join(baseDir, "thumbnails")
	thumbMgr := thumbnail.NewEngine(thumbnail.Config{
		CacheDir:    thumbDir,
		MaxMemoryMB: 64,
	})

	metaMgr := meta.NewManager(db)
	remotesMgr := remotes.NewManager(db, reg)
	certMgr := cert.NewManager(db)

	inst := &FreeboxInstance{
		Engine:      eng,
		DB:          db,
		AuthMgr:     authMgr,
		Mounts:      reg,
		TelegramMgr: tgMgr,
		ThumbMgr:    thumbMgr,
		MetaMgr:     metaMgr,
		RemotesMgr:  remotesMgr,
		CertMgr:     certMgr,
		Servers:     make(map[uint8]any),
	}

	h := registerInstance(inst)
	if outResult != nil {
		outResult.success = 1
		outResult.handle = C.uint64_t(h)
	}
	return h
}

//export Freebox_RegisterDartPort
func Freebox_RegisterDartPort(engineHandle C.uint64_t, dartPort C.int64_t) C.uint8_t {
	inst := getInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}
	inst.DartPort = int64(dartPort)
	return 1
}

//export Freebox_CloseEngine
func Freebox_CloseEngine(engineHandle C.uint64_t) C.uint8_t {
	inst := unregisterInstance(uint64(engineHandle))
	if inst == nil {
		return 0
	}

	inst.serversMu.Lock()
	for _, s := range inst.Servers {
		if closer, ok := s.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	inst.serversMu.Unlock()

	if inst.Server != nil {
		_ = inst.Server.Close()
	}
	if inst.Engine != nil {
		inst.Engine.Close()
	}
	if inst.DB != nil {
		_ = inst.DB.Close()
	}
	return 1
}

func uint64ToC(val uint64) C.uint64_t {
	return C.uint64_t(val)
}

func InitEngineHelper(dbPath, passphrase string, maxWorkers int, streamPort string) (uint64, error) {
	dbBytes := []byte(dbPath)
	passBytes := []byte(passphrase)
	portBytes := []byte(streamPort)
	var resC C.FreeboxResultC

	var dbPtr, passPtr, portPtr *C.uint8_t
	if len(dbBytes) > 0 {
		dbPtr = (*C.uint8_t)(unsafe.Pointer(&dbBytes[0]))
	}
	if len(passBytes) > 0 {
		passPtr = (*C.uint8_t)(unsafe.Pointer(&passBytes[0]))
	}
	if len(portBytes) > 0 {
		portPtr = (*C.uint8_t)(unsafe.Pointer(&portBytes[0]))
	}

	h := Freebox_InitEngine(
		dbPtr, C.int32_t(len(dbBytes)),
		passPtr, C.int32_t(len(passBytes)),
		C.int32_t(maxWorkers),
		portPtr, C.int32_t(len(portBytes)),
		&resC,
	)
	if h == 0 || resC.success == 0 {
		return 0, errors.New("failed initializing engine")
	}
	return uint64(h), nil
}

func SubmitTaskBytesHelper(engineHandle uint64, taskTypeCode uint8, srcBytes, dstBytes, dataBytes []byte, priority int32, dartPort int64) uint64 {
	var srcPtr, dstPtr, dataPtr *C.uint8_t
	if len(srcBytes) > 0 {
		srcPtr = (*C.uint8_t)(unsafe.Pointer(&srcBytes[0]))
	}
	if len(dstBytes) > 0 {
		dstPtr = (*C.uint8_t)(unsafe.Pointer(&dstBytes[0]))
	}
	if len(dataBytes) > 0 {
		dataPtr = (*C.uint8_t)(unsafe.Pointer(&dataBytes[0]))
	}

	var resC C.FreeboxResultC
	uID := Freebox_SubmitTaskBytes(
		C.uint64_t(engineHandle),
		C.FreeboxTaskType(taskTypeCode),
		srcPtr, C.int32_t(len(srcBytes)),
		dstPtr, C.int32_t(len(dstBytes)),
		dataPtr, C.int32_t(len(dataBytes)),
		C.int32_t(priority),
		C.int64_t(dartPort),
		&resC,
	)
	return uint64(uID)
}

func SubmitTaskRunesHelper(engineHandle uint64, taskTypeCode uint8, srcRunes, dstRunes []rune, priority int32, dartPort int64) uint64 {
	var srcPtr, dstPtr *C.uint32_t
	if len(srcRunes) > 0 {
		srcPtr = (*C.uint32_t)(unsafe.Pointer(&srcRunes[0]))
	}
	if len(dstRunes) > 0 {
		dstPtr = (*C.uint32_t)(unsafe.Pointer(&dstRunes[0]))
	}

	var resC C.FreeboxResultC
	uID := Freebox_SubmitTaskRunes(
		C.uint64_t(engineHandle),
		C.FreeboxTaskType(taskTypeCode),
		srcPtr, C.int32_t(len(srcRunes)),
		dstPtr, C.int32_t(len(dstRunes)),
		C.int32_t(priority),
		C.int64_t(dartPort),
		&resC,
	)
	return uint64(uID)
}

func ClipboardCopyHelper(engineHandle uint64, path []byte) error {
	var pPtr *C.uint8_t
	if len(path) > 0 {
		pPtr = (*C.uint8_t)(unsafe.Pointer(&path[0]))
	}
	var resC C.FreeboxResultC
	if ok := Freebox_ClipboardCopy(C.uint64_t(engineHandle), pPtr, C.int32_t(len(path)), &resC); ok == 0 {
		return errors.New("clipboard copy failed")
	}
	return nil
}

func ClipboardPasteHelper(engineHandle uint64, dstDir []byte) (uint64, error) {
	var pPtr *C.uint8_t
	if len(dstDir) > 0 {
		pPtr = (*C.uint8_t)(unsafe.Pointer(&dstDir[0]))
	}
	var resC C.FreeboxResultC
	tID := Freebox_ClipboardPaste(C.uint64_t(engineHandle), pPtr, C.int32_t(len(dstDir)), 0, &resC)
	if tID == 0 {
		return 0, errors.New("clipboard paste failed")
	}
	return uint64(tID), nil
}

func SearchHelper(engineHandle uint64, rootPath, namePat, contentPat, replacement []byte, matchType, target uint8, isReplace, caseSensitive bool) (uint64, error) {
	var rootPtr, namePtr, contentPtr, repPtr *C.uint8_t
	if len(rootPath) > 0 {
		rootPtr = (*C.uint8_t)(unsafe.Pointer(&rootPath[0]))
	}
	if len(namePat) > 0 {
		namePtr = (*C.uint8_t)(unsafe.Pointer(&namePat[0]))
	}
	if len(contentPat) > 0 {
		contentPtr = (*C.uint8_t)(unsafe.Pointer(&contentPat[0]))
	}
	if len(replacement) > 0 {
		repPtr = (*C.uint8_t)(unsafe.Pointer(&replacement[0]))
	}

	var isRep, isCase C.uint8_t
	if isReplace {
		isRep = 1
	}
	if caseSensitive {
		isCase = 1
	}

	var resC C.FreeboxResultC
	tID := Freebox_Search(
		C.uint64_t(engineHandle),
		rootPtr, C.int32_t(len(rootPath)),
		namePtr, C.int32_t(len(namePat)),
		contentPtr, C.int32_t(len(contentPat)),
		repPtr, C.int32_t(len(replacement)),
		C.FreeboxSearchMatchType(matchType),
		C.FreeboxSearchTarget(target),
		isRep, isCase,
		0,
		&resC,
	)
	if tID == 0 {
		return 0, errors.New("search failed")
	}
	return uint64(tID), nil
}

func DedupHelper(engineHandle uint64, rootPath []byte, method, action, keepPolicy uint8, minSize int64, maxWorkers int) (uint64, error) {
	var rootPtr *C.uint8_t
	if len(rootPath) > 0 {
		rootPtr = (*C.uint8_t)(unsafe.Pointer(&rootPath[0]))
	}
	var resC C.FreeboxResultC
	tID := Freebox_DedupScan(
		C.uint64_t(engineHandle),
		rootPtr, C.int32_t(len(rootPath)),
		C.FreeboxDedupMethod(method),
		C.FreeboxDedupAction(action),
		C.FreeboxDedupKeepPolicy(keepPolicy),
		C.int64_t(minSize),
		C.int32_t(maxWorkers),
		0,
		&resC,
	)
	if tID == 0 {
		return 0, errors.New("dedup failed")
	}
	return uint64(tID), nil
}

func ArchiveCompressHelper(engineHandle uint64, format uint8, srcPath, dstPath []byte) (uint64, error) {
	var srcPtr, dstPtr *C.uint8_t
	if len(srcPath) > 0 {
		srcPtr = (*C.uint8_t)(unsafe.Pointer(&srcPath[0]))
	}
	if len(dstPath) > 0 {
		dstPtr = (*C.uint8_t)(unsafe.Pointer(&dstPath[0]))
	}
	var resC C.FreeboxResultC
	tID := Freebox_ArchiveCompress(
		C.uint64_t(engineHandle),
		C.FreeboxArchiveFormat(format),
		srcPtr, C.int32_t(len(srcPath)),
		dstPtr, C.int32_t(len(dstPath)),
		nil, 0,
		0,
		&resC,
	)
	if tID == 0 {
		return 0, errors.New("archive compress failed")
	}
	return uint64(tID), nil
}

func ArchivePreviewHelper(engineHandle uint64, archivePath []byte) (int, error) {
	var arcPtr *C.uint8_t
	if len(archivePath) > 0 {
		arcPtr = (*C.uint8_t)(unsafe.Pointer(&archivePath[0]))
	}
	var resC C.FreeboxResultC
	n := Freebox_ArchivePreview(
		C.uint64_t(engineHandle),
		arcPtr, C.int32_t(len(archivePath)),
		nil, 0,
		&resC,
	)
	if n < 0 {
		return 0, errors.New("archive preview failed")
	}
	return int(n), nil
}

func ExtractMetadataHelper(engineHandle uint64, srcPath []byte) (string, error) {
	var srcPtr *C.uint8_t
	if len(srcPath) > 0 {
		srcPtr = (*C.uint8_t)(unsafe.Pointer(&srcPath[0]))
	}
	var metaC C.FreeboxMediaMetaC
	var resC C.FreeboxResultC
	if ok := Freebox_ExtractMetadata(C.uint64_t(engineHandle), srcPtr, C.int32_t(len(srcPath)), &metaC, &resC); ok == 0 {
		return "", errors.New("extract metadata failed")
	}
	return cBytesToGoString(metaC.title.ptr, metaC.title.len), nil
}

func SignFileHelper(engineHandle uint64, srcPath, privKeyPEM []byte) (string, error) {
	var srcPtr, keyPtr *C.uint8_t
	if len(srcPath) > 0 {
		srcPtr = (*C.uint8_t)(unsafe.Pointer(&srcPath[0]))
	}
	if len(privKeyPEM) > 0 {
		keyPtr = (*C.uint8_t)(unsafe.Pointer(&privKeyPEM[0]))
	}
	var sigPtr *C.uint8_t
	var sigLen C.int32_t
	var resC C.FreeboxResultC
	if ok := Freebox_SignFile(C.uint64_t(engineHandle), srcPtr, C.int32_t(len(srcPath)), keyPtr, C.int32_t(len(privKeyPEM)), &sigPtr, &sigLen, &resC); ok == 0 {
		return "", errors.New("sign file failed")
	}
	return cBytesToGoString(sigPtr, sigLen), nil
}

func VerifyFileSignatureHelper(engineHandle uint64, srcPath, sigHex, pubKeyPEM []byte) bool {
	var srcPtr, sigPtr, keyPtr *C.uint8_t
	if len(srcPath) > 0 {
		srcPtr = (*C.uint8_t)(unsafe.Pointer(&srcPath[0]))
	}
	if len(sigHex) > 0 {
		sigPtr = (*C.uint8_t)(unsafe.Pointer(&sigHex[0]))
	}
	if len(pubKeyPEM) > 0 {
		keyPtr = (*C.uint8_t)(unsafe.Pointer(&pubKeyPEM[0]))
	}
	var resC C.FreeboxResultC
	return Freebox_VerifyFileSignature(C.uint64_t(engineHandle), srcPtr, C.int32_t(len(srcPath)), sigPtr, C.int32_t(len(sigHex)), keyPtr, C.int32_t(len(pubKeyPEM)), &resC) != 0
}

func StartHTTPServerHelper(engineHandle uint64, port []byte) error {
	var portPtr *C.uint8_t
	if len(port) > 0 {
		portPtr = (*C.uint8_t)(unsafe.Pointer(&port[0]))
	}
	var resC C.FreeboxResultC
	if ok := Freebox_StartHTTPServer(C.uint64_t(engineHandle), portPtr, C.int32_t(len(port)), nil, 0, &resC); ok == 0 {
		return errors.New("start http server failed")
	}
	return nil
}

func StopHTTPServerHelper(engineHandle uint64) error {
	if ok := Freebox_StopHTTPServer(C.uint64_t(engineHandle)); ok == 0 {
		return errors.New("stop http server failed")
	}
	return nil
}

func main() {}
