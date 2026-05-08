// In-process executor — runs tools directly in the host. NOT isolated;
// intended for dev/test only.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
)

// envContextKey is the context key under which per-allocation env vars are
// propagated to in-process tools running in local mode. In firecracker mode
// the same map is delivered to the guest via VM boot config.
type envContextKey struct{}

// EnvFromContext returns the per-allocation env map attached by the local
// executor, or nil if none is present. Tools running in local mode that need
// a token or secret should read it via this helper.
func EnvFromContext(ctx context.Context) map[string]string {
	v, _ := ctx.Value(envContextKey{}).(map[string]string)
	return v
}

// localExecutor runs tools directly in-process.
type localExecutor struct {
	tools   *tool.Registry
	workdir string
}

func newLocalExecutor(tools *tool.Registry, workdir string) *localExecutor {
	return &localExecutor{tools: tools, workdir: workdir}
}

func (l *localExecutor) Execute(ctx context.Context, toolName string, input json.RawMessage, env map[string]string) (*ExecResult, error) {
	t, ok := l.tools.Get(toolName)
	if !ok {
		return &ExecResult{
			Content: fmt.Sprintf("unknown tool: %s", toolName),
			IsError: true,
		}, nil
	}

	if len(env) > 0 {
		ctx = context.WithValue(ctx, envContextKey{}, env)
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
