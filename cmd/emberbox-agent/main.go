// emberbox-agent is the default in-VM agent binary. It registers the built-in
// tools from github.com/Trystan-SA/emberbox/tools and serves them over the
// network. The listener is configurable so the same binary can run under TCP
// (Docker, tests) or AF_VSOCK (Firecracker): pass --vsock-port to listen on
// vsock instead of --listen's TCP socket.
package main

import (
	"context"
	"errors"
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
	listenAddr := flag.String("listen", ":10000", "TCP address to listen on (ignored when --vsock-port is set)")
	vsockPort := flag.Uint("vsock-port", 0, "if non-zero, listen on AF_VSOCK at this port instead of TCP")
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

	ln, transport, err := makeListener(*vsockPort, *listenAddr)
	if err != nil {
		log.Error("[Emberbox/agent] failed to bind listen socket", "transport", transport, "error", err)
		os.Exit(1)
	}
	log.Info("[Emberbox/agent] listening for host requests", "transport", transport, "addr", ln.Addr().String(), "workdir", *workdir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.Serve(ctx, ln); err != nil {
		log.Error("[Emberbox/agent] serve loop ended unexpectedly", "error", err)
		os.Exit(1)
	}
}

// makeListener returns the listener selected by flags. vsock takes precedence
// when --vsock-port is non-zero.
func makeListener(vsockPort uint, tcpAddr string) (net.Listener, string, error) {
	if vsockPort != 0 {
		ln, err := listenVsock(uint32(vsockPort))
		if err != nil {
			return nil, "vsock", err
		}
		return ln, "vsock", nil
	}
	if tcpAddr == "" {
		return nil, "tcp", errors.New("either --listen or --vsock-port must be set")
	}
	ln, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return nil, "tcp", err
	}
	return ln, "tcp", nil
}
