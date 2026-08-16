package ftp

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Client represents an FTP client.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
}

// Dial connects to the FTP server at addr.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("ftp dial error: %w", err)
	}

	c := &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}

	// Read initial server banner
	line, err := c.readLine()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to read banner: %w", err)
	}
	if !strings.HasPrefix(line, "220") {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid banner: %s", line)
	}

	return c, nil
}

func (c *Client) readLine() (string, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *Client) sendCmd(cmd string) (string, error) {
	if _, err := fmt.Fprintf(c.conn, "%s\r\n", cmd); err != nil {
		return "", err
	}
	return c.readLine()
}

// Login authenticates with the FTP server using user and pass.
func (c *Client) Login(user, pass string) error {
	resp, err := c.sendCmd(fmt.Sprintf("USER %s", user))
	if err != nil {
		return err
	}

	if strings.HasPrefix(resp, "230") {
		return nil
	}

	if strings.HasPrefix(resp, "331") {
		resp, err = c.sendCmd(fmt.Sprintf("PASS %s", pass))
		if err != nil {
			return err
		}
		if strings.HasPrefix(resp, "230") {
			return nil
		}
	}

	return fmt.Errorf("login failed: %s", resp)
}

// Pwd returns the current working directory.
func (c *Client) Pwd() (string, error) {
	resp, err := c.sendCmd("PWD")
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resp, "257") {
		return "", fmt.Errorf("pwd failed: %s", resp)
	}
	// Format is typically 257 "/path" ...
	start := strings.Index(resp, "\"")
	end := strings.LastIndex(resp, "\"")
	if start != -1 && end > start {
		return resp[start+1 : end], nil
	}
	return resp, nil
}

// Cwd changes the current working directory.
func (c *Client) Cwd(path string) error {
	resp, err := c.sendCmd(fmt.Sprintf("CWD %s", path))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(resp, "250") {
		return fmt.Errorf("cwd failed: %s", resp)
	}
	return nil
}

// Pasv enters passive mode and returns a connection to the data port.
func (c *Client) Pasv() (net.Conn, error) {
	resp, err := c.sendCmd("PASV")
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(resp, "227") {
		return nil, fmt.Errorf("pasv failed: %s", resp)
	}

	start := strings.Index(resp, "(")
	end := strings.LastIndex(resp, ")")
	if start == -1 || end <= start {
		return nil, fmt.Errorf("malformed PASV response: %s", resp)
	}

	parts := strings.Split(resp[start+1:end], ",")
	if len(parts) != 6 {
		return nil, fmt.Errorf("invalid PASV ip/port parts: %s", resp)
	}

	ip := fmt.Sprintf("%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3])
	p1, _ := strconv.Atoi(parts[4])
	p2, _ := strconv.Atoi(parts[5])
	port := (p1 << 8) | p2

	return net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), 5*time.Second)
}

// List retrieves file listings for the specified directory.
func (c *Client) List(path string) ([]string, error) {
	dataConn, err := c.Pasv()
	if err != nil {
		return nil, err
	}
	defer dataConn.Close()

	cmd := "LIST"
	if path != "" {
		cmd = fmt.Sprintf("LIST %s", path)
	}
	resp, err := c.sendCmd(cmd)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(resp, "150") {
		return nil, fmt.Errorf("list failed to open data connection: %s", resp)
	}

	var results []string
	scanner := bufio.NewScanner(dataConn)
	for scanner.Scan() {
		results = append(results, scanner.Text())
	}

	closeResp, err := c.readLine()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(closeResp, "226") {
		return nil, fmt.Errorf("list completion error: %s", closeResp)
	}

	return results, nil
}

// Quit sends the QUIT command to the FTP server and closes the connection.
func (c *Client) Quit() error {
	_, err := c.sendCmd("QUIT")
	_ = c.Close()
	return err
}

// Close closes the underlying network connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
