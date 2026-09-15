package webdav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"time"

	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

type Client struct {
	BaseURL    string
	Username   string
	Password   string
	httpClient *http.Client
}

type WebdavMethod string

const (
	WebdavMethodMkcol    WebdavMethod = "MKCOL"
	WebdavMethodPropfind WebdavMethod = "PROPFIND"
	WebdavMethodPut      WebdavMethod = "PUT"
	WebdavMethodGet      WebdavMethod = "GET"
	WebdavMethodDelete   WebdavMethod = "DELETE"
	WebdavMethodMove     WebdavMethod = "MOVE"
	WebdavMethodCopy     WebdavMethod = "COPY"
)

// WebDAV XML structures for PROPFIND response
type Multistatus struct {
	XMLName   xml.Name   `xml:"multistatus"`
	Responses []Response `xml:"response"`
}

type Response struct {
	Href     string   `xml:"href"`
	Propstat Propstat `xml:"propstat"`
}

type Propstat struct {
	Prop   Prop   `xml:"prop"`
	Status string `xml:"status"`
}

type Prop struct {
	ResourceType     ResourceType `xml:"resourcetype"`
	GetContentLength int64        `xml:"getcontentlength"`
	GetLastModified  string       `xml:"getlastmodified"`
	DisplayName      string       `xml:"displayname"`
}

type ResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func (rt ResourceType) IsCollection() bool {
	return rt.Collection != nil
}

func NewClient(baseURL, username, password string, httpClient *http.Client) *Client {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		BaseURL:    baseURL,
		Username:   username,
		Password:   password,
		httpClient: httpClient,
	}
}

func (c *Client) doRequest(ctx context.Context, method WebdavMethod, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, string(method), url, body)
	if err != nil {
		return nil, err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	if method == WebdavMethodPropfind {
		req.Header.Set("Depth", "1")
	}
	if method == WebdavMethodPut && ctx != nil {
		if length := ctx.Value(ctxkey.ContentLength); length != nil {
			if l, ok := length.(int64); ok {
				req.ContentLength = l
			}
		}
	}
	return c.httpClient.Do(req)
}

func (c *Client) Exists(ctx context.Context, remotePath string) (bool, error) {
	url := c.BaseURL + remotePath
	resp, err := c.doRequest(ctx, WebdavMethodPropfind, url, nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("PROPFIND: %s", resp.Status)
}

func (c *Client) MkDir(ctx context.Context, dirPath string) error {
	dirPath = strings.Trim(dirPath, "/")
	if dirPath == "" {
		return nil
	}
	parts := strings.Split(dirPath, "/")
	var currentPath strings.Builder
	for i, part := range parts {
		if i > 0 {
			currentPath.WriteString("/")
		}
		currentPath.WriteString(part)

		exists, err := c.Exists(ctx, currentPath.String())
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		url := c.BaseURL + currentPath.String()
		resp, err := c.doRequest(ctx, WebdavMethodMkcol, url, nil)
		if err != nil {
			return err
		}
		resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("MKCOL %s: %s", currentPath.String(), resp.Status)
		}
	}
	return nil
}

func (c *Client) WriteFile(ctx context.Context, remotePath string, content io.Reader) error {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.Trim(remotePath, "/"), "/")
	u.Path = path.Join(u.Path, strings.Join(parts, "/"))
	resp, err := c.doRequest(ctx, WebdavMethodPut, u.String(), content)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("PUT: %s", resp.Status)
}

// ListDir lists files and directories in the given path
func (c *Client) ListDir(ctx context.Context, dirPath string) ([]Response, error) {
	dirPath = strings.Trim(dirPath, "/")
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, dirPath)
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}

	resp, err := c.doRequest(ctx, WebdavMethodPropfind, u.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus {
		return nil, fmt.Errorf("PROPFIND: %s", resp.Status)
	}

	var multistatus Multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&multistatus); err != nil {
		return nil, fmt.Errorf("failed to decode PROPFIND response: %w", err)
	}

	// Filter out the directory itself from results
	var results []Response
	basePath := u.Path
	for _, r := range multistatus.Responses {
		decodedHref, err := url.PathUnescape(r.Href)
		if err != nil {
			decodedHref = r.Href
		}
		// Skip the directory itself
		if strings.TrimSuffix(decodedHref, "/") == strings.TrimSuffix(basePath, "/") {
			continue
		}
		results = append(results, r)
	}

	return results, nil
}

