library freebox;

import 'dart:convert';
import 'dart:ffi' as ffi;
import 'dart:typed_data';
import 'package:ffi/ffi.dart';
import 'src/bindings.dart';
import 'src/types.dart';
import 'src/async_bridge.dart';

export 'src/types.dart'
    show
        TaskType,
        TaskStatus,
        TaskProgress,
        FileInfo,
        MediaMeta,
        ServerType,
        ArchiveFormat,
        DedupMethod,
        DedupAction,
        DedupKeepPolicy,
        SearchMatchType,
        SearchTarget;

class FreeboxClient {
  late final FreeboxBindings _bindings;
  late final AsyncDartBridge _asyncBridge;
  int _engineHandle = 0;

  FreeboxClient(String dynamicLibraryPath) {
    final lib = ffi.DynamicLibrary.open(dynamicLibraryPath);
    _bindings = FreeboxBindings(lib);
    _asyncBridge = AsyncDartBridge(_bindings);
  }

  /// Initializes the Freebox Go engine with DB path and passphrase.
  void initEngine({
    String dbPath = './data/freebox.db',
    String passphrase = 'freebox-secret-passphrase',
    int maxWorkers = 4,
    String streamPort = ':8090',
  }) {
    using((Arena arena) {
      final dbPathPtr = dbPath.toNativeBytes(arena);
      final passPtr = passphrase.toNativeBytes(arena);
      final streamPortPtr = streamPort.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      final handle = _bindings.initEngine(
        dbPathPtr,
        dbPath.length,
        passPtr,
        passphrase.length,
        maxWorkers,
        streamPortPtr,
        streamPort.length,
        resultPtr,
      );

      if (handle == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed initializing Freebox engine: $err');
      }

      _engineHandle = handle;
      _bindings.registerDartPort(_engineHandle, _asyncBridge.nativePort);
    });
  }

  /// Real-time stream of task progress updates sent from Go worker queue
  Stream<TaskProgress> get taskProgressStream => _asyncBridge.progressStream;

