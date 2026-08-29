package ipc

import (
	"encoding/json"
	"fmt"
)

// Handler processes a single RPC method call against inst, given raw JSON params.
type Handler func(inst *Instance, conn *Conn, params json.RawMessage) (any, error)

var methodRegistry = make(map[string]Handler)

// register adds a method handler. Called from init() in handlers_*.go files.
func register(name string, h Handler) {
	if _, exists := methodRegistry[name]; exists {
		panic("ipc: duplicate method registration: " + name)
	}
	methodRegistry[name] = h
}

func dispatch(inst *Instance, conn *Conn, method string, params json.RawMessage) (any, error) {
	h, ok := methodRegistry[method]
	if !ok {
		return nil, fmt.Errorf("unknown method: %s", method)
	}
	return h(inst, conn, params)
}

// decodeParams unmarshals raw JSON params into v, treating empty params as a no-op.
func decodeParams(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	return json.Unmarshal(params, v)
}
