// Custom tool example: register your own tool.Tool alongside the built-ins
// and dispatch it through the sandbox like any other tool.
//
//	go run ./examples/customtool
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Trystan-SA/emberbox/sandbox"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/Trystan-SA/emberbox/tools"
)

type greetTool struct{}

func (greetTool) Name() string { return "greet" }

func (greetTool) Execute(_ context.Context, in json.RawMessage) (*tool.Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		args.Name = "world"
	}
	return &tool.Result{Content: fmt.Sprintf("hello, %s", args.Name)}, nil
}

func main() {
	r := tool.NewRegistry()
	tools.RegisterDefaults(r)
	r.Register(greetTool{})

	pool, err := sandbox.New(sandbox.Config{
		Mode:           sandbox.ModeLocal,
		Tools:          r,
		DefaultTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer pool.Shutdown(context.Background())

	ctx := context.Background()
	id, err := pool.Allocate(ctx, sandbox.AllocRequest{})
	if err != nil {
		panic(err)
	}
	defer pool.Release(ctx, id)

	res, err := pool.Execute(ctx, id, "greet", json.RawMessage(`{"name":"emberbox"}`))
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content)
}
