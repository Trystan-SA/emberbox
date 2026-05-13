// LocalBackend runs tools directly in the host process. NOT isolated;
// intended for dev/test only.
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
// propagated to in-process tools running under LocalBackend. In firecracker
// mode the same map is delivered to the guest via VM boot config.
type envContextKey struct{}

// EnvFromContext returns the per-allocation env map attached by LocalBackend,
// or nil if none is present. Tools running in local mode that need a token or
// secret should read it via this helper.
func EnvFromContext(ctx context.Context) map[string]string {
	v, _ := ctx.Value(envContextKey{}).(map[string]string)
	return v
}

// LocalBackend dispatches tools in-process against a shared registry.
type LocalBackend struct {
	tools   *tool.Registry
	workdir string
}

// NewLocalBackend constructs a LocalBackend that dispatches against r. workdir
// is informational for tools that want to know the host CWD.
func NewLocalBackend(r *tool.Registry, workdir string) *LocalBackend {
	return &LocalBackend{tools: r, workdir: workdir}
}

// Name implements Backend.
func (*LocalBackend) Name() string { return "local" }

type localHandle struct {
	id  string
	env map[string]string
}

func (h *localHandle) ID() string { return h.id }

// Boot implements Backend. It's a constant-time operation in local mode —
// nothing actually boots.
func (b *LocalBackend) Boot(_ context.Context, req AllocRequest) (Handle, error) {
	return &localHandle{
		id:  "local-" + uuid.New().String()[:8],
		env: req.Env,
	}, nil
}

// Exec implements Backend.
func (b *LocalBackend) Exec(ctx context.Context, h Handle, toolName string, input json.RawMessage) (*ExecResult, error) {
	lh, ok := h.(*localHandle)
	if !ok {
		return nil, fmt.Errorf("emberbox/sandbox: LocalBackend got handle of type %T", h)
	}

	t, ok := b.tools.Get(toolName)
	if !ok {
		return &ExecResult{
			Content: fmt.Sprintf("unknown tool: %s", toolName),
			IsError: true,
		}, nil
	}

	if len(lh.env) > 0 {
		ctx = context.WithValue(ctx, envContextKey{}, lh.env)
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

// Destroy implements Backend. No-op in local mode.
func (b *LocalBackend) Destroy(_ context.Context, _ Handle) error { return nil }
