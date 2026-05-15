// emberbox-agent is the default in-VM agent binary. It registers the built-in
// tools from github.com/Trystan-SA/emberbox/tools and serves them over an
// AF_VSOCK listener so the host-side FirecrackerBackend can talk to it
// through Firecracker's vsock UDS multiplexer.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Trystan-SA/emberbox/agent"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/Trystan-SA/emberbox/tools"
)

func main() {
	vsockPort := flag.Uint("vsock-port", 10000, "AF_VSOCK port to listen on")
	workdir := flag.String("workdir", "/", "agent working directory")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if *vsockPort == 0 {
		log.Error("[Emberbox/agent] --vsock-port must be non-zero")
		os.Exit(1)
	}

	r := tool.NewRegistry()
	tools.RegisterDefaults(r)

	a := agent.New(agent.Config{
		Tools:   r,
		Workdir: *workdir,
		Logger:  log,
	})

	ln, err := agent.ListenVsock(uint32(*vsockPort))
	if err != nil {
		log.Error("[Emberbox/agent] failed to bind vsock listener", "port", *vsockPort, "error", err)
		os.Exit(1)
	}
	log.Info("[Emberbox/agent] listening for host requests", "transport", "vsock", "addr", ln.Addr().String(), "workdir", *workdir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.Serve(ctx, ln); err != nil {
		log.Error("[Emberbox/agent] serve loop ended unexpectedly", "error", err)
		os.Exit(1)
	}
}
