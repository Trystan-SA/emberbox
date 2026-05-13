// emberbox-agent is the default in-VM agent binary. It registers the built-in
// tools from github.com/Trystan-SA/emberbox/tools and serves them over the
// network. The listen address is provided by --listen (default :10000) so the
// same binary works under TCP for tests and over a vsock proxy in production.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Trystan-SA/emberbox/agent"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/Trystan-SA/emberbox/tools"
)

func main() {
	listenAddr := flag.String("listen", ":10000", "address to listen on (TCP)")
	workdir := flag.String("workdir", "/", "agent working directory")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	r := tool.NewRegistry()
	tools.RegisterDefaults(r)

	a := agent.New(agent.Config{
		Tools:   r,
		Workdir: *workdir,
		Logger:  log,
	})

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Error("[Emberbox/agent] failed to bind listen socket", "addr", *listenAddr, "error", err)
		os.Exit(1)
	}
	log.Info("[Emberbox/agent] listening for host requests", "addr", *listenAddr, "workdir", *workdir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.Serve(ctx, ln); err != nil {
		log.Error("[Emberbox/agent] serve loop ended unexpectedly", "error", err)
		os.Exit(1)
	}
}
