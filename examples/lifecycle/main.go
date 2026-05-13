// Lifecycle example: start a sandbox, list active sandboxes, run a command,
// release it. Uses host mode so it works without docker or firecracker; the
// same API works in those modes — just flip Config.Mode or pass Config.Backend.
//
//	go run ./examples/lifecycle
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

func main() {
	r := tool.NewRegistry()
	tools.RegisterDefaults(r)

	pool, err := sandbox.New(sandbox.Config{
		Mode:           sandbox.ModeHost,
		Tools:          r,
		DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer pool.Shutdown(context.Background())

	ctx := context.Background()

	// 1. Start a sandbox.
	id, err := pool.Allocate(ctx, sandbox.AllocRequest{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("allocated sandbox: %s\n", id)

	// 2. List active sandboxes. The Pool tracks them itself — Firecracker has
	//    no native "list VMs" command, so this counter is the source of truth.
	warm, active := pool.Status()
	fmt.Printf("status: warm_pool=%d active=%d\n", warm, active)

	// 3. Run a basic command.
	res, err := pool.Execute(ctx, id, "bash", json.RawMessage(`{"command":"uname -a"}`))
	if err != nil {
		panic(err)
	}
	fmt.Printf("output (%dms): %s", res.DurationMS, res.Content)

	// 4. Die.
	pool.Release(ctx, id)
	warm, active = pool.Status()
	fmt.Printf("after release: warm_pool=%d active=%d\n", warm, active)
}