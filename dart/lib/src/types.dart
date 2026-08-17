import 'dart:ffi' as ffi;
import 'dart:typed_data';
import 'dart:convert';
import 'package:ffi/ffi.dart';

/// FreeboxByteBuffer matches C struct FreeboxByteBuffer
final class FreeboxByteBuffer extends ffi.Struct {
  external ffi.Pointer<ffi.Uint8> ptr;

  @ffi.Int32()
  external int len;
}

/// FreeboxRuneBuffer matches C struct FreeboxRuneBuffer
final class FreeboxRuneBuffer extends ffi.Struct {
  external ffi.Pointer<ffi.Uint32> ptr;

  @ffi.Int32()
  external int len;
}

/// Task progress struct matching C FreeboxTaskProgressC
final class FreeboxTaskProgressC extends ffi.Struct {
  @ffi.Uint64()
  external int taskId;

  @ffi.Int64()
  external int bytesProcessed;

  @ffi.Int64()
  external int totalBytes;

  @ffi.Double()
  external double percent;

  @ffi.Double()
  external double speedBytesSec;

  @ffi.Int64()
  external int durationMs;

  @ffi.Uint8()
  external int status;

  external FreeboxByteBuffer currentItem;
  external FreeboxByteBuffer errorMsg;
}

/// Task record struct matching C FreeboxTaskRecordC
final class FreeboxTaskRecordC extends ffi.Struct {
  @ffi.Uint64()
  external int taskId;

  @ffi.Uint8()
  external int taskType;

  @ffi.Uint8()
  external int status;

  @ffi.Int32()
  external int priority;

  @ffi.Int64()
  external int bytesProcessed;

  @ffi.Int64()
  external int totalBytes;

  @ffi.Double()
  external double percent;

  @ffi.Int64()
  external int startTimeUnix;

  @ffi.Int64()
  external int endTimeUnix;

  @ffi.Int64()
  external int durationMs;

  external FreeboxByteBuffer srcPath;
  external FreeboxByteBuffer dstPath;
  external FreeboxByteBuffer currentItem;
  external FreeboxByteBuffer errorMsg;
}

/// File info struct matching C FreeboxFileInfoC
final class FreeboxFileInfoC extends ffi.Struct {
  external FreeboxByteBuffer name;

  @ffi.Int64()
  external int size;

  @ffi.Int64()
  external int modTimeUnix;

  @ffi.Uint8()
  external int isDir;

  @ffi.Uint32()
  external int mode;
}

/// Media meta struct matching C FreeboxMediaMetaC
final class FreeboxMediaMetaC extends ffi.Struct {
  external FreeboxByteBuffer title;
  external FreeboxByteBuffer artist;
  external FreeboxByteBuffer album;
  external FreeboxByteBuffer format;

  @ffi.Int32()
  external int width;

  @ffi.Int32()
  external int height;

  @ffi.Int64()
  external int durationMs;

  @ffi.Int64()
  external int bitrate;
}

/// Generic C result struct matching C FreeboxResultC
final class FreeboxResultC extends ffi.Struct {
  @ffi.Uint8()
  external int success;

  external FreeboxByteBuffer errorMsg;

  @ffi.Uint64()
  external int handle;

  @ffi.Int64()
  external int value;
}

enum TaskType {
  create(1),
  delete(2),
  copy(3),
  move(4),
  open(5),
  serve(6),
  proxy(7),
  custom(8);

  final int code;
  const TaskType(this.code);

  static TaskType fromCode(int code) {
    return TaskType.values.firstWhere(
      (e) => e.code == code,
      orElse: () => TaskType.custom,
    );
  }
}

enum TaskStatus {
  pending(0),
  running(1),
  paused(2),
  completed(3),
  failed(4),
  canceled(5);

  final int code;
  const TaskStatus(this.code);

  static TaskStatus fromCode(int code) {
    return TaskStatus.values.firstWhere(
      (e) => e.code == code,
      orElse: () => TaskStatus.pending,
    );
  }
}

