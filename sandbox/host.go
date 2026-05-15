// HostBackend runs tools directly in the host process. NOT isolated;
// intended for dev/test or when the user explicitly opts out of isolation.

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
	"github.com/google/uuid"
)

// envContextKey is the context key under which per-allocation env vars are
// propagated to in-process tools running under HostBackend. In firecracker
// mode the values are not yet propagated into the guest.
type envContextKey struct{}

// EnvFromContext returns the per-allocation env map attached by HostBackend,
// or nil if none is present. Tools running in host mode that need a token or
// secret should read it via this helper.
func EnvFromContext(ctx context.Context) map[string]string {
	v, _ := ctx.Value(envContextKey{}).(map[string]string)
	return v
}

// HostBackend dispatches tools in-process against a shared registry.
type HostBackend struct {
	tools   *tool.Registry
	workdir string
}

// NewHostBackend constructs a HostBackend that dispatches against r. workdir
// is informational for tools that want to know the host CWD.
func NewHostBackend(r *tool.Registry, workdir string) *HostBackend {
	return &HostBackend{tools: r, workdir: workdir}
}

// Name implements Backend.
func (*HostBackend) Name() string { return "host" }

type hostHandle struct {
	id  string
	env map[string]string
}

func (h *hostHandle) ID() string { return h.id }

// Boot implements Backend. It's a constant-time operation in host mode —
// nothing actually boots.
func (b *HostBackend) Boot(_ context.Context, req AllocRequest) (Handle, error) {
	return &hostHandle{
		id:  "host-" + uuid.New().String()[:8],
		env: req.Env,
	}, nil
}

// Exec implements Backend.
func (b *HostBackend) Exec(ctx context.Context, h Handle, toolName string, input json.RawMessage) (*ExecResult, error) {
	hh, err := assertHandle[*hostHandle](h, "Host")
	if err != nil {
		return nil, err
	}

	t, ok := b.tools.Get(toolName)
	if !ok {
		return &ExecResult{
			Content: fmt.Sprintf("unknown tool: %s", toolName),
			IsError: true,
		}, nil
	}

	if len(hh.env) > 0 {
		ctx = context.WithValue(ctx, envContextKey{}, hh.env)
	}

	start := time.Now()
	result, err := t.Execute(ctx, input)
	if err != nil {
		return &ExecResult{
			Content:    fmt.Sprintf("tool error: %s", err),
			IsError:    true,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil
	}
	return &ExecResult{
		Content:    result.Content,
		IsError:    result.IsError,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// Destroy implements Backend. No-op in host mode.
func (b *HostBackend) Destroy(_ context.Context, _ Handle) error { return nil }
