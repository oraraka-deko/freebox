package ws

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for remote API client connections
	},
}

// Event represents a message exchanged over WebSocket.
type Event struct {
	Type      string      `json:"type"`
	Topic     string      `json:"topic,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// ActionHandler is called when a client sends an action over WS.
type ActionHandler func(client *Client, event Event) error

// Hub manages active WebSocket clients and topic subscriptions.
type Hub struct {
	clients     map[*Client]bool
	topics      map[string]map[*Client]bool
	register    chan *Client
	unregister  chan *Client
	broadcast   chan Event
	actionFn    ActionHandler
	mu          sync.RWMutex
	stopChan    chan struct{}
}

// NewHub creates a new WebSocket hub.
func NewHub(actionHandler ActionHandler) *Hub {
	h := &Hub{
		clients:    make(map[*Client]bool),
		topics:     make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Event, 256),
		actionFn:   actionHandler,
		stopChan:   make(chan struct{}),
	}
	go h.run()
	return h
}

// Close stops the hub and disconnects clients.
func (h *Hub) Close() {
	close(h.stopChan)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.Close()
	}
}

func (h *Hub) run() {
	for {
		select {
		case <-h.stopChan:
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				for topic := range h.topics {
					delete(h.topics[topic], client)
				}
				client.Close()
			}
			h.mu.Unlock()
		case event := <-h.broadcast:
			h.mu.RLock()
			if event.Topic == "" {
				// Broadcast to all clients
				for client := range h.clients {
					client.Send(event)
				}
			} else if subs, ok := h.topics[event.Topic]; ok {
				for client := range subs {
					client.Send(event)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Broadcast sends an event to all subscribers of topic (or all if topic is empty).
func (h *Hub) Broadcast(topic, eventType string, data interface{}) {
	event := Event{
		Type:      eventType,
		Topic:     topic,
		Data:      data,
		Timestamp: time.Now(),
	}
	select {
	case h.broadcast <- event:
	default:
	}
}

// Subscribe adds client to a specific topic.
func (h *Hub) Subscribe(client *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.topics[topic] == nil {
		h.topics[topic] = make(map[*Client]bool)
	}
	h.topics[topic][client] = true
}

// Unsubscribe removes client from topic.
func (h *Hub) Unsubscribe(client *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.topics[topic]; ok {
		delete(subs, client)
	}
}

// ServeHTTP upgrades HTTP connection to WebSocket and registers client.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request, username string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := NewClient(h, conn, username)
	h.register <- client

	go client.writePump()
	go client.readPump()
}
