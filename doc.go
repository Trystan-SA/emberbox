// Package emberbox is a Go library for running LLM agent tool calls inside
// pluggable sandboxes.
//
// Emberbox exposes a single host-side API ([sandbox.Pool]) that dispatches
// tool calls to one of several backends:
//
//   - Firecracker: per-task microVM (~125 ms boot, ~5 MB VMM overhead). The
//     only backend that actually isolates the guest from the host.
//   - Host:        in-process dispatch. NOT isolated — tools run with the
//     calling process's privileges and filesystem. Exists as a fallback so
//     the library stays usable on hosts that don't have Firecracker (no
//     KVM, no `firecracker` binary, non-Linux dev machines), and for tests
//     or callers that have already isolated themselves another way. It
//     provides no protection or sandboxing on its own.
//
// Both implement the same [sandbox.Backend] interface, so callers can swap
// backends without changing dispatch code, and can plug in their own
// (cloud-hypervisor, kata, gVisor, remote services, ...) by passing an
// implementation via [sandbox.Config].Backend.
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
//   - [github.com/Trystan-SA/emberbox/cmd/emberbox-agent] — default in-guest agent binary used by the Firecracker backend.
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
// Firecracker is the only backend that provides real isolation, and is the
// right choice for any workload running untrusted code or LLM-generated tool
// calls. Host mode is the default because it's the only backend that runs
// everywhere — it makes the library usable on machines that don't (or can't)
// run Firecracker, but it offers no protection: tools execute in the same
// process with the same permissions as the caller. Use it only for tests,
// local development, or when the caller is already sandboxed by something
// outside Emberbox.
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
// lifecycle, customtool, custombackend, and firecrackerbackend.
package emberbox
