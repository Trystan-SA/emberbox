// agentClient is the host-side counterpart of agent.Agent: it opens a
// connection to an emberbox-agent (running in a container, microVM, or
// anywhere reachable over a net.Conn), sends one agent.Request, and decodes
// one agent.Response.
//
// DockerBackend uses dialAgent over TCP; FirecrackerBackend uses
// dialAgentVsockUDS which goes through Firecracker's vsock UDS multiplexer.
// Both funnel into agentRoundTrip once the connection is established.

package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// agentRequest mirrors agent.Request. Duplicated here so sandbox doesn't
// depend on the guest-side agent package (which would create an unnecessary
// cycle of host↔guest imports).
type agentRequest struct {
	ToolName       string          `json:"tool_name"`
	Input          json.RawMessage `json:"input"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
}

// agentResponse mirrors agent.Response.
type agentResponse struct {
	Output     string `json:"output"`
	IsError    bool   `json:"is_error"`
	DurationMS int64  `json:"duration_ms"`
}

// dialAgent dials addr (TCP) and round-trips one agent request/response.
func dialAgent(ctx context.Context, addr, toolName string, input json.RawMessage, timeout time.Duration) (*ExecResult, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial agent: %w", err)
	}
	return agentRoundTrip(ctx, conn, toolName, input, timeout)
}

// dialAgentVsockUDS dials the Firecracker vsock UDS multiplexer at udsPath,
// negotiates a CONNECT to the given guest vsock port, and then round-trips
// one agent request/response.
//
// Wire protocol after dial:
//
//	host -> firecracker: "CONNECT <port>\n"
//	firecracker -> host: "OK <port>\n"   (success; port is the ephemeral
//	                                     guest-side source port)
//
// On any other reply ("REJECTED" or socket close), Firecracker has refused
// the connection (no listener on that port, vsock device misconfigured, etc.)
// and we surface that as a dial error.
func dialAgentVsockUDS(ctx context.Context, udsPath string, port uint32, toolName string, input json.RawMessage, timeout time.Duration) (*ExecResult, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", udsPath)
	if err != nil {
		return nil, fmt.Errorf("dial vsock uds %s: %w", udsPath, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send vsock CONNECT: %w", err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read vsock CONNECT reply: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "OK ") {
		_ = conn.Close()
		return nil, fmt.Errorf("vsock CONNECT refused: %q", line)
	}
	return agentRoundTrip(ctx, &vsockBufferedConn{Conn: conn, br: reader}, toolName, input, timeout)
}

// vsockBufferedConn wraps a net.Conn with a bufio.Reader so any bytes that
// have already been consumed (e.g. while reading the OK line) are still
// available to the JSON decoder. Without this, a request piggybacked behind
// "OK\n" in the same packet would be lost.
type vsockBufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *vsockBufferedConn) Read(b []byte) (int, error) { return c.br.Read(b) }

// agentRoundTrip is the transport-agnostic agent protocol step: encode one
// request, decode one response, close.
func agentRoundTrip(ctx context.Context, conn net.Conn, toolName string, input json.RawMessage, timeout time.Duration) (*ExecResult, error) {
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	req := agentRequest{
		ToolName: toolName,
		Input:    input,
	}
	if timeout > 0 {
		req.TimeoutSeconds = int(timeout.Seconds())
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	var resp agentResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &ExecResult{
		Content:    resp.Output,
		IsError:    resp.IsError,
		DurationMS: resp.DurationMS,
	}, nil
}

// waitForAgent polls addr until a TCP connection succeeds or ctx expires.
// Used after launching a container to know when the in-container agent is
// ready to accept requests.
func waitForAgent(ctx context.Context, addr string, interval time.Duration) error {
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: interval}).DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waitForAgent %s: %w", addr, ctx.Err())
		case <-t.C:
		}
	}
}

// waitForVsockAgent polls the Firecracker vsock UDS multiplexer until a
// CONNECT to the given port succeeds (or ctx expires). Returns nil as soon
// as the in-VM agent accepts a probe connection.
func waitForVsockAgent(ctx context.Context, udsPath string, port uint32, interval time.Duration) error {
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := vsockProbe(ctx, udsPath, port); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waitForVsockAgent %s port=%d: %w", udsPath, port, ctx.Err())
		case <-t.C:
		}
	}
}

// vsockProbe opens the UDS, sends CONNECT, reads the reply, and closes. Used
// only as a readiness signal — the byte body itself is discarded.
func vsockProbe(ctx context.Context, udsPath string, port uint32) error {
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := d.DialContext(ctx, "unix", udsPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "OK ") {
		return fmt.Errorf("vsock probe refused: %q", strings.TrimSpace(line))
	}
	return nil
}
