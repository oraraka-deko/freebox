package smb

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// Client represents an SMB client.
type Client struct {
	conn net.Conn
}

// Dial connects to an SMB server at addr.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("smb dial error: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Negotiate performs an SMB2 Negotiate handshake with the server.
func (c *Client) Negotiate() (uint16, error) {
	// Build SMB2 Negotiate Request
	reqHeader := make([]byte, 64)
	reqHeader[0] = 0xFE
	reqHeader[1] = 'S'
	reqHeader[2] = 'M'
	reqHeader[3] = 'B'
	binary.LittleEndian.PutUint16(reqHeader[4:6], 64) // StructureSize
	binary.LittleEndian.PutUint16(reqHeader[12:14], 0) // Negotiate
	binary.LittleEndian.PutUint64(reqHeader[24:32], 1) // MessageID

	body := make([]byte, 36)
	binary.LittleEndian.PutUint16(body[0:2], 36)
	binary.LittleEndian.PutUint16(body[2:4], 1)
	binary.LittleEndian.PutUint16(body[4:6], 0x0202) // SMB 2.0.2

	packet := append(reqHeader, body...)
	tcpHeader := make([]byte, 4)
	binary.BigEndian.PutUint32(tcpHeader, uint32(len(packet)))

	if _, err := c.conn.Write(tcpHeader); err != nil {
		return 0, fmt.Errorf("failed to write tcp header: %w", err)
	}
	if _, err := c.conn.Write(packet); err != nil {
		return 0, fmt.Errorf("failed to write packet: %w", err)
	}

	respHeader := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, respHeader); err != nil {
		return 0, fmt.Errorf("failed to read response header: %w", err)
	}
	respLen := binary.BigEndian.Uint32(respHeader)
	if respLen < 64 {
		return 0, fmt.Errorf("invalid response length: %d", respLen)
	}

	respBody := make([]byte, respLen)
	if _, err := io.ReadFull(c.conn, respBody); err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	if respBody[0] != 0xFE || respBody[1] != 'S' || respBody[2] != 'M' || respBody[3] != 'B' {
		return 0, fmt.Errorf("invalid smb2 magic in response")
	}

	if len(respBody) >= 68 {
		dialect := binary.LittleEndian.Uint16(respBody[66:68])
		return dialect, nil
	}

	return 0x0202, nil
}

// Close closes the SMB client connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
