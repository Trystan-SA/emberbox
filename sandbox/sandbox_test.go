package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
	"github.com/stretchr/testify/require"
)

func newLocalPool(t *testing.T, r *tool.Registry) *Pool {
	t.Helper()
	p, err := New(Config{
		Mode:           ModeLocal,
		Tools:          r,
		DefaultTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	return p
}

func TestPool_LocalMode_AllocateExecuteRelease(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(stubTool{name: "echo", fn: func(_ context.Context, in json.RawMessage) (*tool.Result, error) {
		return &tool.Result{Content: string(in)}, nil
	}})
	p := newLocalPool(t, r)
	defer p.Shutdown(context.Background())

	id, err := p.Allocate(context.Background(), AllocRequest{Timeout: time.Second})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	res, err := p.Execute(context.Background(), id, "echo", json.RawMessage(`"hi"`))
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, `"hi"`, res.Content)

	p.Release(context.Background(), id)

	_, err = p.Execute(context.Background(), id, "echo", nil)
	require.ErrorIs(t, err, ErrVMNotFound)
}

func TestPool_LocalMode_RequiresTools(t *testing.T) {
	_, err := New(Config{Mode: ModeLocal})
	require.Error(t, err)
}

func TestPool_UnknownMode(t *testing.T) {
	_, err := New(Config{Mode: "bogus", Tools: tool.NewRegistry()})
	require.Error(t, err)
}

func TestPool_DefaultMode_IsLocal(t *testing.T) {
	p, err := New(Config{Tools: tool.NewRegistry()})
	require.NoError(t, err)
	defer p.Shutdown(context.Background())
	id, err := p.Allocate(context.Background(), AllocRequest{Timeout: time.Second})
	require.NoError(t, err)
	require.NotEmpty(t, id)
}

func TestPool_Status(t *testing.T) {
	p := newLocalPool(t, tool.NewRegistry())
	defer p.Shutdown(context.Background())

	pool, active := p.Status()
	require.Equal(t, 0, pool)
	require.Equal(t, 0, active)

	id, err := p.Allocate(context.Background(), AllocRequest{Timeout: time.Second})
	require.NoError(t, err)

	_, active = p.Status()
	require.Equal(t, 1, active)

	p.Release(context.Background(), id)
	_, active = p.Status()
	require.Equal(t, 0, active)
}

func TestPool_Execute_ToolErrorReportedAsContent(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(stubTool{name: "boom", fn: func(_ context.Context, _ json.RawMessage) (*tool.Result, error) {
		return nil, errors.New("kaboom")
	}})
	p := newLocalPool(t, r)
	defer p.Shutdown(context.Background())

	id, err := p.Allocate(context.Background(), AllocRequest{Timeout: time.Second})
	require.NoError(t, err)

	res, err := p.Execute(context.Background(), id, "boom", nil)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "kaboom")
}
