package sandbox

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Trystan-SA/emberbox/tool"
	"github.com/stretchr/testify/require"
)

type stubTool struct {
	name string
	fn   func(context.Context, json.RawMessage) (*tool.Result, error)
}

func (s stubTool) Name() string { return s.name }
func (s stubTool) Execute(ctx context.Context, in json.RawMessage) (*tool.Result, error) {
	return s.fn(ctx, in)
}

func newBootedHostHandle(t *testing.T, b *HostBackend, env map[string]string) Handle {
	t.Helper()
	h, err := b.Boot(context.Background(), AllocRequest{Env: env})
	require.NoError(t, err)
	return h
}

func TestHostBackend_DispatchesByName(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(stubTool{name: "echo", fn: func(_ context.Context, in json.RawMessage) (*tool.Result, error) {
		return &tool.Result{Content: string(in)}, nil
	}})
	b := NewHostBackend(r, "")
	h := newBootedHostHandle(t, b, nil)

	res, err := b.Exec(context.Background(), h, "echo", json.RawMessage(`"hi"`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, `"hi"`, res.Content)
}

func TestHostBackend_UnknownToolIsErrorNotErr(t *testing.T) {
	b := NewHostBackend(tool.NewRegistry(), "")
	h := newBootedHostHandle(t, b, nil)

	res, err := b.Exec(context.Background(), h, "nope", nil)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "unknown tool")
}

func TestHostBackend_EnvPropagatedViaContext(t *testing.T) {
	var got map[string]string
	r := tool.NewRegistry()
	r.Register(stubTool{name: "envcheck", fn: func(ctx context.Context, _ json.RawMessage) (*tool.Result, error) {
		got = EnvFromContext(ctx)
		return &tool.Result{Content: "ok"}, nil
	}})
	b := NewHostBackend(r, "")
	h := newBootedHostHandle(t, b, map[string]string{"FOO": "bar"})

	_, err := b.Exec(context.Background(), h, "envcheck", nil)
	require.NoError(t, err)
	require.Equal(t, "bar", got["FOO"])
}

func TestHostBackend_RejectsForeignHandle(t *testing.T) {
	b := NewHostBackend(tool.NewRegistry(), "")
	_, err := b.Exec(context.Background(), &firecrackerHandle{id: "x"}, "any", nil)
	require.Error(t, err)
}
