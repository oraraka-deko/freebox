/// High-level Freebox client: connects to the freeboxd IPC socket and
/// exposes one method per RPC category, delegating everything to JSON-RPC
/// calls over [IpcClient] instead of FFI/cgo marshaling.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'src/ipc_client.dart';
import 'src/transfer_client.dart';
import 'src/types.dart';

export 'src/types.dart';
export 'src/ipc_client.dart' show IpcNotification, IpcException;
export 'src/transfer_client.dart' show TransferClient;

class FreeboxClient {
  final IpcClient _ipc;
  final TransferClient _transfers;

  FreeboxClient._(this._ipc, this._transfers);

  /// Connects to a freeboxd Unix domain socket, e.g. "/run/freebox/freebox.sock".
  static Future<FreeboxClient> connectUnix(String socketPath) async {
    final ipc = await IpcClient.connectUnix(socketPath);
    return FreeboxClient._(ipc, TransferClient.unix(socketPath));
  }

  /// Connects to a freeboxd TCP loopback fallback, e.g. 127.0.0.1:9191.
  static Future<FreeboxClient> connectTcp(String host, int port) async {
    final ipc = await IpcClient.connectTcp(host, port);
    return FreeboxClient._(ipc, TransferClient.tcp(host, port));
  }

  Future<void> close() => _ipc.close();

  /// Stream of task.progress / task.status / transfer.* notifications.
  Stream<IpcNotification> get notifications => _ipc.notifications;

  /// Stream of just task.progress and task.status, decoded as [TaskProgress].
  Stream<TaskProgress> get taskProgressStream => _ipc.notifications
      .where((n) => n.method == 'task.progress' || n.method == 'task.status')
      .map((n) => TaskProgress.fromJson(n.params as Map<String, dynamic>));

  // ---- VFS ----

  Future<void> mount(String name, String type, String path, {bool readOnly = false}) async {
    await _ipc.call('vfs.mount', {'name': name, 'type': type, 'path': path, 'readOnly': readOnly});
  }

  Future<void> unmount(String name) async {
    await _ipc.call('vfs.unmount', {'name': name});
  }