  /// Submits a task using byte buffer parameters (zero C-string allocation)
  int submitTaskBytes({
    required TaskType type,
    required String srcPath,
    String dstPath = '',
    List<int>? data,
    int priority = 0,
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');

    return using((Arena arena) {
      final srcPtr = srcPath.toNativeBytes(arena);
      final dstPtr = dstPath.toNativeBytes(arena);

      ffi.Pointer<ffi.Uint8> dataPtr = ffi.nullptr;
      int dataLen = 0;
      if (data != null && data.isNotEmpty) {
        dataPtr = arena<ffi.Uint8>(data.length);
        dataPtr.asTypedList(data.length).setAll(0, data);
        dataLen = data.length;
      }

      final resultPtr = arena<FreeboxResultC>();
      final taskId = _bindings.submitTaskBytes(
        _engineHandle,
        type.code,
        srcPtr,
        srcPath.length,
        dstPtr,
        dstPath.length,
        dataPtr,
        dataLen,
        priority,
        _asyncBridge.nativePort,
        resultPtr,
      );

      if (taskId == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed submitting task: $err');
      }

      return taskId;
    });
  }

  /// Submits a task using UTF-32 rune array parameters
  int submitTaskRunes({
    required TaskType type,
    required String srcPath,
    String dstPath = '',
    int priority = 0,
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');

    return using((Arena arena) {
      final srcRunes = srcPath.runes.toList();
      final dstRunes = dstPath.runes.toList();

      final srcRunesPtr = srcPath.toNativeRunes(arena);
      final dstRunesPtr = dstPath.toNativeRunes(arena);

      final resultPtr = arena<FreeboxResultC>();
      final taskId = _bindings.submitTaskRunes(
        _engineHandle,
        type.code,
        srcRunesPtr,
        srcRunes.length,
        dstRunesPtr,
        dstRunes.length,
        priority,
        _asyncBridge.nativePort,
        resultPtr,
      );

      if (taskId == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed submitting task with runes: $err');
      }

      return taskId;
    });
  }

  /// Clipboard Operations
  bool clipboardCopy(String path) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final pPtr = path.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();
      return _bindings.clipboardCopy(_engineHandle, pPtr, path.length, resultPtr) != 0;
    });
  }

  bool clipboardCut(String path) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final pPtr = path.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();
      return _bindings.clipboardCut(_engineHandle, pPtr, path.length, resultPtr) != 0;
    });
  }

  int clipboardPaste(String dstDir) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final dPtr = dstDir.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();
      final tID = _bindings.clipboardPaste(_engineHandle, dPtr, dstDir.length, _asyncBridge.nativePort, resultPtr);
      if (tID == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed pasting clipboard: $err');
      }
      return tID;
    });
  }

  void clipboardClear() {
    if (_engineHandle != 0) {
      _bindings.clipboardClear(_engineHandle);
    }
  }

  int get clipboardCount {
    if (_engineHandle == 0) return 0;
    return _bindings.clipboardCount(_engineHandle);
  }

  /// Deduplication Scan
  int deduplicate({
    required String rootPath,
    DedupMethod method = DedupMethod.quick,
    DedupAction action = DedupAction.report,
    DedupKeepPolicy keepPolicy = DedupKeepPolicy.oldest,
    int minSize = 1,
    int maxWorkers = 4,
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final rPtr = rootPath.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();
      final tID = _bindings.dedupScan(
        _engineHandle,
        rPtr,
        rootPath.length,
        method.code,
        action.code,
        keepPolicy.code,
        minSize,
        maxWorkers,
        _asyncBridge.nativePort,
        resultPtr,
      );
      if (tID == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed starting dedup scan: $err');
      }
      return tID;
    });
  }

  /// Search & Replace
  int search({
    required String rootPath,
    String namePattern = '',
    String contentPattern = '',
    String replacement = '',
    SearchMatchType matchType = SearchMatchType.substring,
    SearchTarget target = SearchTarget.all,
    bool isReplace = false,
    bool caseSensitive = false,
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final rPtr = rootPath.toNativeBytes(arena);
      final nPtr = namePattern.toNativeBytes(arena);
      final cPtr = contentPattern.toNativeBytes(arena);
      final repPtr = replacement.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      final tID = _bindings.search(
        _engineHandle,
        rPtr,
        rootPath.length,
        nPtr,
        namePattern.length,
        cPtr,
        contentPattern.length,
        repPtr,
        replacement.length,
        matchType.code,
        target.code,
        isReplace ? 1 : 0,
        caseSensitive ? 1 : 0,
        _asyncBridge.nativePort,
        resultPtr,
      );

      if (tID == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed starting search/replace: $err');
      }
      return tID;
    });
  }

  /// Archive Operations
  int compressArchive({
    required ArchiveFormat format,
    required String srcPath,
    required String dstPath,
    String password = '',
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final sPtr = srcPath.toNativeBytes(arena);
      final dPtr = dstPath.toNativeBytes(arena);
      final pPtr = password.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      final tID = _bindings.archiveCompress(
        _engineHandle,
        format.code,
        sPtr,
        srcPath.length,
        dPtr,
        dstPath.length,
        pPtr,
        password.length,
        _asyncBridge.nativePort,
        resultPtr,
      );

      if (tID == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed compressing archive: $err');
      }
      return tID;
    });
  }

  int extractArchive({
    required String srcArchive,
    required String dstDir,
    String password = '',
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final sPtr = srcArchive.toNativeBytes(arena);
      final dPtr = dstDir.toNativeBytes(arena);
      final pPtr = password.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      final tID = _bindings.archiveExtract(
        _engineHandle,
        sPtr,
        srcArchive.length,
        dPtr,
        dstDir.length,
        pPtr,
        password.length,
        _asyncBridge.nativePort,
        resultPtr,
      );

      if (tID == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed extracting archive: $err');
      }
      return tID;
    });
  }

  int previewArchive({
    required String archivePath,
    String password = '',
  }) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final aPtr = archivePath.toNativeBytes(arena);
      final pPtr = password.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      final count = _bindings.archivePreview(_engineHandle, aPtr, archivePath.length, pPtr, password.length, resultPtr);
      return count;
    });
  }

  /// Thumbnail Generation
  Uint8List generateThumbnail(String srcPath, {int width = 256, int height = 256, int quality = 80}) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final sPtr = srcPath.toNativeBytes(arena);
      final outDataPtr = arena<ffi.Pointer<ffi.Uint8>>();
      final outLenPtr = arena<ffi.Int32>();
      final resultPtr = arena<FreeboxResultC>();

      final ok = _bindings.generateThumbnail(
        _engineHandle,
        sPtr,
        srcPath.length,
        width,
        height,
        quality,
        outDataPtr,
        outLenPtr,
        resultPtr,
      );

      if (ok == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed generating thumbnail: $err');
      }

      final len = outLenPtr.value;
      final ptr = outDataPtr.value;
      if (ptr == ffi.nullptr || len <= 0) return Uint8List(0);

      final resultBytes = Uint8List.fromList(ptr.asTypedList(len));
      _bindings.freeBuffer(ptr.cast<ffi.Void>());
      return resultBytes;
    });
  }

  /// Metadata Extraction
  MediaMeta extractMetadata(String srcPath) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final sPtr = srcPath.toNativeBytes(arena);
      final metaPtr = arena<FreeboxMediaMetaC>();
      final resultPtr = arena<FreeboxResultC>();

      final ok = _bindings.extractMetadata(_engineHandle, sPtr, srcPath.length, metaPtr, resultPtr);
      if (ok == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed extracting metadata: $err');
      }

      return MediaMeta(
        title: metaPtr.ref.title.toDartString(),
        format: metaPtr.ref.format.toDartString(),
        width: metaPtr.ref.width,
        height: metaPtr.ref.height,
        durationMs: metaPtr.ref.durationMs,
        bitrate: metaPtr.ref.bitrate,
      );
    });
  }

  /// Signer
  String signFile(String filePath, String privKeyPEM) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final fPtr = filePath.toNativeBytes(arena);
      final kPtr = privKeyPEM.toNativeBytes(arena);
      final outSigPtr = arena<ffi.Pointer<ffi.Uint8>>();
      final outLenPtr = arena<ffi.Int32>();
      final resultPtr = arena<FreeboxResultC>();

      final ok = _bindings.signFile(
        _engineHandle,
        fPtr,
        filePath.length,
        kPtr,
        privKeyPEM.length,
        outSigPtr,
        outLenPtr,
        resultPtr,
      );

      if (ok == 0 || resultPtr.ref.success == 0) {
        final err = resultPtr.ref.errorMsg.toDartString();
        throw StateError('Failed signing file: $err');
      }

      final len = outLenPtr.value;
      final ptr = outSigPtr.value;
      if (ptr == ffi.nullptr || len <= 0) return '';
      final sig = utf8.decode(ptr.asTypedList(len));
      _bindings.freeBuffer(ptr.cast<ffi.Void>());
      return sig;
    });
  }

  bool verifyFileSignature(String filePath, String sigHex, String pubKeyPEM) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final fPtr = filePath.toNativeBytes(arena);
      final sPtr = sigHex.toNativeBytes(arena);
      final kPtr = pubKeyPEM.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      final ok = _bindings.verifyFileSignature(
        _engineHandle,
        fPtr,
        filePath.length,
        sPtr,
        sigHex.length,
        kPtr,
        pubKeyPEM.length,
        resultPtr,
      );

      return ok != 0;
    });
  }

  /// Server Management
  bool startServer(ServerType type, {String port = '', String rootDir = '', String friendlyName = ''}) {
    if (_engineHandle == 0) throw StateError('Engine not initialized');
    return using((Arena arena) {
      final pPtr = port.toNativeBytes(arena);
      final rPtr = rootDir.toNativeBytes(arena);
      final fPtr = friendlyName.toNativeBytes(arena);
      final resultPtr = arena<FreeboxResultC>();

      int ok = 0;
      switch (type) {
        case ServerType.api:
          ok = _bindings.startAPIServer(_engineHandle, pPtr, port.length, resultPtr);
          break;
        case ServerType.http:
          ok = _bindings.startHTTPServer(_engineHandle, pPtr, port.length, rPtr, rootDir.length, resultPtr);
          break;
        case ServerType.ftp:
          ok = _bindings.startFTPServer(_engineHandle, pPtr, port.length, rPtr, rootDir.length, resultPtr);
          break;
        case ServerType.dlna:
          ok = _bindings.startDLNAServer(_engineHandle, fPtr, friendlyName.length, pPtr, port.length, rPtr, rootDir.length, resultPtr);
          break;
        default:
          ok = _bindings.startHTTPServer(_engineHandle, pPtr, port.length, rPtr, rootDir.length, resultPtr);
          break;
      }
      return ok != 0;
    });
  }

  bool stopServer(ServerType type) {
    if (_engineHandle == 0) return false;
    switch (type) {
      case ServerType.api:
        return _bindings.stopAPIServer(_engineHandle) != 0;
      case ServerType.http:
        return _bindings.stopHTTPServer(_engineHandle) != 0;
      case ServerType.ftp:
        return _bindings.stopFTPServer(_engineHandle) != 0;
      case ServerType.dlna:
        return _bindings.stopDLNAServer(_engineHandle) != 0;
      default:
        return false;
    }
  }

  /// Task Control
  bool pauseTask(int taskId) {
    if (_engineHandle == 0) return false;
    return _bindings.pauseTask(_engineHandle, taskId) != 0;
  }

  bool resumeTask(int taskId) {
    if (_engineHandle == 0) return false;
    return _bindings.resumeTask(_engineHandle, taskId) != 0;
  }

  bool cancelTask(int taskId) {
    if (_engineHandle == 0) return false;
    return _bindings.cancelTask(_engineHandle, taskId) != 0;
  }

  /// Closes the Freebox engine and releases CGO resources.
  void close() {
    if (_engineHandle != 0) {
      _bindings.closeEngine(_engineHandle);
      _engineHandle = 0;
    }
    _asyncBridge.dispose();
  }
}
