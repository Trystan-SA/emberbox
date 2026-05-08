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

func TestLocalExecutor_DispatchesByName(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(stubTool{name: "echo", fn: func(_ context.Context, in json.RawMessage) (*tool.Result, error) {
		return &tool.Result{Content: string(in)}, nil
	}})
	exe := newLocalExecutor(r, "")

	res, err := exe.Execute(context.Background(), "echo", json.RawMessage(`"hi"`), nil)
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, `"hi"`, res.Content)
}

func TestLocalExecutor_UnknownToolIsErrorNotErr(t *testing.T) {
	exe := newLocalExecutor(tool.NewRegistry(), "")
	res, err := exe.Execute(context.Background(), "nope", nil, nil)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "unknown tool")
}

func TestLocalExecutor_EnvPropagatedViaContext(t *testing.T) {
	var got map[string]string
	r := tool.NewRegistry()
	r.Register(stubTool{name: "envcheck", fn: func(ctx context.Context, _ json.RawMessage) (*tool.Result, error) {
		got = EnvFromContext(ctx)
		return &tool.Result{Content: "ok"}, nil
	}})
	exe := newLocalExecutor(r, "")

	_, err := exe.Execute(context.Background(), "envcheck", nil, map[string]string{"FOO": "bar"})
	require.NoError(t, err)
	require.Equal(t, "bar", got["FOO"])
}