enum ServerType {
  api(1),
  http(2),
  webdav(3),
  ftp(4),
  smb(5),
  sftp(6),
  dlna(7);

  final int code;
  const ServerType(this.code);
}

enum ArchiveFormat {
  zip(1),
  tar(2),
  tgz(3),
  tbz2(4);

  final int code;
  const ArchiveFormat(this.code);
}

enum DedupMethod {
  meta(1),
  quick(2),
  md5(3),
  sha256(4),
  sha1(5),
  crc32(6);

  final int code;
  const DedupMethod(this.code);
}

enum DedupAction {
  report(1),
  delete(2);

  final int code;
  const DedupAction(this.code);
}

enum DedupKeepPolicy {
  oldest(1),
  newest(2),
  shortest(3),
  first(4);

  final int code;
  const DedupKeepPolicy(this.code);
}

enum SearchMatchType {
  substring(1),
  exact(2),
  regex(3),
  glob(4),
  prefix(5),
  suffix(6);

  final int code;
  const SearchMatchType(this.code);
}

enum SearchTarget {
  all(1),
  names(2),
  content(3);

  final int code;
  const SearchTarget(this.code);
}

/// High-level Dart representations
class TaskProgress {
  final int taskId;
  final int bytesProcessed;
  final int totalBytes;
  final double percent;
  final double speedBytesSec;
  final int durationMs;
  final TaskStatus status;
  final String currentItem;
  final String errorMsg;

  TaskProgress({
    required this.taskId,
    required this.bytesProcessed,
    required this.totalBytes,
    required this.percent,
    required this.speedBytesSec,
    required this.durationMs,
    required this.status,
    required this.currentItem,
    required this.errorMsg,
  });
}

class FileInfo {
  final String name;
  final int size;
  final DateTime modTime;
  final bool isDir;
  final int mode;

  FileInfo({
    required this.name,
    required this.size,
    required this.modTime,
    required this.isDir,
    required this.mode,
  });
}

class MediaMeta {
  final String title;
  final String format;
  final int width;
  final int height;
  final int durationMs;
  final int bitrate;

  MediaMeta({
    required this.title,
    required this.format,
    required this.width,
    required this.height,
    required this.durationMs,
    required this.bitrate,
  });
}

/// Extension helpers to pass UTF-8 bytes or UTF-32 runes without string allocations.
extension StringBridgeExtension on String {
  /// Converts string to UTF-8 native byte buffer pointer
  ffi.Pointer<ffi.Uint8> toNativeBytes(Arena arena, [Object? _]) {
    if (isEmpty) {
      final ptr = arena<ffi.Uint8>(1);
      ptr[0] = 0;
      return ptr;
    }
    final bytes = utf8.encode(this);
    final ptr = arena<ffi.Uint8>(bytes.length + 1);
    final typedList = ptr.asTypedList(bytes.length);
    typedList.setAll(0, bytes);
    ptr[bytes.length] = 0;
    return ptr;
  }

  /// Converts string to UTF-32 native rune pointer
  ffi.Pointer<ffi.Uint32> toNativeRunes(Arena arena) {
    final runesList = runes.toList();
    if (runesList.isEmpty) {
      final ptr = arena<ffi.Uint32>(1);
      ptr[0] = 0;
      return ptr;
    }
    final ptr = arena<ffi.Uint32>(runesList.length);
    final typedList = ptr.asTypedList(runesList.length);
    typedList.setAll(0, runesList);
    return ptr;
  }
}

extension FreeboxBufferExtension on FreeboxByteBuffer {
  String toDartString() {
    if (ptr == ffi.nullptr || len <= 0) return '';
    return utf8.decode(ptr.asTypedList(len));
  }

  Uint8List toUint8List() {
    if (ptr == ffi.nullptr || len <= 0) return Uint8List(0);
    return Uint8List.fromList(ptr.asTypedList(len));
  }
}
