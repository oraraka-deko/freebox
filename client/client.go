package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"freebox/api"
	"freebox/api/ws"
	"freebox/auth"
	"freebox/vfs"
	"github.com/gorilla/websocket"
)

// Client is the Go SDK client for remote Freebox servers.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	wsConn     *websocket.Conn
	wsChan     chan ws.Event
	mu         sync.RWMutex
	closed     bool
}

// NewClient creates a new Freebox remote API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		wsChan: make(chan ws.Event, 128),
	}
}

// SetToken manually sets the session API key.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// Token returns the current session token.
func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// Login authenticates with username & password and stores the session API key.
func (c *Client) Login(username, password string) (*auth.Session, error) {
	reqBody, _ := json.Marshal(api.LoginRequest{
		Username: username,
		Password: password,
	})

	resp, err := c.httpClient.Post(c.baseURL+"/api/auth/login", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("login failed (status %d): %v", resp.StatusCode, errResp["error"])
	}

	var session auth.Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}

	c.SetToken(session.Token)
	return &session, nil
}

// Logout revokes the session token.
func (c *Client) Logout() error {
	req, err := c.newRequest(http.MethodPost, "/api/auth/logout", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	c.SetToken("")
	return nil
}

func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	fullURL := c.baseURL + path
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func (c *Client) doJSON(method, path string, bodyData interface{}, result interface{}) error {
	var body io.Reader
	if bodyData != nil {
		data, err := json.Marshal(bodyData)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := c.newRequest(method, path, body)
	if err != nil {
		return err
	}
	if bodyData != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errMap map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errMap)
		return fmt.Errorf("API error (status %d): %v", resp.StatusCode, errMap["error"])
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// --- Mounts API ---

// ListMounts returns all mounts on the remote server.
func (c *Client) ListMounts() ([]vfs.MountInfo, error) {
	var mounts []vfs.MountInfo
	err := c.doJSON(http.MethodGet, "/api/mounts", nil, &mounts)
	return mounts, err
}

// Mount registers a new storage mount point.
func (c *Client) Mount(name string, cfg vfs.MountConfig, persist bool) error {
	req := api.MountRequest{
		Name:    name,
		Config:  cfg,
		Persist: persist,
	}
	return c.doJSON(http.MethodPost, "/api/mounts", req, nil)
}

// Unmount removes a mount point.
func (c *Client) Unmount(name string) error {
	return c.doJSON(http.MethodDelete, "/api/mounts/"+url.PathEscape(name), nil, nil)
}

// --- VFS File Operations ---

// Stat gets metadata for a path in a mount.
func (c *Client) Stat(mount, path string) (*vfs.FileInfo, error) {
	var fi vfs.FileInfo
	p := fmt.Sprintf("/api/vfs/%s/stat?path=%s", url.PathEscape(mount), url.QueryEscape(path))
	err := c.doJSON(http.MethodGet, p, nil, &fi)
	return &fi, err
}

// ReadDir lists directory contents in a mount.
func (c *Client) ReadDir(mount, path string) ([]*vfs.FileInfo, error) {
	var entries []*vfs.FileInfo
	p := fmt.Sprintf("/api/vfs/%s/readdir?path=%s", url.PathEscape(mount), url.QueryEscape(path))
	err := c.doJSON(http.MethodGet, p, nil, &entries)
	return entries, err
}

// Download opens a read stream for a remote file.
func (c *Client) Download(mount, path string) (io.ReadCloser, error) {
	p := fmt.Sprintf("/api/vfs/%s/download?path=%s", url.PathEscape(mount), url.QueryEscape(path))
	req, err := c.newRequest(http.MethodGet, p, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("download error status: %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Upload streams data to a remote file path.
func (c *Client) Upload(mount, path string, reader io.Reader) error {
	p := fmt.Sprintf("/api/vfs/%s/upload?path=%s", url.PathEscape(mount), url.QueryEscape(path))
	req, err := c.newRequest(http.MethodPost, p, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("upload error status: %d", resp.StatusCode)
	}
	return nil
}

// Mkdir creates a directory in a mount.
func (c *Client) Mkdir(mount, path string) error {
	p := fmt.Sprintf("/api/vfs/%s/mkdir?path=%s", url.PathEscape(mount), url.QueryEscape(path))
	return c.doJSON(http.MethodPost, p, nil, nil)
}

// Delete removes a file or directory in a mount.
func (c *Client) Delete(mount, path string) error {
	p := fmt.Sprintf("/api/vfs/%s/delete?path=%s", url.PathEscape(mount), url.QueryEscape(path))
	return c.doJSON(http.MethodDelete, p, nil, nil)
}

// Transfer initiates a cross-mount file transfer task on the remote server.
func (c *Client) Transfer(srcMount, srcPath, dstMount, dstPath string, move bool) (*api.TaskResponse, error) {
	req := api.VFSTransferRequest{
		SrcMount: srcMount,
		SrcPath:  srcPath,
		DstMount: dstMount,
		DstPath:  dstPath,
		Move:     move,
	}
	var task api.TaskResponse
	err := c.doJSON(http.MethodPost, "/api/vfs/transfer", req, &task)
	return &task, err
}

// --- Tasks API ---

// ListTasks returns all active and historical tasks.
func (c *Client) ListTasks() ([]api.TaskResponse, error) {
	var tasks []api.TaskResponse
	err := c.doJSON(http.MethodGet, "/api/tasks", nil, &tasks)
	return tasks, err
}

// GetTask returns details of a single task.
func (c *Client) GetTask(taskID string) (*api.TaskResponse, error) {
	var task api.TaskResponse
	err := c.doJSON(http.MethodGet, "/api/tasks/"+url.PathEscape(taskID), nil, &task)
	return &task, err
}

// PauseTask pauses a running task.
func (c *Client) PauseTask(taskID string) error {
	return c.doJSON(http.MethodPost, "/api/tasks/"+url.PathEscape(taskID)+"/pause", nil, nil)
}

// ResumeTask resumes a paused task.
func (c *Client) ResumeTask(taskID string) error {
	return c.doJSON(http.MethodPost, "/api/tasks/"+url.PathEscape(taskID)+"/resume", nil, nil)
}

// CancelTask cancels a task.
func (c *Client) CancelTask(taskID string) error {
	return c.doJSON(http.MethodPost, "/api/tasks/"+url.PathEscape(taskID)+"/cancel", nil, nil)
}

// --- Stream Proxy API ---

// RegisterStream registers a media stream on the server.
func (c *Client) RegisterStream(name, mount, path, streamURL string) error {
	req := api.RegisterStreamRequest{
		Name:      name,
		MountName: mount,
		Path:      path,
		URL:       streamURL,
	}
	return c.doJSON(http.MethodPost, "/api/streams", req, nil)
}

// UnregisterStream removes a stream proxy source.
func (c *Client) UnregisterStream(name string) error {
	return c.doJSON(http.MethodDelete, "/api/streams/"+url.PathEscape(name), nil, nil)
}

// StreamURL returns the direct HTTP URL to play/stream the named media.
func (c *Client) StreamURL(name string) string {
	return fmt.Sprintf("%s/stream/%s", c.baseURL, url.PathEscape(name))
}

// --- WebSocket Real-Time Channel ---

// ConnectWebSocket connects to the remote server WebSocket hub and starts reading events.
func (c *Client) ConnectWebSocket() (<-chan ws.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wsConn != nil {
		return c.wsChan, nil
	}

	wsBase := strings.Replace(c.baseURL, "http://", "ws://", 1)
	wsBase = strings.Replace(wsBase, "https://", "wss://", 1)
	wsURL := fmt.Sprintf("%s/api/ws?token=%s", wsBase, url.QueryEscape(c.token))

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	c.wsConn = conn

	go func() {
		defer func() {
			c.mu.Lock()
			_ = conn.Close()
			c.wsConn = nil
			c.mu.Unlock()
		}()

		for {
			var event ws.Event
			if err := conn.ReadJSON(&event); err != nil {
				break
			}
			select {
			case c.wsChan <- event:
			default:
			}
		}
	}()

	return c.wsChan, nil
}

// SubscribeWS sends a subscribe request for a topic over the WebSocket connection.
func (c *Client) SubscribeWS(topic string) error {
	c.mu.RLock()
	conn := c.wsConn
	c.mu.RUnlock()

	if conn == nil {
		return errors.New("websocket not connected")
	}

	return conn.WriteJSON(ws.Event{
		Type:  "subscribe",
		Topic: topic,
	})
}

// SendWSAction sends a command action over WebSocket.
func (c *Client) SendWSAction(action, taskID string) error {
	c.mu.RLock()
	conn := c.wsConn
	c.mu.RUnlock()

	if conn == nil {
		return errors.New("websocket not connected")
	}

	return conn.WriteJSON(ws.Event{
		Type: "action",
		Data: map[string]string{
			"action":  action,
			"task_id": taskID,
		},
	})
}

// Close closes WebSocket and releases client resources.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.wsConn != nil {
		_ = c.wsConn.Close()
	}
}

var (
	_ = context.Background
)
