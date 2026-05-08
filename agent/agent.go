// Package agent implements the in-VM agent that executes tools.
//
// The default binary cmd/emberbox-agent runs inside each microVM. It listens
// for tool execution requests from the host (typically over vsock), dispatches
// them through a tool.Registry, and writes the result back on the same
// connection.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
)

// Config configures an Agent.
type Config struct {
	Tools   *tool.Registry
	Workdir string
	Logger  *slog.Logger // optional; defaults to slog.Default()
}

// Agent dispatches tool execution requests inside a sandboxed environment.
type Agent struct {
	tools   *tool.Registry
	workdir string
	log     *slog.Logger
}

// New creates an Agent. Tools must be non-nil.
func New(cfg Config) *Agent {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Agent{
		tools:   cfg.Tools,
		workdir: cfg.Workdir,
		log:     log,
	}
}

// Request is the JSON envelope for a single tool execution.
type Request struct {
	ToolName       string          `json:"tool_name"`
	Input          json.RawMessage `json:"input"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
}

// Response is the JSON envelope returned to the caller.
type Response struct {
	Output     string `json:"output"`
	IsError    bool   `json:"is_error"`
	DurationMS int64  `json:"duration_ms"`
}

// Serve accepts connections on the listener until ctx is cancelled.
// Each connection is handed to HandleConnection in its own goroutine.
func (a *Agent) Serve(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			return fmt.Errorf("emberbox/agent: accept: %w", err)
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			a.HandleConnection(ctx, c)
		}(conn)
	}
}

// HandleConnection processes a single connection: decode one Request, dispatch,
// encode one Response, close.
func (a *Agent) HandleConnection(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req Request
	if err := decoder.Decode(&req); err != nil {
		if err != io.EOF {
			a.log.Error("emberbox/agent: decode request", "error", err)
		}
		return
	}

	a.log.Info("emberbox/agent: executing tool", "tool", req.ToolName)
	start := time.Now()

	timeout := 60 * time.Second
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp := a.executeTool(execCtx, &req)
	resp.DurationMS = time.Since(start).Milliseconds()

	if err := encoder.Encode(resp); err != nil {
		a.log.Error("emberbox/agent: encode response", "error", err)
	}
}

func (a *Agent) executeTool(ctx context.Context, req *Request) *Response {
	t, ok := a.tools.Get(req.ToolName)
	if !ok {
		return &Response{
			Output:  fmt.Sprintf("unknown tool: %s", req.ToolName),
			IsError: true,
		}
	}

	result, err := t.Execute(ctx, req.Input)
	if err != nil {
		return &Response{
			Output:  fmt.Sprintf("tool error: %s", err),
			IsError: true,
		}
	}

	return &Response{
		Output:  result.Content,
		IsError: result.IsError,
	}
}
