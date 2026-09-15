package ipc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type aria2MockReq struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type aria2MockResp struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      string `json:"id"`
	Result  any    `json:"result,omitempty"`
}

func setupMockAria2Server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req aria2MockReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var result any
		switch req.Method {
		case "aria2.getVersion":
			result = map[string]any{
				"version":         "1.36.0",
				"enabledFeatures": []string{"Async DNS", "BitTorrent", "GZip", "HTTPS"},
			}
		case "aria2.addUri":
			result = "mock-gid-12345"
		case "aria2.tellStatus":
			result = map[string]any{
				"gid":             "mock-gid-12345",
				"status":          "active",
				"totalLength":     "1048576",
				"completedLength": "524288",
				"downloadSpeed":   "102400",
				"files":           []any{},
			}
		case "aria2.tellActive":
			result = []any{
				map[string]any{
					"gid":    "mock-gid-12345",
					"status": "active",
				},
			}
		case "aria2.pause":
			result = "mock-gid-12345"
		case "aria2.unpause":
			result = "mock-gid-12345"
		case "aria2.remove":
			result = "mock-gid-12345"
		case "aria2.getGlobalStat":
			result = map[string]any{
				"downloadSpeed": "102400",
				"uploadSpeed":   "20480",
				"numActive":     "1",
				"numWaiting":    "0",
				"numStopped":    "0",
			}
		default:
			result = "OK"
		}

		resp := aria2MockResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result:  result,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return server
}

func TestIPCAria2Handlers(t *testing.T) {
	aria2Server := setupMockAria2Server(t)
	defer aria2Server.Close()

	_, socketPath := newTestServer(t)
	c := newTestClient(t, socketPath)
	defer c.nc.Close()

	// 1. aria2.connect
	connRes := c.call("aria2.connect", map[string]any{
		"url": aria2Server.URL,
	})
	if ok, _ := connRes["ok"].(bool); !ok {
		t.Fatalf("aria2.connect failed: %v", connRes)
	}
	if ver, _ := connRes["version"].(string); ver != "1.36.0" {
		t.Fatalf("expected version 1.36.0, got %s", ver)
	}

	// 2. aria2.addUri
	addRes := c.call("aria2.addUri", map[string]any{
		"uris": []string{"https://example.com/file.iso"},
	})
	gid, _ := addRes["gid"].(string)
	if gid != "mock-gid-12345" {
		t.Fatalf("expected gid mock-gid-12345, got %s", gid)
	}

	// 3. aria2.tellStatus
	statusRes := c.call("aria2.tellStatus", map[string]any{
		"gid": "mock-gid-12345",
	})
	if statusRes["status"] != "active" {
		t.Fatalf("expected status active, got %v", statusRes["status"])
	}

	// 4. aria2.tellActive
	activeRaw := c.callRaw("aria2.tellActive", map[string]any{})
	activeList, ok := activeRaw.([]any)
	if !ok || len(activeList) == 0 {
		t.Fatalf("expected non-empty active list, got %v", activeRaw)
	}

	// 5. aria2.pause
	pauseRes := c.call("aria2.pause", map[string]any{
		"gid": "mock-gid-12345",
	})
	if pauseRes["gid"] != "mock-gid-12345" {
		t.Fatalf("expected paused gid mock-gid-12345, got %v", pauseRes["gid"])
	}

	// 6. aria2.unpause
	unpauseRes := c.call("aria2.unpause", map[string]any{
		"gid": "mock-gid-12345",
	})
	if unpauseRes["gid"] != "mock-gid-12345" {
		t.Fatalf("expected unpaused gid mock-gid-12345, got %v", unpauseRes["gid"])
	}

	// 7. aria2.getGlobalStat
	statRes := c.call("aria2.getGlobalStat", map[string]any{})
	if statRes["downloadSpeed"] != "102400" {
		t.Fatalf("expected downloadSpeed 102400, got %v", statRes["downloadSpeed"])
	}

	// 8. aria2.remove
	removeRes := c.call("aria2.remove", map[string]any{
		"gid": "mock-gid-12345",
	})
	if removeRes["gid"] != "mock-gid-12345" {
		t.Fatalf("expected removed gid mock-gid-12345, got %v", removeRes["gid"])
	}
}
