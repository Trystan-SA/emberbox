// FirecrackerBackend example: spawn a real Firecracker microVM, dispatch a
// custom tool through the in-VM agent, then tear the VM down.
//
// Custom tools for firecracker mode live inside the in-guest agent binary —
// the host just calls them by name. So this example has two pieces:
//
//  1. examples/firecrackerbackend/agent — a small in-VM agent that registers
//     a custom "greet" tool alongside the built-ins. Cross-compile for the
//     guest and bake into your rootfs at /usr/local/bin/emberbox-agent:
//
//       CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
//         -o emberbox-agent ./examples/firecrackerbackend/agent
//
//     The rootfs must invoke this binary at boot with --vsock-port=10000.
//
//  2. This file — host-side spawner/caller.
//
// To run end-to-end you need a Firecracker-capable host (KVM) plus three
// things on disk: the `firecracker` binary, a vmlinux kernel, and an ext4
// rootfs containing the agent. The repo ships a one-shot setup script:
//
//   ./scripts/setup-firecracker.sh
//   source ~/.emberbox/firecracker/.envrc
//   go run ./examples/firecrackerbackend
//
// Or wire the three EMBERBOX_FIRECRACKER_* env vars by hand:
//
//   EMBERBOX_FIRECRACKER_BIN=/usr/local/bin/firecracker \
//   EMBERBOX_FIRECRACKER_KERNEL=/path/to/vmlinux \
//   EMBERBOX_FIRECRACKER_ROOTFS=/path/to/rootfs.ext4 \
//     go run ./examples/firecrackerbackend
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Trystan-SA/emberbox/sandbox"
)

func main() {
	bin := os.Getenv("EMBERBOX_FIRECRACKER_BIN")
	kernel := os.Getenv("EMBERBOX_FIRECRACKER_KERNEL")
	rootfs := os.Getenv("EMBERBOX_FIRECRACKER_ROOTFS")
	if kernel == "" || rootfs == "" {
		fmt.Fprintln(os.Stderr, "set EMBERBOX_FIRECRACKER_KERNEL and EMBERBOX_FIRECRACKER_ROOTFS (and optionally _BIN) to run this example")
		os.Exit(2)
	}

	backend := sandbox.NewFirecrackerBackend(sandbox.FirecrackerConfig{
		FirecrackerBinary: bin, // empty → resolved via $PATH
		KernelPath:        kernel,
		RootfsPath:        rootfs,
		BootTimeout:       45 * time.Second,
	})

	pool, err := sandbox.New(sandbox.Config{
		Backend:        backend,
		DefaultTimeout: 30 * time.Second,
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
	fmt.Printf("microVM booted: %s\n", id)

	// 1. Call the custom tool. The host doesn't need the tool's code — only
	//    its name and the JSON shape its Execute expects. The agent baked into
	//    the rootfs owns the implementation.
	payload, _ := json.Marshal(map[string]string{"name": "emberbox"})
	res, err := pool.Execute(ctx, id, "greet", payload)
	if err != nil {
		panic(err)
	}
	fmt.Printf("greet (%dms): %s\n", res.DurationMS, res.Content)

	// 2. Built-in tools still work — they're registered by the agent's call
	//    to tools.RegisterDefaults.
	res, err = pool.Execute(ctx, id, "bash", json.RawMessage(`{"command":"uname -a"}`))
	if err != nil {
		panic(err)
	}
	fmt.Printf("bash (%dms): %s", res.DurationMS, res.Content)
}
