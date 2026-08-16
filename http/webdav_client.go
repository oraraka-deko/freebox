package http

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebDAVResource represents a file or collection found via WebDAV.
type WebDAVResource struct {
	Href         string
	DisplayName  string
	IsDir        bool
	Size         int64
	LastModified string
}

// WebDAVClient represents a client for WebDAV servers.
type WebDAVClient struct {
	baseURL    string
	user       string
	pass       string
	httpClient *http.Client
}

// NewWebDAVClient creates a new WebDAV client instance.
func NewWebDAVClient(baseURL, user, pass string, timeout time.Duration) *WebDAVClient {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &WebDAVClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		pass:    pass,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *WebDAVClient) buildURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.baseURL + path
}

func (c *WebDAVClient) setAuth(req *http.Request) {
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
}

// Propfind queries metadata of resources at the given WebDAV path.
func (c *WebDAVClient) Propfind(ctx context.Context, path string, depth string) ([]WebDAVResource, error) {
	if depth == "" {
		depth = "1"
	}
	fullURL := c.buildURL(path)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", fullURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	req.Header.Set("Depth", depth)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("propfind failed with status %d", resp.StatusCode)
	}

	var ms propfindResponse
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("failed to decode propfind xml: %w", err)
	}

	var results []WebDAVResource
	for _, item := range ms.Response {
		res := WebDAVResource{
			Href:         item.Href,
			DisplayName:  item.Propstat.Prop.DisplayName,
			Size:         item.Propstat.Prop.GetContentLength,
			LastModified: item.Propstat.Prop.GetLastModified,
			IsDir:        item.Propstat.Prop.ResourceType != nil && item.Propstat.Prop.ResourceType.Collection != nil,
		}
		results = append(results, res)
	}
	return results, nil
}

// Get downloads a file from the given WebDAV path.
func (c *WebDAVClient) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.buildURL(path), nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("webdav get failed: %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Put uploads content to the given WebDAV path.
func (c *WebDAVClient) Put(ctx context.Context, path string, data io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.buildURL(path), data)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("webdav put failed: %d", resp.StatusCode)
	}
	return nil
}

// Mkcol creates a directory/collection at the given path.
func (c *WebDAVClient) Mkcol(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, "MKCOL", c.buildURL(path), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdav mkcol failed: %d", resp.StatusCode)
	}
	return nil
}

// Delete removes a file or directory at the given path.
func (c *WebDAVClient) Delete(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.buildURL(path), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("webdav delete failed: %d", resp.StatusCode)
	}
	return nil
}

// Move renames or moves a resource from src to dst.
func (c *WebDAVClient) Move(ctx context.Context, src, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "MOVE", c.buildURL(src), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	req.Header.Set("Destination", dst)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdav move failed: %d", resp.StatusCode)
	}
	return nil
}

// Copy copies a resource from src to dst.
func (c *WebDAVClient) Copy(ctx context.Context, src, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "COPY", c.buildURL(src), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	req.Header.Set("Destination", dst)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdav copy failed: %d", resp.StatusCode)
	}
	return nil
}

// Options queries the WebDAV server capabilities.
func (c *WebDAVClient) Options(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "OPTIONS", c.buildURL(path), nil)
	if err != nil {
		return "", err
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	return resp.Header.Get("DAV"), nil
}

func parseURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
