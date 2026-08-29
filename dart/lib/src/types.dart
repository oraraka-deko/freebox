/// Plain-Dart types mirroring the Go IPC server's JSON payloads.
/// No FFI/native structs -- these are simple value types deserialized
/// straight from JSON.
library;

class TaskType {
  static const create = 'CREATE';
  static const delete = 'DELETE';
  static const copy = 'COPY';
  static const move = 'MOVE';
  static const open = 'OPEN';
  static const serve = 'SERVE';
  static const proxy = 'PROXY';
  static const custom = 'CUSTOM';
}

class TaskStatus {
  static const pending = 'PENDING';
  static const running = 'RUNNING';
  static const paused = 'PAUSED';
  static const completed = 'COMPLETED';
  static const failed = 'FAILED';
  static const canceled = 'CANCELED';
}

class ArchiveFormat {
  static const zip = 'zip';
  static const tar = 'tar';
  static const tarGz = 'tar.gz';
  static const tarBz2 = 'tar.bz2';
  static const rar = 'rar';
  static const sevenZ = '7z';
}

class DedupMethod {
  static const meta = 'meta';
  static const quickHash = 'quick_hash';
  static const md5 = 'md5';
  static const sha256 = 'sha256';
  static const sha1 = 'sha1';
  static const crc32 = 'crc32';
}

class DedupAction {
  static const report = 'report';
  static const delete = 'delete';
}

class DedupKeepPolicy {
  static const oldest = 'oldest';
  static const newest = 'newest';
  static const shortestPath = 'shortest_path';
  static const first = 'first';
}

class SearchMatchType {
  static const substring = 'substring';
  static const exact = 'exact';
  static const regex = 'regex';
  static const glob = 'glob';
  static const prefix = 'prefix';
  static const suffix = 'suffix';
}

class SearchTarget {
  static const all = 'all';
  static const names = 'names';
  static const content = 'content';
}

/// A file/directory entry returned by vfs.listDir.
class FileInfo {
  final String name;
  final int size;
  final int modTime; // unix nanoseconds
  final bool isDir;

  FileInfo({required this.name, required this.size, required this.modTime, required this.isDir});

  factory FileInfo.fromJson(Map<String, dynamic> json) => FileInfo(
        name: json['name'] as String,
        size: (json['size'] as num).toInt(),
        modTime: (json['modTime'] as num).toInt(),
        isDir: json['isDir'] as bool,
      );
}

/// Progress/status pushed via "task.progress" / "task.status" notifications.
class TaskProgress {
  final String taskId;
  final String status;
  final int bytesProcessed;
  final int totalBytes;
  final double percent;
  final double speedBytesSec;
  final int durationMs;
  final String currentItem;
  final String error;

  TaskProgress({
    required this.taskId,
    required this.status,
    required this.bytesProcessed,
    required this.totalBytes,
    required this.percent,
    required this.speedBytesSec,
    required this.durationMs,
    required this.currentItem,
    required this.error,
  });

  factory TaskProgress.fromJson(Map<String, dynamic> json) => TaskProgress(
        taskId: json['taskId'] as String? ?? '',
        status: json['status'] as String? ?? '',
        bytesProcessed: (json['bytesProcessed'] as num?)?.toInt() ?? 0,
        totalBytes: (json['totalBytes'] as num?)?.toInt() ?? 0,
        percent: (json['percent'] as num?)?.toDouble() ?? 0,
        speedBytesSec: (json['speedBytesSec'] as num?)?.toDouble() ?? 0,
        durationMs: (json['durationMs'] as num?)?.toInt() ?? 0,
        currentItem: json['currentItem'] as String? ?? '',
        error: json['error'] as String? ?? '',
      );
}
