// FirecrackerBackend boots a fresh Firecracker microVM per sandbox and talks
// to the in-VM emberbox-agent over vsock. The host doesn't need AF_VSOCK
// support: Firecracker exposes a Unix-domain-socket multiplexer (PUT /vsock,
// uds_path) and the agentclient layer drives it via CONNECT framing.
//
// One firecracker subprocess per microVM. Each VM gets its own working dir
// under WorkRoot containing:
//
//   - firecracker.sock — VMM API socket
//   - vsock.sock       — host UDS multiplexer (CONNECT/OK framing)
//   - vsock.sock_N     — created by firecracker for guest-initiated streams
//
// Lifecycle:
//
//  1. Boot: mkdir per-VM dir → launch firecracker subprocess →
//     wait for API socket → PUT machine-config/boot-source/drives/vsock →
//     PUT actions InstanceStart → wait for vsock CONNECT to agent port.
//  2. Exec: dial vsock UDS, CONNECT <agent_port>, round-trip agent request.
//  3. Destroy: terminate subprocess (SIGTERM, then SIGKILL on timeout),
//     remove per-VM dir.
//
// Operational requirements at runtime:
//
//   - FirecrackerBinary points to a real `firecracker` binary, and the host
//     kernel must expose /dev/kvm (Firecracker is KVM-only).
//   - KernelPath is a Firecracker-compatible vmlinux image.
//   - RootfsPath is an ext4 image containing emberbox-agent built for the
//     guest, invoked with --vsock-port=<AgentPort> (default 10000) at boot.
//
// Env injection (AllocRequest.Env) is not yet wired through to the guest. The
// docker backend uses --env; firecracker needs either MMDS or a per-VM
// cmdline append. Tracked as a follow-up; today FirecrackerBackend logs the
// env count and continues.

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// FirecrackerConfig configures a FirecrackerBackend.
type FirecrackerConfig struct {
	// FirecrackerBinary is the path to the `firecracker` binary. Default
	// "firecracker" (resolved via $PATH).
	FirecrackerBinary string
	// KernelPath is the guest kernel image (vmlinux).
	KernelPath string
	// RootfsPath is the guest root filesystem (ext4) containing
	// /usr/local/bin/emberbox-agent. Mounted read-only by default; set
	// RootfsReadWrite=true to allow guest writes (note: shared between VMs
	// when read-only, so RW + a shared image will corrupt).
	RootfsPath string
	// RootfsReadWrite, if true, mounts the rootfs read-write in the guest.
	// Default false (the same image is safe to share across many VMs).
	RootfsReadWrite bool
	// KernelBootArgs is appended to the kernel cmdline. Defaults to a quiet
	// boot suitable for Firecracker's stripped-down minirootfs convention.
	KernelBootArgs string
	// AgentPort is the vsock port the in-VM agent listens on. Default 10000.
	// The rootfs image must launch emberbox-agent with --vsock-port=<this>.
	AgentPort uint32
	// GuestCID is the vsock CID assigned to the guest. Default 3 (the
	// firecracker convention; 0/1/2 are reserved).
	GuestCID int
	// DefaultMemoryMB is used when an AllocRequest omits MemoryMB. Default 128.
	DefaultMemoryMB int
	// DefaultVCPUs is used when an AllocRequest omits VCPUs. Default 1.
	DefaultVCPUs int
	// WorkRoot is the parent directory under which per-VM working dirs are
	// created. Default os.TempDir(). Must be on a filesystem that supports
	// Unix sockets (most do; /tmp on Linux is fine).
	WorkRoot string
	// BootTimeout caps how long Boot waits for the agent to become reachable
	// over vsock. Default 30s — kernel + userspace cold boot can be slow on
	// busy hosts.
	BootTimeout time.Duration
	// StopTimeout caps how long Destroy waits for the firecracker process to
	// exit after SIGTERM before sending SIGKILL. Default 5s.
	StopTimeout time.Duration
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// FirecrackerBackend implements Backend on top of real Firecracker microVMs.
type FirecrackerBackend struct {
	cfg FirecrackerConfig
	log *slog.Logger

	// launchVMM is the subprocess launch step, isolated so tests can inject a
	// fake VMM that opens mock listeners at the expected paths. Nil = use the
	// real `firecracker --api-sock <path>` invocation.
	launchVMM func(ctx context.Context, apiPath, workDir string, logFile *os.File) (*exec.Cmd, error)
}

// NewFirecrackerBackend constructs a FirecrackerBackend. KernelPath and
// RootfsPath must be set; everything else has a sensible default.
func NewFirecrackerBackend(cfg FirecrackerConfig) *FirecrackerBackend {
	if cfg.FirecrackerBinary == "" {
		cfg.FirecrackerBinary = "firecracker"
	}
	if cfg.AgentPort == 0 {
		cfg.AgentPort = 10000
	}
	if cfg.GuestCID == 0 {
		cfg.GuestCID = 3
	}
	if cfg.DefaultMemoryMB == 0 {
		cfg.DefaultMemoryMB = 128
	}
	if cfg.DefaultVCPUs == 0 {
		cfg.DefaultVCPUs = 1
	}
	if cfg.WorkRoot == "" {
		cfg.WorkRoot = os.TempDir()
	}
	if cfg.BootTimeout == 0 {
		cfg.BootTimeout = 30 * time.Second
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 5 * time.Second
	}
	if cfg.KernelBootArgs == "" {
		// "console=ttyS0" is convenient when debugging; "reboot=k panic=1" makes
		// Firecracker tear down on guest panic instead of hanging. pci=off and
		// noapic/i8042.noaux mirror Firecracker's recommended minimal cmdline.
		cfg.KernelBootArgs = "console=ttyS0 reboot=k panic=1 pci=off i8042.noaux i8042.nomux i8042.nopnp i8042.dumbkbd"
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &FirecrackerBackend{cfg: cfg, log: log}
}

// Name implements Backend.
func (*FirecrackerBackend) Name() string { return "firecracker" }

// firecrackerHandle carries per-VM state. The Pool only uses ID() — the rest
// is for our own Exec/Destroy.
type firecrackerHandle struct {
	id       string
	workDir  string // per-VM directory; removed on Destroy
	udsPath  string // host UDS multiplexer path (vsock.sock)
	apiPath  string // firecracker API socket path
	cmd      *exec.Cmd
	logFile  *os.File // captures firecracker stdout/stderr; closed on Destroy
	memoryMB int
	vcpus    int
	env      map[string]string
	bootedAt time.Time

	stopOnce sync.Once // guards Destroy idempotency
	stopErr  error
}

func (h *firecrackerHandle) ID() string { return h.id }

// Boot implements Backend.
func (b *FirecrackerBackend) Boot(ctx context.Context, req AllocRequest) (Handle, error) {
	if err := b.validateConfig(); err != nil {
		return nil, err
	}

	id := uuid.New().String()[:12]
	workDir, err := os.MkdirTemp(b.cfg.WorkRoot, "emberbox-fc-"+id+"-")
	if err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	apiPath := filepath.Join(workDir, "firecracker.sock")
	udsPath := filepath.Join(workDir, "vsock.sock")

	mem := req.MemoryMB
	if mem == 0 {
		mem = b.cfg.DefaultMemoryMB
	}
	vcpus := req.VCPUs
	if vcpus == 0 {
		vcpus = b.cfg.DefaultVCPUs
	}

	logPath := filepath.Join(workDir, "firecracker.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("create log file: %w", err)
	}

	b.log.Info("[Emberbox/firecracker] launching VMM", "vm_id", id, "api_sock", apiPath, "memory_mb", mem, "vcpus", vcpus)
	launch := b.launchVMM
	if launch == nil {
		launch = b.realLaunchVMM
	}
	cmd, err := launch(ctx, apiPath, workDir, logFile)
	if err != nil {
		_ = logFile.Close()
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	h := &firecrackerHandle{
		id:       id,
		workDir:  workDir,
		udsPath:  udsPath,
		apiPath:  apiPath,
		cmd:      cmd,
		logFile:  logFile,
		memoryMB: mem,
		vcpus:    vcpus,
		env:      req.Env,
		bootedAt: time.Now(),
	}

	// If anything below fails, tear down the VMM and remove the work dir so
	// we don't leak processes/sockets. Use a flag rather than a defer-on-err
	// helper to avoid pulling in an extra layer.
	bootCtx, cancel := context.WithTimeout(ctx, b.cfg.BootTimeout)
	defer cancel()

	if err := b.driveBoot(bootCtx, h); err != nil {
		_ = b.destroyHandle(context.Background(), h)
		return nil, err
	}

	b.log.Info("[Emberbox/firecracker] microVM ready", "vm_id", id, "agent_port", b.cfg.AgentPort, "boot_ms", time.Since(h.bootedAt).Milliseconds())
	if len(req.Env) > 0 {
		b.log.Warn("[Emberbox/firecracker] env injection not yet wired through to the guest — values ignored", "vm_id", id, "env_count", len(req.Env))
	}
	return h, nil
}

// realLaunchVMM starts `firecracker --api-sock <apiPath>` in its own process
// group so Destroy can signal-atomically tear down the VMM and anything it
// spawned (jailer helpers, etc.). Stdout/stderr go to logFile so a failed boot
// leaves debuggable output behind in workDir.
func (b *FirecrackerBackend) realLaunchVMM(_ context.Context, apiPath, _ string, logFile *os.File) (*exec.Cmd, error) {
	// Plain exec.Command (not CommandContext): the VMM must outlive the Boot
	// context — its lifecycle is tied to Destroy, not the call that booted it.
	cmd := exec.Command(b.cfg.FirecrackerBinary, "--api-sock", apiPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// validateConfig enforces the inputs Boot can't recover from at runtime.
func (b *FirecrackerBackend) validateConfig() error {
	var missing []string
	if b.cfg.KernelPath == "" {
		missing = append(missing, "KernelPath")
	}
	if b.cfg.RootfsPath == "" {
		missing = append(missing, "RootfsPath")
	}
	if len(missing) > 0 {
		return fmt.Errorf("emberbox/sandbox: FirecrackerConfig missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// driveBoot performs the API-driven boot sequence and the readiness probe.
// Split from Boot so the error path is a single cleanup point.
func (b *FirecrackerBackend) driveBoot(ctx context.Context, h *firecrackerHandle) error {
	if err := waitForSocket(ctx, h.apiPath, 25*time.Millisecond); err != nil {
		return fmt.Errorf("firecracker API socket: %w", err)
	}

	client := newFirecrackerClient(h.apiPath)
	if err := client.SetMachineConfig(ctx, firecrackerMachineConfig{
		VcpuCount:  h.vcpus,
		MemSizeMib: h.memoryMB,
	}); err != nil {
		return fmt.Errorf("set machine-config: %w", err)
	}
	if err := client.SetBootSource(ctx, firecrackerBootSource{
		KernelImagePath: b.cfg.KernelPath,
		BootArgs:        b.cfg.KernelBootArgs,
	}); err != nil {
		return fmt.Errorf("set boot-source: %w", err)
	}
	if err := client.SetDrive(ctx, firecrackerDrive{
		DriveID:      "rootfs",
		PathOnHost:   b.cfg.RootfsPath,
		IsRootDevice: true,
		IsReadOnly:   !b.cfg.RootfsReadWrite,
	}); err != nil {
		return fmt.Errorf("set drive rootfs: %w", err)
	}
	if err := client.SetVsock(ctx, firecrackerVsock{
		GuestCID: b.cfg.GuestCID,
		UdsPath:  h.udsPath,
	}); err != nil {
		return fmt.Errorf("set vsock: %w", err)
	}
	if err := client.StartInstance(ctx); err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	if err := waitForVsockAgent(ctx, h.udsPath, b.cfg.AgentPort, 100*time.Millisecond); err != nil {
		return fmt.Errorf("in-VM agent never came up on vsock port %d: %w", b.cfg.AgentPort, err)
	}
	return nil
}

// Exec dispatches a tool call to the in-VM agent via vsock UDS.
func (b *FirecrackerBackend) Exec(ctx context.Context, h Handle, toolName string, input json.RawMessage) (*ExecResult, error) {
	fh, err := assertHandle[*firecrackerHandle](h, "Firecracker")
	if err != nil {
		return nil, err
	}
	var timeout time.Duration
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	}
	return dialAgentVsockUDS(ctx, fh.udsPath, b.cfg.AgentPort, toolName, input, timeout)
}

// Destroy implements Backend. Idempotent.
func (b *FirecrackerBackend) Destroy(ctx context.Context, h Handle) error {
	fh, err := assertHandle[*firecrackerHandle](h, "Firecracker")
	if err != nil {
		return err
	}
	return b.destroyHandle(ctx, fh)
}

// destroyHandle is the shared cleanup path used by Destroy and by Boot when a
// boot-time error needs to undo the partial VMM launch.
func (b *FirecrackerBackend) destroyHandle(ctx context.Context, h *firecrackerHandle) error {
	h.stopOnce.Do(func() {
		h.stopErr = b.terminate(ctx, h)
	})
	return h.stopErr
}

// terminate shuts the VMM down. SIGTERM first; SIGKILL after StopTimeout.
// We signal the process group (pgid was set in Boot) so any helper Firecracker
// spawns (jailer, etc.) goes with it.
func (b *FirecrackerBackend) terminate(_ context.Context, h *firecrackerHandle) error {
	defer func() {
		if h.logFile != nil {
			_ = h.logFile.Close()
		}
		if h.workDir != "" {
			if err := os.RemoveAll(h.workDir); err != nil {
				b.log.Debug("[Emberbox/firecracker] remove work dir failed", "vm_id", h.id, "dir", h.workDir, "error", err)
			}
		}
	}()

	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}

	pid := h.cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		b.log.Debug("[Emberbox/firecracker] SIGTERM failed (process likely gone)", "vm_id", h.id, "error", err)
	}

	done := make(chan error, 1)
	go func() { done <- h.cmd.Wait() }()

	select {
	case <-done:
		return nil
	case <-time.After(b.cfg.StopTimeout):
		b.log.Warn("[Emberbox/firecracker] VMM didn't exit after SIGTERM, sending SIGKILL", "vm_id", h.id, "timeout", b.cfg.StopTimeout)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
		return nil
	}
}

