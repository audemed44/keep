// Package docker is a minimal client for the Docker Engine API over its
// unix socket: run a command in a container, stop and start containers,
// and read a container's mounts.
package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrNotFound = errors.New("no such container")

type Client struct {
	http *http.Client
	// Exec streams run as long as the command does; the caller's context
	// bounds them.
	stream *http.Client
}

func New(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    2,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Client{
		http:   &http.Client{Transport: transport, Timeout: 30 * time.Second},
		stream: &http.Client{Transport: transport},
	}
}

func (c *Client) do(ctx context.Context, client *http.Client, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	// 304: already stopped or already started.
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		var e struct{ Message string }
		if json.Unmarshal(msg, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("docker: %s", e.Message)
		}
		return nil, fmt.Errorf("docker: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.do(ctx, c.http, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type Mount struct {
	Type        string `json:"Type"` // bind or volume
	Name        string `json:"Name"` // the volume's name
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type Container struct {
	Name    string
	Running bool
	Mounts  []Mount
}

// Inspect reads a container by name or id.
func (c *Client) Inspect(ctx context.Context, name string) (Container, error) {
	var raw struct {
		Name   string
		State  struct{ Running bool }
		Mounts []Mount
	}
	if err := c.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, &raw); err != nil {
		return Container{}, err
	}
	return Container{Name: strings.TrimPrefix(raw.Name, "/"), Running: raw.State.Running, Mounts: raw.Mounts}, nil
}

func (c *Client) Stop(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/stop?t=30", nil, nil)
}

func (c *Client) Start(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil, nil)
}

// ExitError is a command that ran and failed.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return fmt.Sprintf("exit status %d: %s", e.Code, msg)
}

// stderrLimit caps how much of a command's stderr is kept for its error.
const stderrLimit = 16 << 10

// Exec runs cmd in a running container and copies its stdout to stdout.
// A non-zero exit is an *ExitError with the end of stderr.
func (c *Client) Exec(ctx context.Context, container string, cmd []string, stdout io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	var created struct{ Id string }
	err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", map[string]any{
		"AttachStdout": true, "AttachStderr": true, "Cmd": cmd,
	}, &created)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, c.stream, http.MethodPost, "/exec/"+created.Id+"/start", map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return err
	}
	stderr := &tail{max: stderrLimit}
	err = demux(resp.Body, stdout, stderr)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading the output of %s: %w", cmd[0], err)
	}
	var state struct {
		Running  bool
		ExitCode int
	}
	if err := c.call(ctx, http.MethodGet, "/exec/"+created.Id+"/json", nil, &state); err != nil {
		return err
	}
	if state.ExitCode != 0 {
		return &ExitError{Code: state.ExitCode, Stderr: stderr.String()}
	}
	return nil
}

// demux splits Docker's multiplexed stream: frames of an 8-byte header
// (stream 1 stdout, 2 stderr; then a big-endian length) and the payload.
func demux(r io.Reader, stdout, stderr io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		n := int64(binary.BigEndian.Uint32(hdr[4:]))
		w := io.Discard
		switch hdr[0] {
		case 1:
			w = stdout
		case 2:
			w = stderr
		}
		if _, err := io.CopyN(w, r, n); err != nil {
			return err
		}
	}
}

// tail keeps the last max bytes written to it.
type tail struct {
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }
