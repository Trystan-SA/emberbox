// Custom in-VM agent for the firecrackerbackend example. Mirrors
// cmd/emberbox-agent but adds a "greet" tool to the registry, demonstrating
// how to ship custom tools to a Firecracker guest.
//
// Build for the guest (linux/amd64 is the usual Firecracker target):
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
//	  -o emberbox-agent ./examples/firecrackerbackend/agent
//
// Copy the resulting binary into your rootfs at /usr/local/bin/emberbox-agent
// and have the guest's init invoke it with --vsock-port=10000.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Trystan-SA/emberbox/agent"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/Trystan-SA/emberbox/tools"
)

// greetTool is the same tiny example from examples/customtool, but registered
// here on the guest side — that's where firecracker-mode tools have to live.
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
	vsockPort := flag.Uint("vsock-port", 10000, "AF_VSOCK port to listen on (Firecracker default)")
	workdir := flag.String("workdir", "/", "agent working directory")
	flag.Parse()

	// When the agent runs as PID 1 (which it does in the setup-firecracker.sh
	// rootfs), the kernel hands it an empty environment — including no PATH.
	// The built-in bash tool calls exec.Command("bash", ...), which fails with
	// "exit code -1" if PATH is unset. Plant a sane default if the parent
	// didn't supply one.
	if os.Getenv("PATH") == "" {
		_ = os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	r := tool.NewRegistry()
	tools.RegisterDefaults(r)
	r.Register(greetTool{})

	a := agent.New(agent.Config{
		Tools:   r,
		Workdir: *workdir,
		Logger:  log,
	})

	ln, err := listenVsock(uint32(*vsockPort))
	if err != nil {
		log.Error("[Emberbox/agent] vsock listen failed", "port", *vsockPort, "error", err)
		os.Exit(1)
	}
	log.Info("[Emberbox/agent] listening on vsock", "port", *vsockPort, "workdir", *workdir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.Serve(ctx, ln); err != nil {
		log.Error("[Emberbox/agent] serve loop ended unexpectedly", "error", err)
		os.Exit(1)
	}
}
