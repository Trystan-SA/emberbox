// Package emberbox is a Go library for running LLM agent tool calls inside
// pluggable sandboxes.
//
// Emberbox exposes a single host-side API ([sandbox.Pool]) that dispatches
// tool calls to one of several isolation backends:
//
//   - Host:        in-process dispatch, no isolation. Cheapest; opt-in only.
//   - Docker:      one container per session, reused across many tool calls.
//   - Firecracker: per-task microVM (~125 ms boot, ~5 MB VMM overhead).
//
// All three implement the same [sandbox.Backend] interface, so callers can
// swap isolation levels without changing dispatch code, and can plug in
// custom backends (cloud-hypervisor, kata, gVisor, remote services, ...) by
// passing their own implementation via [sandbox.Config].Backend.
//
// # Module layout
//
// Emberbox is split into small packages with narrow responsibilities:
//
//   - [github.com/Trystan-SA/emberbox/tool]      — shared Tool / Result / Registry contract.
//   - [github.com/Trystan-SA/emberbox/sandbox]   — host-side Pool, Backend interface, built-in backends.
//   - [github.com/Trystan-SA/emberbox/sandbox/sandboxtest] — test helpers (NewFake) for downstream consumers.
//   - [github.com/Trystan-SA/emberbox/agent]     — guest-side Agent that serves tool requests over a net.Listener.
//   - [github.com/Trystan-SA/emberbox/tools]     — built-in Tool implementations (bash, file_read, file_write, file_edit, glob, grep, web_fetch).
//   - [github.com/Trystan-SA/emberbox/cmd/emberbox-agent] — default in-guest agent binary used by the Docker and Firecracker backends.
//
// # Quickstart
//
// The smallest useful program allocates a host-mode sandbox and runs a
// built-in bash tool:
//
//	r := tool.NewRegistry()
//	tools.RegisterDefaults(r)
//
//	p, err := sandbox.New(sandbox.Config{
//	    Mode:           sandbox.ModeHost,
//	    Tools:          r,
//	    DefaultTimeout: 30 * time.Second,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer p.Shutdown(context.Background())
//
//	ctx := context.Background()
//	id, _ := p.Allocate(ctx, sandbox.AllocRequest{})
//	defer p.Release(ctx, id)
//
//	res, _ := p.Execute(ctx, id, "bash", json.RawMessage(`{"command":"echo hello"}`))
//	fmt.Println(res.Content) // "hello\n"
//
// # Choosing a backend
//
// Host mode is the default and runs tools directly in the calling process —
// use it for tests, local development, or callers that have already isolated
// themselves. Docker mode is the right choice for long-running coding
// sessions that need a real filesystem and persistent state across many tool
// calls. Firecracker mode is the right choice for high-volume, short-lived
// validation workloads where per-task isolation matters more than warm-state
// reuse.
//
// # Status
//
// Emberbox is pre-1.0. The public API of the [github.com/Trystan-SA/emberbox/sandbox]
// and [github.com/Trystan-SA/emberbox/tool] packages may change between minor
// versions. See the project README for the current backend support matrix.
//
// # Examples
//
// Runnable examples live under examples/ in the repository:
// lifecycle, customtool, custombackend, dockerbackend, and firecrackerbackend.
package emberbox