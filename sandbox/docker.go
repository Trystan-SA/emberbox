// DockerBackend runs each sandbox inside a Docker container that hosts an
// emberbox-agent. One container per Allocate, reused across Execute calls,
// torn down on Release. Implemented via shell-outs to the `docker` CLI to
// avoid pulling the full Docker SDK into the dependency tree.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// DockerConfig configures a DockerBackend.
type DockerConfig struct {
	// Image is the agent image to run. Must contain /usr/local/bin/emberbox-agent
	// listening on AgentPort. Built from the repo Dockerfile.
	Image string
	// AgentPort is the TCP port the in-container agent listens on. Default 10000.
	AgentPort int
	// DockerBin is the docker CLI binary. Default "docker".
	DockerBin string
	// Network optionally sets --network. Empty = docker default (bridge).
	Network string
	// Mounts are extra bind mounts (host:container). Used by callers that
	// want the agent to access a project directory.
	Mounts []string
	// BootTimeout caps how long Boot waits for the agent to become reachable.
	// Default 15s.
	BootTimeout time.Duration
	// StopTimeout caps how long Destroy waits for the container to stop
	// gracefully before docker kills it. Default 5s (passed as `--time` to
	// `docker stop`).
	StopTimeout time.Duration
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// DockerBackend boots a fresh container per sandbox and talks to its in-container
// agent over TCP.
type DockerBackend struct {
	cfg DockerConfig
	log *slog.Logger
}

// NewDockerBackend constructs a DockerBackend. Image is required.
func NewDockerBackend(cfg DockerConfig) (*DockerBackend, error) {
	if cfg.Image == "" {
		return nil, errors.New("emberbox/sandbox: DockerConfig.Image is required")
	}
	if cfg.AgentPort == 0 {
		cfg.AgentPort = 10000
	}
	if cfg.DockerBin == "" {
		cfg.DockerBin = "docker"
	}
	if cfg.BootTimeout == 0 {
		cfg.BootTimeout = 15 * time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 5 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &DockerBackend{cfg: cfg, log: log}, nil
}

// Name implements Backend.
func (*DockerBackend) Name() string { return "docker" }

type dockerHandle struct {
	id          string // container id (docker's)
	hostPort    int    // host port mapped to AgentPort
	containerIP string // unused for now; reserved for --network=user flows
	env         map[string]string
	bootedAt    time.Time
}

func (h *dockerHandle) ID() string { return h.id }

// Boot starts a container running the agent and waits for it to be reachable.
func (b *DockerBackend) Boot(ctx context.Context, req AllocRequest) (Handle, error) {
	args := []string{
		"run", "--detach", "--rm",
		"--publish", fmt.Sprintf("127.0.0.1::%d", b.cfg.AgentPort),
	}
	if b.cfg.Network != "" {
		args = append(args, "--network", b.cfg.Network)
	}
	for _, m := range b.cfg.Mounts {
		args = append(args, "--mount", "type=bind,"+mountSpec(m))
	}
	for k, v := range req.Env {
		args = append(args, "--env", k+"="+v)
	}
	if req.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", req.MemoryMB))
	}
	args = append(args, b.cfg.Image)

	out, err := b.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	containerID := strings.TrimSpace(out)
	if containerID == "" {
		return nil, errors.New("docker run: empty container id")
	}
	b.log.Info("[Emberbox/docker] container started", "container_id", shortID(containerID), "image", b.cfg.Image)

	port, err := b.lookupPort(ctx, containerID)
	if err != nil {
		_ = b.stop(context.Background(), containerID)
		return nil, fmt.Errorf("inspect port: %w", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	b.log.Debug("[Emberbox/docker] waiting for in-container agent", "container_id", shortID(containerID), "addr", addr, "timeout", b.cfg.BootTimeout)

	waitCtx, cancel := context.WithTimeout(ctx, b.cfg.BootTimeout)
	defer cancel()
	if err := waitForAgent(waitCtx, addr, 50*time.Millisecond); err != nil {
		b.log.Error("[Emberbox/docker] in-container agent never came up", "container_id", shortID(containerID), "addr", addr, "error", err)
		_ = b.stop(context.Background(), containerID)
		return nil, fmt.Errorf("agent never came up at %s: %w", addr, err)
	}
	b.log.Info("[Emberbox/docker] container ready", "container_id", shortID(containerID), "addr", addr)

	return &dockerHandle{
		id:       containerID,
		hostPort: port,
		env:      req.Env,
		bootedAt: time.Now(),
	}, nil
}

// Exec dispatches a tool call to the container's agent.
func (b *DockerBackend) Exec(ctx context.Context, h Handle, toolName string, input json.RawMessage) (*ExecResult, error) {
	dh, ok := h.(*dockerHandle)
	if !ok {
		return nil, fmt.Errorf("emberbox/sandbox: DockerBackend got handle of type %T", h)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", dh.hostPort)

	var timeout time.Duration
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	return dialAgent(ctx, addr, toolName, input, timeout)
}

// Destroy stops and removes the container.
func (b *DockerBackend) Destroy(ctx context.Context, h Handle) error {
	dh, ok := h.(*dockerHandle)
	if !ok {
		return fmt.Errorf("emberbox/sandbox: DockerBackend got handle of type %T", h)
	}
	return b.stop(ctx, dh.id)
}

// run executes `docker <args>` and returns trimmed stdout. Stderr is folded
// into the error on failure.
func (b *DockerBackend) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.cfg.DockerBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// lookupPort returns the host port docker mapped to the agent's container port.
func (b *DockerBackend) lookupPort(ctx context.Context, containerID string) (int, error) {
	tmpl := fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%d/tcp") 0).HostPort}}`, b.cfg.AgentPort)
	out, err := b.run(ctx, "inspect", "--format", tmpl, containerID)
	if err != nil {
		return 0, err
	}
	var port int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &port); err != nil {
		return 0, fmt.Errorf("parse port %q: %w", out, err)
	}
	return port, nil
}

func (b *DockerBackend) stop(ctx context.Context, containerID string) error {
	stopSecs := int(b.cfg.StopTimeout.Seconds())
	if stopSecs < 1 {
		stopSecs = 1
	}
	if _, err := b.run(ctx, "stop", "--time", fmt.Sprintf("%d", stopSecs), containerID); err != nil {
		// container may already be gone (--rm); don't surface that as fatal.
		b.log.Debug("[Emberbox/docker] container stop failed (may be already gone)", "container_id", shortID(containerID), "error", err)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// mountSpec accepts either "host:container" or a docker --mount spec snippet
// and returns the suffix expected after "type=bind,".
func mountSpec(spec string) string {
	if strings.Contains(spec, "=") {
		return spec
	}
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return fmt.Sprintf("source=%s,target=%s", spec, spec)
	}
	return fmt.Sprintf("source=%s,target=%s", parts[0], parts[1])
}
