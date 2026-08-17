import 'dart:async';
import 'dart:convert';
import 'dart:ffi' as ffi;
import 'dart:isolate';
import 'dart:typed_data';
import 'types.dart';
import 'bindings.dart';

class AsyncDartBridge {
  final FreeboxBindings bindings;
  late final ReceivePort _receivePort;
  late final StreamController<TaskProgress> _progressController;

  AsyncDartBridge(this.bindings) {
    _progressController = StreamController<TaskProgress>.broadcast();
    _initBridge();
  }

  void _initBridge() {
    final initResult = bindings.initDartApi(ffi.NativeApi.initializeApiDLData);
    if (initResult != 0) {
      throw StateError('Failed to initialize Dart API DL in native bridge (code $initResult)');
    }

    _receivePort = ReceivePort();
    _receivePort.listen(_handleNativeMessage);
  }

  int get nativePort => _receivePort.sendPort.nativePort;
  Stream<TaskProgress> get progressStream => _progressController.stream;

  void _handleNativeMessage(dynamic message) {
    if (message is! List || message.length < 9) {
      return;
    }

    final taskId = message[0] as int;
    final statusCode = message[1] as int;
    final bytesProcessed = message[2] as int;
    final totalBytes = message[3] as int;
    final percent = (message[4] as num).toDouble();
    final speedBytesSec = (message[5] as num).toDouble();
    final durationMs = message[6] as int;

    String currentItem = '';
    if (message[7] is Uint8List) {
      final bytes = message[7] as Uint8List;
      if (bytes.isNotEmpty) {
        currentItem = utf8.decode(bytes);
      }
    }

    String errorMsg = '';
    if (message[8] is Uint8List) {
      final bytes = message[8] as Uint8List;
      if (bytes.isNotEmpty) {
        errorMsg = utf8.decode(bytes);
      }
    }

    final progress = TaskProgress(
      taskId: taskId,
      bytesProcessed: bytesProcessed,
      totalBytes: totalBytes,
      percent: percent,
      speedBytesSec: speedBytesSec,
      durationMs: durationMs,
      status: TaskStatus.fromCode(statusCode),
      currentItem: currentItem,
      errorMsg: errorMsg,
    );

    _progressController.add(progress);
  }

  void dispose() {
    _receivePort.close();
    _progressController.close();
  }
}
