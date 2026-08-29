package ipc

import (
	"encoding/json"
	"net/http"

	"freebox/proxy"
)

func init() {
	register("streams.register", handleStreamsRegister)
	register("streams.unregister", handleStreamsUnregister)
}

type streamsRegisterParams struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func handleStreamsRegister(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p streamsRegisterParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	streamServer := inst.Engine.StreamServer()
	if streamServer == nil {
		return nil, errStreamServerNotInitialized
	}
	src, err := proxy.NewHTTPSource(p.URL, http.DefaultClient)
	if err != nil {
		return nil, err
	}
	streamServer.RegisterSource(p.Name, src)
	return map[string]bool{"ok": true}, nil
}

type streamsUnregisterParams struct {
	Name string `json:"name"`
}

func handleStreamsUnregister(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p streamsUnregisterParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if streamServer := inst.Engine.StreamServer(); streamServer != nil {
		streamServer.UnregisterSource(p.Name)
	}
	return map[string]bool{"ok": true}, nil
}
