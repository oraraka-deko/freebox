package ipc

import "encoding/json"

func init() {
	register("transfer.open", handleTransferOpen)
	register("transfer.close", handleTransferClose)
}

type transferOpenParams struct {
	Mode   string `json:"mode"` // "read" or "write"
	Path   string `json:"path"` // "mount:subpath"
	Offset int64  `json:"offset,omitempty"`
}

// handleTransferOpen issues a one-time token; the caller must then dial the
// same socket/port again, send magic "FBXT" + the 16-byte token (hex-decoded),
// and stream raw bytes for the requested mode.
func handleTransferOpen(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p transferOpenParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}

	transferID, token, err := inst.Transfers.open(conn, p.Mode, fs, subPath, p.Offset)
	if err != nil {
		return nil, err
	}
	return map[string]string{"transferId": transferID, "token": token}, nil
}

type transferCloseParams struct {
	TransferID string `json:"transferId"`
}

func handleTransferClose(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p transferCloseParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ok := inst.Transfers.close(p.TransferID)
	return map[string]bool{"closed": ok}, nil
}
