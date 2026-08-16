package dlna

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Device represents a discovered DLNA/UPnP device.
type Device struct {
	DeviceType   string `xml:"deviceType"`
	FriendlyName string `xml:"friendlyName"`
	Manufacturer string `xml:"manufacturer"`
	ModelName    string `xml:"modelName"`
	ModelNumber  string `xml:"modelNumber"`
	URLBase      string
}

type rootXML struct {
	XMLName xml.Name `xml:"root"`
	Device  Device   `xml:"device"`
}

// Client represents a DLNA / UPnP Control Point client.
type Client struct {
	httpClient *http.Client
}

// NewClient creates a new DLNA client.
func NewClient(timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// GetDeviceDescription fetches and parses the UPnP device XML from the given descriptor URL.
func (c *Client) GetDeviceDescription(ctx context.Context, descURL string) (*Device, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, descURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch device description: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var root rootXML
	if err := xml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("failed to parse xml: %w", err)
	}

	root.Device.URLBase = descURL
	return &root.Device, nil
}

// Browse performs a ContentDirectory Browse action on a DLNA DMS control URL.
func (c *Client) Browse(ctx context.Context, controlURL, objectID string) (string, error) {
	if objectID == "" {
		objectID = "0"
	}

	soapAction := `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"`
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <ObjectID>%s</ObjectID>
      <BrowseFlag>BrowseDirectChildren</BrowseFlag>
      <Filter>*</Filter>
      <StartingIndex>0</StartingIndex>
      <RequestedCount>100</RequestedCount>
      <SortCriteria></SortCriteria>
    </u:Browse>
  </s:Body>
</s:Envelope>`, objectID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", soapAction)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("browse SOAP request failed: %w", err)
	}
	defer resp.Body.Close()

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(resBody), nil
}

// Play sends a Play action to a DLNA DMR AVTransport control URL.
func (c *Client) Play(ctx context.Context, controlURL string) error {
	soapAction := `"urn:schemas-upnp-org:service:AVTransport:1#Play"`
	body := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Play xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
      <InstanceID>0</InstanceID>
      <Speed>1</Speed>
    </u:Play>
  </s:Body>
</s:Envelope>`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controlURL, bytes.NewBufferString(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", soapAction)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("play SOAP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("play failed with status %d", resp.StatusCode)
	}
	return nil
}
