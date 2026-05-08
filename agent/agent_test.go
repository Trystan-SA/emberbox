package agent

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
	"github.com/stretchr/testify/require"
)

// echoTool returns its input as the result content.
type echoTool struct{}

func (echoTool) Name() string { return "echo" }
func (echoTool) Execute(_ context.Context, input json.RawMessage) (*tool.Result, error) {
	return &tool.Result{Content: string(input)}, nil
}

func TestAgent_HandleConnection_DispatchesAndResponds(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(echoTool{})
	a := New(Config{Tools: r})

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go a.HandleConnection(context.Background(), c2)

	req := Request{ToolName: "echo", Input: json.RawMessage(`"hi"`)}
	require.NoError(t, json.NewEncoder(c1).Encode(req))

	var resp Response
	require.NoError(t, c1.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(c1).Decode(&resp))
	require.False(t, resp.IsError)
	require.Equal(t, `"hi"`, resp.Output)
}

func TestAgent_HandleConnection_UnknownTool(t *testing.T) {
	r := tool.NewRegistry()
	a := New(Config{Tools: r})

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go a.HandleConnection(context.Background(), c2)

	require.NoError(t, json.NewEncoder(c1).Encode(Request{ToolName: "nope"}))

	var resp Response
	require.NoError(t, c1.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(c1).Decode(&resp))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Output, "unknown tool")
}
