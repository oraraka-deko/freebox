import 'dart:ffi' as ffi;
import 'types.dart';

typedef NativeInitDartApi = ffi.Int32 Function(ffi.Pointer<ffi.Void> data);
typedef DartInitDartApi = int Function(ffi.Pointer<ffi.Void> data);

typedef NativeInitEngine = ffi.Uint64 Function(
  ffi.Pointer<ffi.Uint8> dbPathPtr,
  ffi.Int32 dbPathLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  ffi.Int32 passLen,
  ffi.Int32 maxWorkers,
  ffi.Pointer<ffi.Uint8> streamPortPtr,
  ffi.Int32 streamPortLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartInitEngine = int Function(
  ffi.Pointer<ffi.Uint8> dbPathPtr,
  int dbPathLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  int passLen,
  int maxWorkers,
  ffi.Pointer<ffi.Uint8> streamPortPtr,
  int streamPortLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeRegisterDartPort = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Int64 dartPort,
);
typedef DartRegisterDartPort = int Function(
  int engineHandle,
  int dartPort,
);

typedef NativeCloseEngine = ffi.Uint8 Function(ffi.Uint64 engineHandle);
typedef DartCloseEngine = int Function(int engineHandle);

typedef NativeSubmitTaskBytes = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Uint8 taskTypeCode,
  ffi.Pointer<ffi.Uint8> srcPtr,
  ffi.Int32 srcLen,
  ffi.Pointer<ffi.Uint8> dstPtr,
  ffi.Int32 dstLen,
  ffi.Pointer<ffi.Uint8> dataPtr,
  ffi.Int32 dataLen,
  ffi.Int32 priority,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartSubmitTaskBytes = int Function(
  int engineHandle,
  int taskTypeCode,
  ffi.Pointer<ffi.Uint8> srcPtr,
  int srcLen,
  ffi.Pointer<ffi.Uint8> dstPtr,
  int dstLen,
  ffi.Pointer<ffi.Uint8> dataPtr,
  int dataLen,
  int priority,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeSubmitTaskRunes = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Uint8 taskTypeCode,
  ffi.Pointer<ffi.Uint32> srcRunesPtr,
  ffi.Int32 srcRunesLen,
  ffi.Pointer<ffi.Uint32> dstRunesPtr,
  ffi.Int32 dstRunesLen,
  ffi.Int32 priority,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartSubmitTaskRunes = int Function(
  int engineHandle,
  int taskTypeCode,
  ffi.Pointer<ffi.Uint32> srcRunesPtr,
  int srcRunesLen,
  ffi.Pointer<ffi.Uint32> dstRunesPtr,
  int dstRunesLen,
  int priority,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeTaskControl = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Uint64 taskId,
);
typedef DartTaskControl = int Function(
  int engineHandle,
  int taskId,
);

typedef NativeGetTaskProgress = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Uint64 taskId,
  ffi.Pointer<FreeboxTaskProgressC> outProgress,
);
typedef DartGetTaskProgress = int Function(
  int engineHandle,
  int taskId,
  ffi.Pointer<FreeboxTaskProgressC> outProgress,
);

typedef NativeListDir = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> pathPtr,
  ffi.Int32 pathLen,
  ffi.Pointer<ffi.Pointer<FreeboxFileInfoC>> outFilesArray,
  ffi.Pointer<ffi.Int32> outCount,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartListDir = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> pathPtr,
  int pathLen,
  ffi.Pointer<ffi.Pointer<FreeboxFileInfoC>> outFilesArray,
  ffi.Pointer<ffi.Int32> outCount,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeFreeBuffer = ffi.Void Function(ffi.Pointer<ffi.Void> ptr);
typedef DartFreeBuffer = void Function(ffi.Pointer<ffi.Void> ptr);

// Server Bindings
typedef NativeStartServer = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> portPtr,
  ffi.Int32 portLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartStartServer = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> portPtr,
  int portLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeStartServerWithRoot = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> portPtr,
  ffi.Int32 portLen,
  ffi.Pointer<ffi.Uint8> rootDirPtr,
  ffi.Int32 rootDirLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartStartServerWithRoot = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> portPtr,
  int portLen,
  ffi.Pointer<ffi.Uint8> rootDirPtr,
  int rootDirLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeStartDLNAServer = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> friendlyNamePtr,
  ffi.Int32 friendlyNameLen,
  ffi.Pointer<ffi.Uint8> portPtr,
  ffi.Int32 portLen,
  ffi.Pointer<ffi.Uint8> rootDirPtr,
  ffi.Int32 rootDirLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartStartDLNAServer = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> friendlyNamePtr,
  int friendlyNameLen,
  ffi.Pointer<ffi.Uint8> portPtr,
  int portLen,
  ffi.Pointer<ffi.Uint8> rootDirPtr,
  int rootDirLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeStopServer = ffi.Uint8 Function(ffi.Uint64 engineHandle);
typedef DartStopServer = int Function(int engineHandle);

// Clipboard Bindings
typedef NativeClipboardCopy = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> pathPtr,
  ffi.Int32 pathLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartClipboardCopy = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> pathPtr,
  int pathLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeClipboardPaste = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> dstDirPtr,
  ffi.Int32 dstDirLen,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartClipboardPaste = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> dstDirPtr,
  int dstDirLen,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeClipboardClear = ffi.Uint8 Function(ffi.Uint64 engineHandle);
typedef DartClipboardClear = int Function(int engineHandle);

typedef NativeClipboardCount = ffi.Int32 Function(ffi.Uint64 engineHandle);
typedef DartClipboardCount = int Function(int engineHandle);

// Dedup Bindings
typedef NativeDedupScan = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> rootPathPtr,
  ffi.Int32 rootPathLen,
  ffi.Uint8 methodCode,
  ffi.Uint8 actionCode,
  ffi.Uint8 keepPolicyCode,
  ffi.Int64 minSize,
  ffi.Int32 maxWorkers,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartDedupScan = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> rootPathPtr,
  int rootPathLen,
  int methodCode,
  int actionCode,
  int keepPolicyCode,
  int minSize,
  int maxWorkers,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

// Search Bindings
typedef NativeSearch = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> rootPathPtr,
  ffi.Int32 rootPathLen,
  ffi.Pointer<ffi.Uint8> namePatternPtr,
  ffi.Int32 namePatternLen,
  ffi.Pointer<ffi.Uint8> contentPatternPtr,
  ffi.Int32 contentPatternLen,
  ffi.Pointer<ffi.Uint8> replacementPtr,
  ffi.Int32 replacementLen,
  ffi.Uint8 matchTypeCode,
  ffi.Uint8 targetCode,
  ffi.Uint8 isReplace,
  ffi.Uint8 caseSensitive,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartSearch = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> rootPathPtr,
  int rootPathLen,
  ffi.Pointer<ffi.Uint8> namePatternPtr,
  int namePatternLen,
  ffi.Pointer<ffi.Uint8> contentPatternPtr,
  int contentPatternLen,
  ffi.Pointer<ffi.Uint8> replacementPtr,
  int replacementLen,
  int matchTypeCode,
  int targetCode,
  int isReplace,
  int caseSensitive,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

// Archive Bindings
typedef NativeArchiveCompress = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Uint8 formatCode,
  ffi.Pointer<ffi.Uint8> srcPathPtr,
  ffi.Int32 srcPathLen,
  ffi.Pointer<ffi.Uint8> dstPathPtr,
  ffi.Int32 dstPathLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  ffi.Int32 passLen,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartArchiveCompress = int Function(
  int engineHandle,
  int formatCode,
  ffi.Pointer<ffi.Uint8> srcPathPtr,
  int srcPathLen,
  ffi.Pointer<ffi.Uint8> dstPathPtr,
  int dstPathLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  int passLen,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeArchiveExtract = ffi.Uint64 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> srcArchivePtr,
  ffi.Int32 srcArchiveLen,
  ffi.Pointer<ffi.Uint8> dstDirPtr,
  ffi.Int32 dstDirLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  ffi.Int32 passLen,
  ffi.Int64 dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartArchiveExtract = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> srcArchivePtr,
  int srcArchiveLen,
  ffi.Pointer<ffi.Uint8> dstDirPtr,
  int dstDirLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  int passLen,
  int dartPort,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeArchivePreview = ffi.Int32 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> archivePathPtr,
  ffi.Int32 archivePathLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  ffi.Int32 passLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartArchivePreview = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> archivePathPtr,
  int archivePathLen,
  ffi.Pointer<ffi.Uint8> passPtr,
  int passLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

// Thumbnail & Metadata Bindings
typedef NativeGenerateThumbnail = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> srcPathPtr,
  ffi.Int32 srcPathLen,
  ffi.Int32 width,
  ffi.Int32 height,
  ffi.Int32 quality,
  ffi.Pointer<ffi.Pointer<ffi.Uint8>> outDataPtr,
  ffi.Pointer<ffi.Int32> outDataLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartGenerateThumbnail = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> srcPathPtr,
  int srcPathLen,
  int width,
  int height,
  int quality,
  ffi.Pointer<ffi.Pointer<ffi.Uint8>> outDataPtr,
  ffi.Pointer<ffi.Int32> outDataLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeExtractMetadata = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> srcPathPtr,
  ffi.Int32 srcPathLen,
  ffi.Pointer<FreeboxMediaMetaC> outMeta,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartExtractMetadata = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> srcPathPtr,
  int srcPathLen,
  ffi.Pointer<FreeboxMediaMetaC> outMeta,
  ffi.Pointer<FreeboxResultC> outResult,
);

// Signer Bindings
typedef NativeSignFile = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> filePathPtr,
  ffi.Int32 filePathLen,
  ffi.Pointer<ffi.Uint8> privKeyPEMPtr,
  ffi.Int32 privKeyPEMLen,
  ffi.Pointer<ffi.Pointer<ffi.Uint8>> outSigPtr,
  ffi.Pointer<ffi.Int32> outSigLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartSignFile = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> filePathPtr,
  int filePathLen,
  ffi.Pointer<ffi.Uint8> privKeyPEMPtr,
  int privKeyPEMLen,
  ffi.Pointer<ffi.Pointer<ffi.Uint8>> outSigPtr,
  ffi.Pointer<ffi.Int32> outSigLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

typedef NativeVerifyFileSignature = ffi.Uint8 Function(
  ffi.Uint64 engineHandle,
  ffi.Pointer<ffi.Uint8> filePathPtr,
  ffi.Int32 filePathLen,
  ffi.Pointer<ffi.Uint8> sigHexPtr,
  ffi.Int32 sigHexLen,
  ffi.Pointer<ffi.Uint8> pubKeyPEMPtr,
  ffi.Int32 pubKeyPEMLen,
  ffi.Pointer<FreeboxResultC> outResult,
);
typedef DartVerifyFileSignature = int Function(
  int engineHandle,
  ffi.Pointer<ffi.Uint8> filePathPtr,
  int filePathLen,
  ffi.Pointer<ffi.Uint8> sigHexPtr,
  int sigHexLen,
  ffi.Pointer<ffi.Uint8> pubKeyPEMPtr,
  int pubKeyPEMLen,
  ffi.Pointer<FreeboxResultC> outResult,
);

class FreeboxBindings {
  final ffi.DynamicLibrary lib;

  late final DartInitDartApi initDartApi;
  late final DartInitEngine initEngine;
  late final DartRegisterDartPort registerDartPort;
  late final DartCloseEngine closeEngine;
  late final DartSubmitTaskBytes submitTaskBytes;
  late final DartSubmitTaskRunes submitTaskRunes;
  late final DartTaskControl pauseTask;
  late final DartTaskControl resumeTask;
  late final DartTaskControl cancelTask;
  late final DartGetTaskProgress getTaskProgress;
  late final DartListDir listDir;
  late final DartFreeBuffer freeBuffer;

  // Servers
  late final DartStartServer startAPIServer;
  late final DartStopServer stopAPIServer;
  late final DartStartServerWithRoot startHTTPServer;
  late final DartStopServer stopHTTPServer;
  late final DartStartServerWithRoot startFTPServer;
  late final DartStopServer stopFTPServer;
  late final DartStartDLNAServer startDLNAServer;
  late final DartStopServer stopDLNAServer;

  // Clipboard
  late final DartClipboardCopy clipboardCopy;
  late final DartClipboardCopy clipboardCut;
  late final DartClipboardPaste clipboardPaste;
  late final DartClipboardClear clipboardClear;
  late final DartClipboardCount clipboardCount;

  // Dedup & Search
  late final DartDedupScan dedupScan;
  late final DartSearch search;

  // Archive
  late final DartArchiveCompress archiveCompress;
  late final DartArchiveExtract archiveExtract;
  late final DartArchivePreview archivePreview;

  // Media
  late final DartGenerateThumbnail generateThumbnail;
  late final DartExtractMetadata extractMetadata;

  // Signer
  late final DartSignFile signFile;
  late final DartVerifyFileSignature verifyFileSignature;

  FreeboxBindings(this.lib) {
    initDartApi = lib.lookupFunction<NativeInitDartApi, DartInitDartApi>('Freebox_InitDartApi');
    initEngine = lib.lookupFunction<NativeInitEngine, DartInitEngine>('Freebox_InitEngine');
    registerDartPort = lib.lookupFunction<NativeRegisterDartPort, DartRegisterDartPort>('Freebox_RegisterDartPort');
    closeEngine = lib.lookupFunction<NativeCloseEngine, DartCloseEngine>('Freebox_CloseEngine');
    submitTaskBytes = lib.lookupFunction<NativeSubmitTaskBytes, DartSubmitTaskBytes>('Freebox_SubmitTaskBytes');
    submitTaskRunes = lib.lookupFunction<NativeSubmitTaskRunes, DartSubmitTaskRunes>('Freebox_SubmitTaskRunes');
    pauseTask = lib.lookupFunction<NativeTaskControl, DartTaskControl>('Freebox_PauseTask');
    resumeTask = lib.lookupFunction<NativeTaskControl, DartTaskControl>('Freebox_ResumeTask');
    cancelTask = lib.lookupFunction<NativeTaskControl, DartTaskControl>('Freebox_CancelTask');
    getTaskProgress = lib.lookupFunction<NativeGetTaskProgress, DartGetTaskProgress>('Freebox_GetTaskProgress');
    listDir = lib.lookupFunction<NativeListDir, DartListDir>('Freebox_ListDir');
    freeBuffer = lib.lookupFunction<NativeFreeBuffer, DartFreeBuffer>('Freebox_FreeBuffer');

    // Servers
    startAPIServer = lib.lookupFunction<NativeStartServer, DartStartServer>('Freebox_StartAPIServer');
    stopAPIServer = lib.lookupFunction<NativeStopServer, DartStopServer>('Freebox_StopAPIServer');
    startHTTPServer = lib.lookupFunction<NativeStartServerWithRoot, DartStartServerWithRoot>('Freebox_StartHTTPServer');
    stopHTTPServer = lib.lookupFunction<NativeStopServer, DartStopServer>('Freebox_StopHTTPServer');
    startFTPServer = lib.lookupFunction<NativeStartServerWithRoot, DartStartServerWithRoot>('Freebox_StartFTPServer');
    stopFTPServer = lib.lookupFunction<NativeStopServer, DartStopServer>('Freebox_StopFTPServer');
    startDLNAServer = lib.lookupFunction<NativeStartDLNAServer, DartStartDLNAServer>('Freebox_StartDLNAServer');
    stopDLNAServer = lib.lookupFunction<NativeStopServer, DartStopServer>('Freebox_StopDLNAServer');

    // Clipboard
    clipboardCopy = lib.lookupFunction<NativeClipboardCopy, DartClipboardCopy>('Freebox_ClipboardCopy');
    clipboardCut = lib.lookupFunction<NativeClipboardCopy, DartClipboardCopy>('Freebox_ClipboardCut');
    clipboardPaste = lib.lookupFunction<NativeClipboardPaste, DartClipboardPaste>('Freebox_ClipboardPaste');
    clipboardClear = lib.lookupFunction<NativeClipboardClear, DartClipboardClear>('Freebox_ClipboardClear');
    clipboardCount = lib.lookupFunction<NativeClipboardCount, DartClipboardCount>('Freebox_ClipboardCount');

    // Dedup & Search
    dedupScan = lib.lookupFunction<NativeDedupScan, DartDedupScan>('Freebox_DedupScan');
    search = lib.lookupFunction<NativeSearch, DartSearch>('Freebox_Search');

    // Archive
    archiveCompress = lib.lookupFunction<NativeArchiveCompress, DartArchiveCompress>('Freebox_ArchiveCompress');
    archiveExtract = lib.lookupFunction<NativeArchiveExtract, DartArchiveExtract>('Freebox_ArchiveExtract');
    archivePreview = lib.lookupFunction<NativeArchivePreview, DartArchivePreview>('Freebox_ArchivePreview');

    // Media
    generateThumbnail = lib.lookupFunction<NativeGenerateThumbnail, DartGenerateThumbnail>('Freebox_GenerateThumbnail');
    extractMetadata = lib.lookupFunction<NativeExtractMetadata, DartExtractMetadata>('Freebox_ExtractMetadata');

    // Signer
    signFile = lib.lookupFunction<NativeSignFile, DartSignFile>('Freebox_SignFile');
    verifyFileSignature = lib.lookupFunction<NativeVerifyFileSignature, DartVerifyFileSignature>('Freebox_VerifyFileSignature');
  }
}
