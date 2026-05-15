// DockerBackend runs each sandbox inside a Docker container that hosts an
// emberbox-agent. One container per Allocate, reused across Execute calls,
// torn down on Release. Implemented via shell-outs to the `docker` CLI to
// avoid pulling the full Docker SDK into the dependency tree.
//
// Containers are a convenience boundary, not a security boundary: the guest
// shares the host kernel. Treat DockerBackend as protection against
// well-behaved code accidentally trashing the host, not against a hostile
// workload actively trying to escape.
//
// Operational notes:
//   - The bundled agent image runs as root so it can write to bind-mounted
//     host directories regardless of their uid. Set [DockerConfig.User]
//     (e.g. "65534:65534") when you don't need that.
//   - The default Docker bridge gives outbound internet. For egress-restricted
//     sandboxes, pre-create `docker network create --internal <name>` and pass
//     it as [DockerConfig.Network]. `--network none` is incompatible because
//     the boot path publishes the agent port over a bridge.

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
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
	// Network optionally sets --network. Empty = docker default (bridge),
	// which allows outbound internet. For egress-restricted sandboxes,
	// pre-create a network with `docker network create --internal <name>`
	// and pass that name here. `--network none` is not supported because
	// the boot path publishes the agent's port over a bridge.
	Network string
	// Mounts are extra bind mounts (host:container). Used by callers that
	// want the agent to access a project directory.
	Mounts []string
	// Privileged disables the default hardening (--cap-drop=ALL and
	// --security-opt=no-new-privileges). Leave false unless you know you
	// need elevated kernel access; this is an escape hatch, not a perf knob.
	Privileged bool
	// CapAdd lists Linux capabilities to add back on top of --cap-drop=ALL.
	// Ignored when Privileged=true.
	CapAdd []string
	// User sets --user (uid[:gid] or name). Empty = the image's default,
	// which is root for the bundled agent image. Recommended: "65534:65534"
	// (nobody) for stateless work that doesn't need to write to bind mounts.
	User string
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
	cfg       DockerConfig
	mountArgs []string // pre-validated "type=bind,source=...,target=..." values
	log       *slog.Logger
}

// NewDockerBackend constructs a DockerBackend. Image is required.
func NewDockerBackend(cfg DockerConfig) (*DockerBackend, error) {
	if cfg.Image == "" {
		return nil, errors.New("emberbox/sandbox: DockerConfig.Image is required")
	}
	mountArgs := make([]string, 0, len(cfg.Mounts))
	for _, m := range cfg.Mounts {
		spec, err := mountSpec(m)
		if err != nil {
			return nil, fmt.Errorf("emberbox/sandbox: DockerConfig.Mounts: %w", err)
		}
		mountArgs = append(mountArgs, "type=bind,"+spec)
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
	return &DockerBackend{cfg: cfg, mountArgs: mountArgs, log: log}, nil
}

// Name implements Backend.
func (*DockerBackend) Name() string { return "docker" }

type dockerHandle struct {
	id       string // container id (docker's)
	hostPort int    // host port mapped to AgentPort
	env      map[string]string
	bootedAt time.Time
}

func (h *dockerHandle) ID() string { return h.id }

// Boot starts a container running the agent and waits for it to be reachable.
func (b *DockerBackend) Boot(ctx context.Context, req AllocRequest) (Handle, error) {
	args := []string{
		"run", "--detach", "--rm",
		"--publish", fmt.Sprintf("127.0.0.1::%d", b.cfg.AgentPort),
	}
	if !b.cfg.Privileged {
		args = append(args, "--cap-drop", "ALL", "--security-opt", "no-new-privileges")
		for _, c := range b.cfg.CapAdd {
			args = append(args, "--cap-add", c)
		}
	}
	if b.cfg.User != "" {
		args = append(args, "--user", b.cfg.User)
	}
	if b.cfg.Network != "" {
		args = append(args, "--network", b.cfg.Network)
	}
	for _, m := range b.mountArgs {
		args = append(args, "--mount", m)
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

	// Watchdog: cancel the dial loop early if the container exits, so callers
	// don't have to wait the full BootTimeout for a container that already died.
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-waitCtx.Done():
				return
			case <-t.C:
				if st, err := b.inspectState(waitCtx, containerID); err == nil && !st.Running {
					cancel()
					return
				}
			}
		}
	}()

	waitErr := waitForAgent(waitCtx, addr, 50*time.Millisecond)
	cancel()
	<-watchDone

	if waitErr != nil {
		cause := waitErr
		if st, sErr := b.inspectState(context.Background(), containerID); sErr == nil && !st.Running {
			cause = fmt.Errorf("container exited before agent became reachable (status=%s, exit_code=%d)", st.Status, st.ExitCode)
		}
		b.log.Error("[Emberbox/docker] in-container agent never came up", "container_id", shortID(containerID), "addr", addr, "error", cause)
		_ = b.stop(context.Background(), containerID)
		return nil, fmt.Errorf("agent never came up at %s: %w", addr, cause)
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
	dh, err := assertHandle[*dockerHandle](h, "Docker")
	if err != nil {
		return nil, err
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
	dh, err := assertHandle[*dockerHandle](h, "Docker")
	if err != nil {
		return err
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
// AgentPort is int-typed; if that ever changes, sanitize before splicing into
// the docker --format template (it's executed by Go's text/template).
func (b *DockerBackend) lookupPort(ctx context.Context, containerID string) (int, error) {
	tmpl := fmt.Sprintf(`{{(index (index .NetworkSettings.Ports "%d/tcp") 0).HostPort}}`, b.cfg.AgentPort)
	out, err := b.run(ctx, "inspect", "--format", tmpl, containerID)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse port %q: %w", out, err)
	}
	return port, nil
}

type dockerState struct {
	Running  bool   `json:"Running"`
	Status   string `json:"Status"`
	ExitCode int    `json:"ExitCode"`
}

// inspectState returns the container's current state. If `--rm` has already
// reaped it, returns Running=false rather than an error so callers can treat
// "gone" the same as "exited".
func (b *DockerBackend) inspectState(ctx context.Context, containerID string) (dockerState, error) {
	out, err := b.run(ctx, "inspect", "--format", "{{json .State}}", containerID)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return dockerState{Status: "gone"}, nil
		}
		return dockerState{}, err
	}
	var s dockerState
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); jerr != nil {
		return dockerState{}, fmt.Errorf("parse state %q: %w", out, jerr)
	}
	return s, nil
}

func (b *DockerBackend) stop(ctx context.Context, containerID string) error {
	stopSecs := int(b.cfg.StopTimeout.Seconds())
	if stopSecs < 1 {
		stopSecs = 1
	}
	if _, err := b.run(ctx, "stop", "--time", strconv.Itoa(stopSecs), containerID); err != nil {
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
// (containing "=") and returns the suffix expected after "type=bind,". A
// bare path with neither "=" nor ":" is rejected: a same-path bind is rarely
// what the caller meant, so we surface it instead of silently doing it.
func mountSpec(spec string) (string, error) {
	if strings.Contains(spec, "=") {
		return spec, nil
	}
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("mount %q: expected \"host:container\" or a docker --mount spec", spec)
	}
	return fmt.Sprintf("source=%s,target=%s", parts[0], parts[1]), nil
}
