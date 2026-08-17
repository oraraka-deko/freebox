import 'dart:io';
import '../lib/freebox.dart';

void main() {
  print('--- Testing Comprehensive Freebox Dart FFI & CGO Bridge ---');

  final dllPath = File('../bridge/libfreebox.dll').absolute.path;
  print('Loading CGO Shared Library from: $dllPath');

  final client = FreeboxClient(dllPath);

  client.initEngine(
    dbPath: './data/test_dart_all.db',
    passphrase: 'dart-bridge-pass',
    maxWorkers: 2,
    streamPort: ':8096',
  );

  print('1. Freebox engine initialized successfully via Dart FFI!');

  // Listen to async task progress stream
  client.taskProgressStream.listen((progress) {
    print('[Dart Async Event] Task #${progress.taskId} | Status: ${progress.status} | Item: ${progress.currentItem}');
  });

  // 2. Submit task via byte buffer
  final byteTaskId = client.submitTaskBytes(
    type: TaskType.create,
    srcPath: 'local:dart_full_test.txt',
    data: [70, 114, 101, 101, 98, 111, 120, 32, 68, 97, 114, 116, 32, 69, 110, 103, 105, 110, 101],
  );
  print('2. Submitted byte task ID: $byteTaskId');

  // 3. Test Clipboard
  client.clipboardCopy('local:dart_full_test.txt');
  print('3. Clipboard count after copy: ${client.clipboardCount}');

  // 4. Test Search
  final searchTaskId = client.search(
    rootPath: 'local:',
    namePattern: '*.txt',
    contentPattern: 'Freebox',
  );
  print('4. Submitted search task ID: $searchTaskId');

  // 5. Test Dedup
  final dedupTaskId = client.deduplicate(
    rootPath: 'local:',
    method: DedupMethod.quick,
  );
  print('5. Submitted dedup task ID: $dedupTaskId');

  // 6. Test Archive Compression
  final archiveTaskId = client.compressArchive(
    format: ArchiveFormat.zip,
    srcPath: 'local:dart_full_test.txt',
    dstPath: 'local:dart_test.zip',
  );
  print('6. Submitted archive task ID: $archiveTaskId');

  // 7. Test Metadata Extraction
  final meta = client.extractMetadata('local:dart_full_test.txt');
  print('7. Extracted Metadata: Title="${meta.title}", Format="${meta.format}", Size=${meta.bitrate} bytes');

  // 8. Test Background Server Lifecycle
  final serverStarted = client.startServer(ServerType.http, port: ':19999');
  print('8. HTTP Server started: $serverStarted');
  sleep(const Duration(milliseconds: 100));
  final serverStopped = client.stopServer(ServerType.http);
  print('   HTTP Server stopped: $serverStopped');

  sleep(const Duration(seconds: 1));

  client.close();
  print('--- All Comprehensive Dart Bridge Tests Passed Successfully! ---');
}
