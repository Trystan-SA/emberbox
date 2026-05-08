package sandboxtest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Trystan-SA/emberbox/sandbox"
	"github.com/Trystan-SA/emberbox/sandbox/sandboxtest"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/stretchr/testify/require"
)

type echoTool struct{}

func (echoTool) Name() string { return "echo" }
func (echoTool) Execute(_ context.Context, in json.RawMessage) (*tool.Result, error) {
	return &tool.Result{Content: string(in)}, nil
}

func TestNewFake_DispatchesRegisteredTool(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(echoTool{})
	p := sandboxtest.NewFake(t, r)

	id, err := p.Allocate(context.Background(), sandbox.AllocRequest{Timeout: time.Second})
	require.NoError(t, err)

	res, err := p.Execute(context.Background(), id, "echo", json.RawMessage(`"hi"`))
	require.NoError(t, err)
	require.Equal(t, `"hi"`, res.Content)
}
