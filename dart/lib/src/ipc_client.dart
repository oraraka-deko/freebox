/// Control-plane client for the Freebox IPC socket: connects, performs the
/// "FBX1" control handshake, and speaks length-prefixed JSON-RPC 2.0.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

/// Magic bytes identifying a persistent control connection (must match
/// ipc.MagicControl in the Go server).
const List<int> _magicControl = [0x46, 0x42, 0x58, 0x31]; // "FBX1"

/// A server-initiated notification (no request id), e.g. task.progress.
class IpcNotification {
  final String method;
  final dynamic params;
  IpcNotification(this.method, this.params);
}

class IpcException implements Exception {
  final String message;
  IpcException(this.message);
  @override
  String toString() => 'IpcException: $message';
}

/// Connects to the Freebox IPC control socket and exposes a JSON-RPC 2.0
/// [call] method plus a [notifications] stream for server-pushed events.
class IpcClient {
  final Socket _socket;
  final StreamController<IpcNotification> _notifications = StreamController.broadcast();
  final Map<int, Completer<dynamic>> _pending = {};
  int _nextId = 1;
  final BytesBuilder _recvBuffer = BytesBuilder(copy: false);
  StreamSubscription<Uint8List>? _sub;

  IpcClient._(this._socket) {
    _socket.add(_magicControl);
    _sub = _socket.listen(_onData, onError: (Object e) => _failAll(e), onDone: () => _failAll(IpcException('connection closed')));
  }

  /// Connects to a Unix domain socket path.
  static Future<IpcClient> connectUnix(String socketPath) async {
    final socket = await Socket.connect(InternetAddress(socketPath, type: InternetAddressType.unix), 0);
    return IpcClient._(socket);
  }

  /// Connects to a TCP loopback address (host, port) -- used as a fallback
  /// where Unix domain sockets aren't available.
  static Future<IpcClient> connectTcp(String host, int port) async {
    final socket = await Socket.connect(host, port);
    return IpcClient._(socket);
  }

  /// The path/address this client connects through, exposed for opening a
  /// second raw connection for fast file transfers.
  Socket get rawSocketForTransfer => _socket;

  Stream<IpcNotification> get notifications => _notifications.stream;

  /// Invokes [method] with [params] and awaits the JSON-RPC result.
  Future<dynamic> call(String method, [Map<String, dynamic>? params]) {
    final id = _nextId++;
    final completer = Completer<dynamic>();
    _pending[id] = completer;

    final payload = utf8.encode(jsonEncode({
      'jsonrpc': '2.0',
      'id': id,
      'method': method,
      if (params != null) 'params': params,
    }));
    _writeFrame(payload);
    return completer.future;
  }

  void _writeFrame(List<int> payload) {
    final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
    _socket.add(header.buffer.asUint8List());
    _socket.add(payload);
  }

  void _onData(Uint8List chunk) {
    _recvBuffer.add(chunk);
    _drainFrames();
  }

  void _drainFrames() {
    while (true) {
      final bytes = _recvBuffer.toBytes();
      if (bytes.length < 4) return;
      final len = ByteData.sublistView(bytes, 0, 4).getUint32(0, Endian.big);
      if (bytes.length < 4 + len) return;

      final payload = bytes.sublist(4, 4 + len);
      final rest = bytes.sublist(4 + len);
      _recvBuffer.clear();
      if (rest.isNotEmpty) _recvBuffer.add(rest);

      _handleFrame(jsonDecode(utf8.decode(payload)) as Map<String, dynamic>);
    }
  }

  void _handleFrame(Map<String, dynamic> msg) {
    final id = msg['id'];
    final method = msg['method'] as String?;

    if (id == null && method != null) {
      _notifications.add(IpcNotification(method, msg['result']));
      return;
    }

    final intId = id is int ? id : int.tryParse('$id');
    final completer = intId != null ? _pending.remove(intId) : null;
    if (completer == null) return;

    final error = msg['error'];
    if (error != null) {
      completer.completeError(IpcException((error as Map)['message'] as String? ?? 'unknown error'));
    } else {
      completer.complete(msg['result']);
    }
  }

  void _failAll(Object err) {
    for (final c in _pending.values) {
      if (!c.isCompleted) c.completeError(err is Exception ? err : IpcException('$err'));
    }
    _pending.clear();
  }

  Future<void> close() async {
    await _sub?.cancel();
    await _socket.close();
    await _notifications.close();
  }
}
