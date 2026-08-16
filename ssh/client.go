package ssh

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

// Client represents an SSH / SFTP client.
type Client struct {
	conn         net.Conn
	reader       *bufio.Reader
	serverBanner string
}

// Dial connects to an SSH server at addr and performs identification handshake.
func Dial(addr string, clientBanner string, timeout time.Duration) (*Client, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	if clientBanner == "" {
		clientBanner = "SSH-2.0-FreeboxClient_1.0"
	}

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("ssh dial failed: %w", err)
	}

	c := &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}

	// Read server identification banner
	serverBanner, err := c.reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to read server banner: %w", err)
	}
	c.serverBanner = strings.TrimSpace(serverBanner)

	if !strings.HasPrefix(c.serverBanner, "SSH-") {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid server banner: %s", c.serverBanner)
	}

	// Send client banner
	if _, err := fmt.Fprintf(conn, "%s\r\n", clientBanner); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to send client banner: %w", err)
	}

	return c, nil
}

// ServerBanner returns the identification banner received from the SSH server.
func (c *Client) ServerBanner() string {
	return c.serverBanner
}

// Close terminates the client connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
