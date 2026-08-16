package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client represents a configurable HTTP client.
type Client struct {
	httpClient *http.Client
	headers    http.Header
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithTimeout sets a custom request timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// WithHeader adds a default header to all requests.
func WithHeader(key, value string) ClientOption {
	return func(c *Client) {
		c.headers.Set(key, value)
	}
}

// NewClient creates a new HTTP client.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		headers: make(http.Header),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Do executes an HTTP request with client headers.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	for k, vals := range c.headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	return c.httpClient.Do(req)
}

// Get executes a GET request.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post executes a POST request with the given content type and body.
func (c *Client) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// PostJSON executes a POST request marshaling the data struct to JSON.
func (c *Client) PostJSON(ctx context.Context, url string, data any) (*http.Response, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal json: %w", err)
	}
	return c.Post(ctx, url, "application/json", bytes.NewReader(payload))
}

// GetJSON executes a GET request and unmarshals the JSON response into v.
func (c *Client) GetJSON(ctx context.Context, url string, v any) error {
	resp, err := c.Get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http error: status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(v)
}
