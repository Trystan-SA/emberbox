# Emberbox

Sandboxes for LLM agents in Go. Pick your isolation level — host process, Docker container, or Firecracker microVM — behind one `sandbox.Backend` interface, with built-in tools (bash, file ops, web fetch) and an extensible tool/agent API.

> **Status:** v0.1. Host mode is exercised in CI on every change. Docker mode is functional and has an integration test that runs locally when `emberbox-agent:test` is built (`docker build -t emberbox-agent:test .`); CI does not yet build that image, so the Docker path is verified by hand for now. Firecracker mode wires the orchestrator and agent but the SDK calls are stubs. Real Firecracker integration is the v0.2 milestone.

## Why

Different tasks need different isolation. Quick validation needs cheap, ephemeral isolation. A long-running coding session needs a full Linux environment with files and tools. Sometimes a developer just wants to run tools against their own filesystem with no sandbox at all. Emberbox gives all three behind one API:

- **Host** — direct dispatch in the host process. No isolation. For the user who already has their environment set up and explicitly opts out.
- **Docker** — one container per session, lives across many tool calls, full environment for complex tasks and project-scoped work.
- **Firecracker** — per-task microVM (~125 ms boot, ~5 MB VMM overhead). Cheap enough to run thousands of times an hour for tiny, isolated validation workloads.

## Install

```bash
go get github.com/Trystan-SA/emberbox@latest
```

## Quickstart (host mode)

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
		Mode:           sandbox.ModeHost,
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

For host mode, register against the registry you pass to `sandbox.Config.Tools`. For docker or firecracker mode, register inside your custom in-guest agent binary (build against `github.com/Trystan-SA/emberbox/agent`) and bake that binary into the image / rootfs the backend uses.

## Testing your integration

`sandboxtest.NewFake` returns a `*sandbox.Pool` running in host mode — useful for downstream consumers who want to test their dispatch logic without spinning up containers or VMs.

```go
import "github.com/Trystan-SA/emberbox/sandbox/sandboxtest"

func TestMyDispatcher(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(myMockTool{})
	p := sandboxtest.NewFake(t, r)
	// ... use p like a real Pool
}
```

## Examples

Runnable examples live under [`examples/`](./examples):

- [`examples/lifecycle`](./examples/lifecycle) — allocate a sandbox, list active sandboxes via `Pool.Status`, run a bash command, release.
- [`examples/customtool`](./examples/customtool) — register a custom `tool.Tool` alongside the built-ins and dispatch it.
- [`examples/custombackend`](./examples/custombackend) — plug a custom `sandbox.Backend` into the Pool (the same seam Firecracker slots into).
- [`examples/dockerbackend`](./examples/dockerbackend) — drive a real Docker container end-to-end.

```bash
go run ./examples/lifecycle
go run ./examples/customtool
go run ./examples/custombackend

# Docker example: build the agent image first.
docker build -t emberbox-agent:test .
go run ./examples/dockerbackend
```

## Backends

`sandbox.Pool` drives any implementation of the `sandbox.Backend` interface:

```go
type Backend interface {
    Name() string
    Boot(ctx context.Context, req AllocRequest) (Handle, error)
    Exec(ctx context.Context, h Handle, toolName string, input json.RawMessage) (*ExecResult, error)
    Destroy(ctx context.Context, h Handle) error
}
```

Built-ins:
- `sandbox.NewHostBackend(r, workdir)` — in-process dispatch. No isolation. Selected by `Mode: ModeHost` (the default).
- `sandbox.NewDockerBackend(cfg)` — one container per `Allocate`, reused across many `Execute` calls, torn down on `Release`. Talks to an in-container `emberbox-agent` over TCP. Build the image from the repo `Dockerfile`.
- `sandbox.NewFirecrackerBackend(cfg)` — Firecracker microVMs for per-task isolation. Selected by `Mode: ModeFirecracker`. **Stubbed today** — `Boot` returns a handle but no VMM is launched, and `Exec` returns `ErrFirecrackerNotImplemented`. The real `firecracker-go-sdk` + vsock impl lands behind this same interface in v0.2 and reuses the same `agentclient` machinery the Docker backend uses today.

To plug in your own (cloud-hypervisor, kata, gVisor, a remote sandbox service, ...), pass it via `Config.Backend`:

```go
pool, _ := sandbox.New(sandbox.Config{
    Backend:        myBackend,
    DefaultTimeout: 30 * time.Second,
})
```

## Packages

- `tool/` — shared `Tool` interface, `Result`, `Registry`. Both `sandbox` and `agent` import this.
- `sandbox/` — host-side: `Pool`, `Allocate`, `Execute`, `Release`, `Shutdown`, plus the pluggable `Backend` interface and built-in `HostBackend` / `DockerBackend` / `FirecrackerBackend`.
- `sandbox/sandboxtest/` — `NewFake` helper for downstream tests.
- `agent/` — guest-side: `Agent.Serve` listens for tool requests on a `net.Listener`.
- `tools/` — built-in `Tool` implementations: bash, file_read, file_write, file_edit, glob, grep, web_fetch, plus `RegisterDefaults`.
- `cmd/emberbox-agent/` — default in-guest agent binary. Run inside a container or microVM; talks to the host over TCP.
- `Dockerfile` — multi-stage build for the `emberbox-agent` Alpine image used by `DockerBackend`.

## License

MIT. See [LICENSE](./LICENSE).
