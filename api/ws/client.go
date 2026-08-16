package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024
)

// Client represents a connected WebSocket client session.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	sendChan chan []byte
	Username string
	closed   bool
	mu       sync.Mutex
}

// NewClient creates a new WebSocket client.
func NewClient(hub *Hub, conn *websocket.Conn, username string) *Client {
	return &Client{
		hub:      hub,
		conn:     conn,
		sendChan: make(chan []byte, 128),
		Username: username,
	}
}

// Send sends an event to the client.
func (c *Client) Send(event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.sendChan <- data:
	default:
	}
}

// Close closes connection and channel.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.conn.Close()
	close(c.sendChan)
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		var event Event
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}

		switch event.Type {
		case "ping":
			c.Send(Event{Type: "pong", Timestamp: time.Now()})
		case "subscribe":
			if event.Topic != "" {
				c.hub.Subscribe(c, event.Topic)
				c.Send(Event{Type: "subscribed", Topic: event.Topic, Timestamp: time.Now()})
			}
		case "unsubscribe":
			if event.Topic != "" {
				c.hub.Unsubscribe(c, event.Topic)
				c.Send(Event{Type: "unsubscribed", Topic: event.Topic, Timestamp: time.Now()})
			}
		case "action":
			if c.hub.actionFn != nil {
				_ = c.hub.actionFn(c, event)
			}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case message, ok := <-c.sendChan:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(message)

			// Drain any queued messages
			n := len(c.sendChan)
			for i := 0; i < n; i++ {
				_, _ = w.Write([]byte{'\n'})
				_, _ = w.Write(<-c.sendChan)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