  Future<List<FileInfo>> listDir(String path) async {
    final result = await _ipc.call('vfs.listDir', {'path': path}) as List<dynamic>;
    return result.map((e) => FileInfo.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Reads a whole small file inline. For large files, prefer [transferRead].
  Future<Uint8List> readFile(String path) async {
    final result = await _ipc.call('vfs.readFile', {'path': path}) as Map<String, dynamic>;
    return base64Decode(result['data'] as String);
  }

  /// Writes a whole small file inline. For large files, prefer [transferWrite].
  Future<void> writeFile(String path, List<int> data) async {
    await _ipc.call('vfs.writeFile', {'path': path, 'data': base64Encode(data)});
  }

  // ---- Fast file transfer ----

  /// Opens a fast read transfer and returns a byte stream for [path].
  Future<Stream<List<int>>> transferRead(String path, {int offset = 0}) async {
    final result = await _ipc.call('transfer.open', {'mode': 'read', 'path': path, 'offset': offset}) as Map<String, dynamic>;
    return _transfers.read(result['token'] as String);
  }

  /// Opens a fast write transfer and streams [data] into [path].
  Future<void> transferWrite(String path, Stream<List<int>> data) async {
    final result = await _ipc.call('transfer.open', {'mode': 'write', 'path': path}) as Map<String, dynamic>;
    await _transfers.write(result['token'] as String, data);
  }

  Future<bool> transferClose(String transferId) async {
    final result = await _ipc.call('transfer.close', {'transferId': transferId}) as Map<String, dynamic>;
    return result['closed'] as bool;
  }

  // ---- Tasks ----

  Future<String> submitTask({
    required String type,
    required String src,
    String dst = '',
    List<int>? data,
    int priority = 0,
  }) async {
    final result = await _ipc.call('tasks.submit', {
      'type': type,
      'src': src,
      'dst': dst,
      if (data != null) 'data': base64Encode(data),
      'priority': priority,
    }) as Map<String, dynamic>;
    return result['taskId'] as String;
  }

  Future<void> pauseTask(String taskId) async => _ipc.call('tasks.pause', {'taskId': taskId});
  Future<void> resumeTask(String taskId) async => _ipc.call('tasks.resume', {'taskId': taskId});
  Future<void> cancelTask(String taskId) async => _ipc.call('tasks.cancel', {'taskId': taskId});

  Future<TaskProgress> getTaskProgress(String taskId) async {
    final result = await _ipc.call('tasks.getProgress', {'taskId': taskId}) as Map<String, dynamic>;
    return TaskProgress.fromJson(result);
  }

  Future<Map<String, dynamic>> getTaskRecord(String taskId) async {
    return await _ipc.call('tasks.getRecord', {'taskId': taskId}) as Map<String, dynamic>;
  }

  // ---- Clipboard ----

  Future<int> clipboardCopy(String path) async {
    final result = await _ipc.call('clipboard.copy', {'path': path}) as Map<String, dynamic>;
    return (result['count'] as num).toInt();
  }

  Future<int> clipboardCut(String path) async {
    final result = await _ipc.call('clipboard.cut', {'path': path}) as Map<String, dynamic>;
    return (result['count'] as num).toInt();
  }

  Future<List<String>> clipboardPaste(String dst) async {
    final result = await _ipc.call('clipboard.paste', {'dst': dst}) as Map<String, dynamic>;
    return (result['taskIds'] as List<dynamic>).cast<String>();
  }

  Future<void> clipboardClear() async => _ipc.call('clipboard.clear');

  Future<int> get clipboardCount async {
    final result = await _ipc.call('clipboard.count') as Map<String, dynamic>;
    return (result['count'] as num).toInt();
  }

  // ---- Search / Dedup ----

  Future<String> search({
    required String rootPath,
    String namePattern = '',
    String contentPattern = '',
    String replacement = '',
    String matchType = SearchMatchType.substring,
    String target = SearchTarget.all,
    bool isReplace = false,
    bool caseSensitive = false,
  }) async {
    final result = await _ipc.call('search.run', {
      'rootPath': rootPath,
      'namePattern': namePattern,
      'contentPattern': contentPattern,
      'replacement': replacement,
      'matchType': matchType,
      'target': target,
      'isReplace': isReplace,
      'caseSensitive': caseSensitive,
    }) as Map<String, dynamic>;
    return result['taskId'] as String;
  }

  Future<String> deduplicate({
    required String rootPath,
    String method = DedupMethod.quickHash,
    String action = DedupAction.report,
    String keepPolicy = DedupKeepPolicy.oldest,
    int minSize = 1,
    int maxWorkers = 4,
  }) async {
    final result = await _ipc.call('dedup.scan', {
      'rootPath': rootPath,
      'method': method,
      'action': action,
      'keepPolicy': keepPolicy,
      'minSize': minSize,
      'maxWorkers': maxWorkers,
    }) as Map<String, dynamic>;
    return result['taskId'] as String;
  }

  // ---- Archive ----

  Future<String> compressArchive({
    required String format,
    required String src,
    required String dst,
    String password = '',
  }) async {
    final result = await _ipc.call('archive.compress', {
      'format': format,
      'src': src,
      'dst': dst,
      'password': password,
    }) as Map<String, dynamic>;
    return result['taskId'] as String;
  }

  Future<String> extractArchive({required String src, required String dst, String password = ''}) async {
    final result = await _ipc.call('archive.extract', {'src': src, 'dst': dst, 'password': password}) as Map<String, dynamic>;
    return result['taskId'] as String;
  }

  Future<List<dynamic>> previewArchive(String path, {String password = ''}) async {
    return await _ipc.call('archive.preview', {'path': path, 'password': password}) as List<dynamic>;
  }

  // ---- Media ----

  Future<Uint8List> generateThumbnail(String path, {int width = 256, int height = 256, int quality = 80}) async {
    final result = await _ipc.call('media.generateThumbnail', {
      'path': path,
      'width': width,
      'height': height,
      'quality': quality,
    }) as Map<String, dynamic>;
    return base64Decode(result['data'] as String);
  }

  Future<Map<String, dynamic>> extractMetadata(String path) async {
    return await _ipc.call('media.extractMetadata', {'path': path}) as Map<String, dynamic>;
  }

  // ---- Streams ----

  Future<void> registerStream(String name, String url) async => _ipc.call('streams.register', {'name': name, 'url': url});
  Future<void> unregisterStream(String name) async => _ipc.call('streams.unregister', {'name': name});

  // ---- Crypto ----

  Future<String> signFile(String path, String privKeyPem) async {
    final result = await _ipc.call('crypto.signFile', {'path': path, 'privKeyPem': privKeyPem}) as Map<String, dynamic>;
    return result['signatureHex'] as String;
  }

  Future<bool> verifyFileSignature(String path, String sigHex, String pubKeyPem) async {
    final result = await _ipc
        .call('crypto.verifyFileSignature', {'path': path, 'sigHex': sigHex, 'pubKeyPem': pubKeyPem}) as Map<String, dynamic>;
    return result['valid'] as bool;
  }

  // ---- Google Drive ----

  Future<Map<String, dynamic>> gdriveAuthorize({
    required String clientId,
    required String clientSecret,
    List<int>? credentialsJson,
    int port = 0,
    String callbackPath = '',
  }) async {
    return await _ipc.call('gdrive.authorize', {
      'clientId': clientId,
      'clientSecret': clientSecret,
      if (credentialsJson != null) 'credentialsJson': base64Encode(credentialsJson),
      'port': port,
      'callbackPath': callbackPath,
    }) as Map<String, dynamic>;
  }

  Future<void> gdriveMount({
    required String mountName,
    required String clientId,
    required String clientSecret,
    required List<int> tokenJson,
    String rootFolderId = '',
  }) async {
    await _ipc.call('gdrive.mount', {
      'mountName': mountName,
      'clientId': clientId,
      'clientSecret': clientSecret,
      'tokenJson': base64Encode(tokenJson),
      'rootFolderId': rootFolderId,
    });
  }

  // ---- Telegram ----

  Future<List<dynamic>> telegramListSessions() async => await _ipc.call('telegram.listSessions') as List<dynamic>;

  Future<void> telegramRemoveSession(int accountId) async => _ipc.call('telegram.removeSession', {'accountId': accountId});

  // ---- Servers ----

  Future<void> startAPIServer({String port = ':8080'}) async => _ipc.call('servers.startAPI', {'port': port});
  Future<void> stopAPIServer() async => _ipc.call('servers.stopAPI');
  Future<void> startHTTPServer({String port = ':8081', String rootDir = ''}) async =>
      _ipc.call('servers.startHTTP', {'port': port, 'rootDir': rootDir});
  Future<void> stopHTTPServer() async => _ipc.call('servers.stopHTTP');
  Future<void> startFTPServer({String port = ':2121', String rootDir = ''}) async =>
      _ipc.call('servers.startFTP', {'port': port, 'rootDir': rootDir});
  Future<void> stopFTPServer() async => _ipc.call('servers.stopFTP');
  Future<void> startDLNAServer({String friendlyName = '', String port = ':8200', String rootDir = ''}) async =>
      _ipc.call('servers.startDLNA', {'friendlyName': friendlyName, 'port': port, 'rootDir': rootDir});
  Future<void> stopDLNAServer() async => _ipc.call('servers.stopDLNA');
}