// ReadFile downloads a file and returns a ReadCloser
func (c *Client) ReadFile(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	filePath = strings.Trim(filePath, "/")
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, 0, err
	}
	u.Path = path.Join(u.Path, filePath)

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("GET: %s", resp.Status)
	}

	return resp.Body, resp.ContentLength, nil
}

// Delete removes a file or directory at the remote path.
func (c *Client) Delete(ctx context.Context, remotePath string) error {
	remotePath = strings.Trim(remotePath, "/")
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	u.Path = path.Join(u.Path, remotePath)
	req, err := http.NewRequestWithContext(ctx, string(WebdavMethodDelete), u.String(), nil)
	if err != nil {
		return err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("DELETE: %s", resp.Status)
}

// Move renames or moves a file or directory from oldPath to newPath.
func (c *Client) Move(ctx context.Context, oldPath, newPath string) error {
	oldPath = strings.Trim(oldPath, "/")
	newPath = strings.Trim(newPath, "/")
	uOld, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	uOld.Path = path.Join(uOld.Path, oldPath)

	uNew, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	uNew.Path = path.Join(uNew.Path, newPath)

	req, err := http.NewRequestWithContext(ctx, string(WebdavMethodMove), uOld.String(), nil)
	if err != nil {
		return err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	req.Header.Set("Destination", uNew.String())
	req.Header.Set("Overwrite", "T")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("MOVE: %s", resp.Status)
}

// Copy copies a file or directory from oldPath to newPath.
func (c *Client) Copy(ctx context.Context, oldPath, newPath string) error {
	oldPath = strings.Trim(oldPath, "/")
	newPath = strings.Trim(newPath, "/")
	uOld, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	uOld.Path = path.Join(uOld.Path, oldPath)

	uNew, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	uNew.Path = path.Join(uNew.Path, newPath)

	req, err := http.NewRequestWithContext(ctx, string(WebdavMethodCopy), uOld.String(), nil)
	if err != nil {
		return err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	req.Header.Set("Destination", uNew.String())
	req.Header.Set("Overwrite", "T")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("COPY: %s", resp.Status)
}

// Stat fetches metadata for a specific file or directory.
func (c *Client) Stat(ctx context.Context, remotePath string) (*storagetypes.FileInfo, error) {
	remotePath = strings.Trim(remotePath, "/")
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	if remotePath != "" {
		u.Path = path.Join(u.Path, remotePath)
	}
	req, err := http.NewRequestWithContext(ctx, string(WebdavMethodPropfind), u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	req.Header.Set("Depth", "0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("file not found: %s", remotePath)
	}
	if resp.StatusCode != http.StatusMultiStatus && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, fmt.Errorf("PROPFIND: %s", resp.Status)
	}

	var multistatus Multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&multistatus); err != nil {
		return nil, fmt.Errorf("failed to decode PROPFIND response: %w", err)
	}
	if len(multistatus.Responses) == 0 {
		return nil, fmt.Errorf("file not found: %s", remotePath)
	}

	r0 := multistatus.Responses[0]
	decodedHref, err := url.PathUnescape(r0.Href)
	if err != nil {
		decodedHref = r0.Href
	}
	name := path.Base(strings.TrimSuffix(decodedHref, "/"))
	if name == "" || name == "." || name == "/" {
		name = path.Base(remotePath)
		if name == "" || name == "." {
			name = "/"
		}
	}

	var modTime time.Time
	if r0.Propstat.Prop.GetLastModified != "" {
		if pt, err := time.Parse(time.RFC1123, r0.Propstat.Prop.GetLastModified); err == nil {
			modTime = pt
		} else if pt, err := time.Parse(time.RFC1123Z, r0.Propstat.Prop.GetLastModified); err == nil {
			modTime = pt
		}
	}

	return &storagetypes.FileInfo{
		Name:    name,
		Path:    remotePath,
		Size:    r0.Propstat.Prop.GetContentLength,
		IsDir:   r0.Propstat.Prop.ResourceType.IsCollection(),
		ModTime: modTime,
	}, nil
}
