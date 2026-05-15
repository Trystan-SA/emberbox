# Emberbox

> ⚠️ **Work in progress.** APIs, backends, and on-disk formats may change without notice. Not yet recommended for production use.

Emberbox is developed as the VM orchestration layer for [Forgebox](https://github.com/Trystan-SA/forgebox), a project for running AI agents in isolated environments. It's published as a standalone library so other Go projects can reuse the same sandbox/agent primitives.

Sandboxes for LLM agents in Go. Pick your isolation level (host process or Firecracker microVM) behind one `sandbox.Backend` interface, with built-in tools (bash, file ops, web fetch) and an extensible tool/agent API.

> **Status:** v0.2. Host mode is exercised in CI on every change. Firecracker mode is functional: the backend launches real `firecracker` subprocesses, drives Firecracker's REST API over its UDS, and talks to the in-VM agent over vsock (via Firecracker's UDS multiplexer, so the host doesn't need AF_VSOCK). End-to-end with a real kernel/rootfs is exercised by `TestFirecrackerBackend_RealBinaryIntegration`, which skips unless `EMBERBOX_FIRECRACKER_BIN`, `_KERNEL`, and `_ROOTFS` are all set.

## Why

Different tasks need different isolation. Quick validation needs cheap, ephemeral isolation. Sometimes a developer just wants to run tools against their own filesystem with no sandbox at all. Emberbox gives both behind one API:

- **Host**: direct dispatch in the host process. No isolation. For the user who already has their environment set up and explicitly opts out.
- **Firecracker**: per-task microVM (~125 ms boot, ~5 MB VMM overhead). Cheap enough to run thousands of times an hour for tiny, isolated validation workloads.

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

For host mode, register against the registry you pass to `sandbox.Config.Tools`. For firecracker mode, register inside your custom in-guest agent binary (build against `github.com/Trystan-SA/emberbox/agent`) and bake that binary into the rootfs the backend uses.

## Testing your integration

`sandboxtest.NewFake` returns a `*sandbox.Pool` running in host mode, useful for downstream consumers who want to test their dispatch logic without spinning up VMs.

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

- [`examples/lifecycle`](./examples/lifecycle): allocate a sandbox, list active sandboxes via `Pool.Status`, run a bash command, release.
- [`examples/customtool`](./examples/customtool): register a custom `tool.Tool` alongside the built-ins and dispatch it.
- [`examples/custombackend`](./examples/custombackend): plug a custom `sandbox.Backend` into the Pool (the same seam Firecracker slots into).
- [`examples/firecrackerbackend`](./examples/firecrackerbackend): spawn a Firecracker microVM and call a custom tool baked into the in-VM agent. Includes a runnable in-guest agent under [`examples/firecrackerbackend/agent`](./examples/firecrackerbackend/agent).

```bash
go run ./examples/lifecycle
go run ./examples/customtool
go run ./examples/custombackend

# Firecracker example: one-shot setup downloads firecracker + kernel,
# builds the custom agent, packages an ext4 rootfs, writes an .envrc.
./scripts/setup-firecracker.sh
source ~/.emberbox/firecracker/.envrc
go run ./examples/firecrackerbackend
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
- `sandbox.NewHostBackend(r, workdir)`: in-process dispatch. No isolation. Selected by `Mode: ModeHost` (the default).
- `sandbox.NewFirecrackerBackend(cfg)`: Firecracker microVMs for per-task isolation. Selected by `Mode: ModeFirecracker`. Each `Allocate` launches a real `firecracker` subprocess, configures it via REST API over a per-VM UDS, and talks to the in-VM `emberbox-agent` over vsock (Firecracker's UDS multiplexer handles the host side, so the host doesn't need AF_VSOCK). Requires `FirecrackerConfig.KernelPath` (vmlinux), `FirecrackerConfig.RootfsPath` (ext4 with `/usr/local/bin/emberbox-agent` invoked at boot with `--vsock-port=10000`), and a Firecracker-capable kernel on the host (KVM). Env injection from `AllocRequest.Env` is logged but not yet propagated into the guest; use a pre-baked rootfs or kernel cmdline for now.

To plug in your own (cloud-hypervisor, kata, gVisor, a remote sandbox service, ...), pass it via `Config.Backend`:

```go
pool, _ := sandbox.New(sandbox.Config{
    Backend:        myBackend,
    DefaultTimeout: 30 * time.Second,
})
```

## Packages

- `tool/`: shared `Tool` interface, `Result`, `Registry`. Both `sandbox` and `agent` import this.
- `sandbox/`: host-side `Pool`, `Allocate`, `Execute`, `Release`, `Shutdown`, plus the pluggable `Backend` interface and built-in `HostBackend` / `FirecrackerBackend`.
- `sandbox/sandboxtest/`: `NewFake` helper for downstream tests.
- `agent/`: guest-side `Agent.Serve` listens for tool requests on a `net.Listener`.
- `tools/`: built-in `Tool` implementations: bash, file_read, file_write, file_edit, glob, grep, web_fetch, plus `RegisterDefaults`.
- `cmd/emberbox-agent/`: default in-guest agent binary. Run inside a Firecracker microVM; talks to the host over AF_VSOCK.

## License

MIT. See [LICENSE](./LICENSE).
