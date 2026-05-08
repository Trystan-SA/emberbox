# Emberbox

Firecracker microVM sandboxes for LLM agents in Go — one VM per session, with built-in tools (bash, file ops, web fetch) and an extensible tool/agent API.

> **Status:** v0.1. Local mode is functional and tested. Firecracker mode wires the orchestrator and agent but the SDK calls are stubs — the same code path that ships in ForgeBox today. Real Firecracker integration is the v0.2 milestone.

## Why

Running LLM-invoked tools on the host is unsafe; running them in a container shares a kernel with the host; running them in a full VM is too slow for per-task isolation. Firecracker microVMs (~125 ms boot, ~5 MB VMM overhead) sit in the right place: per-task isolation cheap enough to run thousands of times an hour. Emberbox packages the orchestration + the in-VM agent + a standard tool set so you don't have to write any of it yourself.

## Install

```bash
go get github.com/Trystan-SA/emberbox@latest
```

## Quickstart (local mode)

```go
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

	p, err := sandbox.New(sandbox.Config{
		Mode:           sandbox.ModeLocal,
		Tools:          r,
		DefaultTimeout: 30 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer p.Shutdown(context.Background())

	ctx := context.Background()
	id, err := p.Allocate(ctx, sandbox.AllocRequest{})
	if err != nil {
		panic(err)
	}
	defer p.Release(ctx, id)

	res, err := p.Execute(ctx, id, "bash", json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content) // "hello\n"
}
```

## Adding a custom tool

Implement `tool.Tool`:

```go
type MyTool struct{}

func (MyTool) Name() string { return "my_tool" }

func (MyTool) Execute(ctx context.Context, in json.RawMessage) (*tool.Result, error) {
	return &tool.Result{Content: "did the thing"}, nil
}
```

Register it:

```go
r := tool.NewRegistry()
tools.RegisterDefaults(r)
r.Register(MyTool{})
```

For local mode, register against the registry you pass to `sandbox.Config.Tools`. For firecracker mode, register inside your custom in-VM agent binary (build against `github.com/Trystan-SA/emberbox/agent`) and bake that binary into the rootfs you point `sandbox.Config.RootfsPath` at.

## Testing your integration

`sandboxtest.NewFake` returns a `*sandbox.Pool` running in local mode — useful for downstream consumers who want to test their dispatch logic without spinning up VMs.

```go
import "github.com/Trystan-SA/emberbox/sandbox/sandboxtest"

func TestMyDispatcher(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(myMockTool{})
	p := sandboxtest.NewFake(t, r)
	// ... use p like a real Pool
}
```

## Packages

- `tool/` — shared `Tool` interface, `Result`, `Registry`. Both `sandbox` and `agent` import this.
- `sandbox/` — host-side: `Pool`, `Allocate`, `Execute`, `Release`, `Shutdown`.
- `sandbox/sandboxtest/` — `NewFake` helper for downstream tests.
- `agent/` — guest-side: `Agent.Serve` listens for tool requests on a `net.Listener`.
- `tools/` — built-in `Tool` implementations: bash, file_read, file_write, file_edit, glob, grep, web_fetch, plus `RegisterDefaults`.
- `cmd/emberbox-agent/` — default in-VM agent binary.

## License

MIT. See [LICENSE](./LICENSE).
