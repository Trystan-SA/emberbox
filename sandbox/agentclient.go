// agentClient is the host-side counterpart of agent.Agent: it opens a TCP
// connection to an emberbox-agent (running in a container, microVM, or
// anywhere reachable over TCP), sends one agent.Request, and decodes one
// agent.Response. DockerBackend uses it directly; FirecrackerBackend will
// reuse it once vsock dialing lands.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

// dialAgent dials addr and round-trips one agent request/response.
func dialAgent(ctx context.Context, addr, toolName string, input json.RawMessage, timeout time.Duration) (*ExecResult, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial agent: %w", err)
	}
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
// Used after launching a container/microVM to know when the in-guest agent
// is ready to accept requests.
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
