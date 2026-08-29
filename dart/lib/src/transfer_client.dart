/// Fast binary file-transfer client: opens a second raw connection to the
/// same IPC endpoint, performs the "FBXT" + token handshake, then streams
/// raw bytes with no JSON framing overhead.
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

const List<int> _magicTransfer = [0x46, 0x42, 0x58, 0x54]; // "FBXT"

List<int> _hexDecode(String hex) {
  final out = Uint8List(hex.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = int.parse(hex.substring(i * 2, i * 2 + 2), radix: 16);
  }
  return out;
}

/// Opens fast transfer connections given a transfer token returned by the
/// `transfer.open` RPC call.
class TransferClient {
  final Future<Socket> Function() _dial;
  TransferClient._(this._dial);

  /// Creates a client that dials the same Unix domain socket used for control.
  factory TransferClient.unix(String socketPath) =>
      TransferClient._(() => Socket.connect(InternetAddress(socketPath, type: InternetAddressType.unix), 0));

  /// Creates a client that dials the same TCP loopback address used for control.
  factory TransferClient.tcp(String host, int port) => TransferClient._(() => Socket.connect(host, port));

  Future<Socket> _handshake(String token) async {
    final socket = await _dial();
    socket.add(_magicTransfer);
    socket.add(_hexDecode(token));
    return socket;
  }

  /// Reads the full contents of a "read" mode transfer as a byte stream.
  /// The connection is closed automatically once the server signals EOF.
  Stream<List<int>> read(String token) async* {
    final socket = await _handshake(token);
    try {
      await for (final chunk in socket) {
        yield chunk;
      }
    } finally {
      await socket.close();
    }
  }

  /// Streams [data] to a "write" mode transfer, then closes the connection
  /// to signal EOF to the server.
  Future<void> write(String token, Stream<List<int>> data) async {
    final socket = await _handshake(token);
    try {
      await socket.addStream(data);
      await socket.flush();
    } finally {
      await socket.close();
    }
  }

  /// Convenience for writing a single in-memory buffer.
  Future<void> writeBytes(String token, List<int> data) => write(token, Stream.value(data));
}
