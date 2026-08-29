// Package ipc implements a JSON-RPC 2.0 control plane and a fast raw-binary
// file transfer channel exposed over a Unix domain socket (with a TCP
// loopback fallback), replacing the old cgo/FFI bridge.
package ipc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrameSize bounds a single control-plane JSON frame to guard against
// misbehaving clients exhausting memory.
const MaxFrameSize = 64 * 1024 * 1024 // 64MB

// Connection magic bytes identifying how a freshly accepted socket should be treated.
var (
	MagicControl  = [4]byte{'F', 'B', 'X', '1'} // persistent JSON-RPC connection
	MagicTransfer = [4]byte{'F', 'B', 'X', 'T'} // one-shot raw transfer connection
)

// TransferTokenSize is the length in bytes of a transfer handshake token.
const TransferTokenSize = 16

// Request is a JSON-RPC 2.0 request/notification envelope.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Response is a JSON-RPC 2.0 response/notification envelope.
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Method  string    `json:"method,omitempty"` // set for server-initiated notifications
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// WriteFrame writes a length-prefixed JSON payload to w.
func WriteFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("ipc: frame too large (%d bytes)", len(payload))
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// ReadFrame reads a length-prefixed JSON payload from r into v.
func ReadFrame(r *bufio.Reader, v any) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header)
	if n > MaxFrameSize {
		return fmt.Errorf("ipc: incoming frame too large (%d bytes)", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(payload, v)
}

// ReadFrameBytes reads a length-prefixed raw payload without unmarshaling.
func ReadFrameBytes(r *bufio.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header)
	if n > MaxFrameSize {
		return nil, fmt.Errorf("ipc: incoming frame too large (%d bytes)", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

var errShortMagic = errors.New("ipc: short read on connection magic")

// ReadMagic reads the 4-byte connection-kind magic from r.
func ReadMagic(r io.Reader) ([4]byte, error) {
	var magic [4]byte
	n, err := io.ReadFull(r, magic[:])
	if err != nil {
		return magic, err
	}
	if n != 4 {
		return magic, errShortMagic
	}
	return magic, nil
}
