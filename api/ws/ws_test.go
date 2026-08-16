package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketHub_PubSub(t *testing.T) {
	actionChan := make(chan Event, 1)

	hub := NewHub(func(client *Client, event Event) error {
		actionChan <- event
		return nil
	})
	defer hub.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeHTTP(w, r, "testuser")
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect client
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 1. Subscribe to topic "tasks"
	subMsg := Event{Type: "subscribe", Topic: "tasks"}
	if err := conn.WriteJSON(subMsg); err != nil {
		t.Fatalf("write sub failed: %v", err)
	}

	// Read confirmation
	var resp Event
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read sub resp failed: %v", err)
	}
	if resp.Type != "subscribed" || resp.Topic != "tasks" {
		t.Fatalf("unexpected resp: %+v", resp)
	}

	// 2. Broadcast event to topic "tasks"
	hub.Broadcast("tasks", "task_progress", map[string]interface{}{"task_id": "123", "percent": 50.0})

	var progressEvent Event
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&progressEvent); err != nil {
		t.Fatalf("read progress event failed: %v", err)
	}
	if progressEvent.Type != "task_progress" {
		t.Fatalf("expected task_progress, got %s", progressEvent.Type)
	}

	// 3. Send action from client
	actionMsg := Event{Type: "action", Topic: "tasks", Data: map[string]string{"action": "pause", "task_id": "123"}}
	if err := conn.WriteJSON(actionMsg); err != nil {
		t.Fatalf("write action failed: %v", err)
	}

	select {
	case act := <-actionChan:
		if act.Type != "action" {
			t.Fatalf("expected action event, got %+v", act)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for action callback")
	}
}
