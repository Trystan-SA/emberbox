package sandbox

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Trystan-SA/emberbox/agent"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/stretchr/testify/require"
)

// startTestAgent spins up an in-process agent.Agent on a random TCP port and
// returns its address. Tears down on t.Cleanup.
func startTestAgent(t *testing.T, r *tool.Registry) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	a := agent.New(agent.Config{Tools: r})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		<-done
	})
	return ln.Addr().String()
}

func TestDialAgent_RoundTrip(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(stubTool{name: "echo", fn: func(_ context.Context, in json.RawMessage) (*tool.Result, error) {
		return &tool.Result{Content: string(in)}, nil
	}})
	addr := startTestAgent(t, r)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := dialAgent(ctx, addr, "echo", json.RawMessage(`"hi"`), time.Second)
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, `"hi"`, res.Content)
}

func TestDialAgent_UnknownToolIsErrorNotErr(t *testing.T) {
	addr := startTestAgent(t, tool.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := dialAgent(ctx, addr, "nope", nil, time.Second)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content, "unknown tool")
}

func TestWaitForAgent_ReturnsOnceListening(t *testing.T) {
	addr := startTestAgent(t, tool.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, waitForAgent(ctx, addr, 20*time.Millisecond))
}

func TestWaitForAgent_HonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := waitForAgent(ctx, "127.0.0.1:1", 20*time.Millisecond) // port 1 should refuse
	require.Error(t, err)
}
