// FirecrackerBackend is the seam where real Firecracker microVMs slot in.
// Today it returns booted-VM handles but does not actually launch a VMM and
// cannot communicate with an in-VM agent. A follow-up replaces the stubbed
// methods with firecracker-go-sdk + vsock calls.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// FirecrackerConfig configures a FirecrackerBackend.
type FirecrackerConfig struct {
	// KernelPath is the guest kernel image.
	KernelPath string
	// RootfsPath is the guest root filesystem (must contain emberbox-agent).
	RootfsPath string
	// DefaultMemoryMB is used when an AllocRequest omits MemoryMB.
	DefaultMemoryMB int
	// DefaultVCPUs is used when an AllocRequest omits VCPUs.
	DefaultVCPUs int
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
}

// FirecrackerBackend boots a fresh microVM per sandbox and communicates with
// the in-VM agent over vsock. STUB: implementations of bootVMM/dialAgent/
// stopVMM are not yet wired up — Boot returns a handle with metadata, Exec
// returns ErrFirecrackerNotImplemented.
type FirecrackerBackend struct {
	cfg FirecrackerConfig
	log *slog.Logger
}

// NewFirecrackerBackend constructs a FirecrackerBackend from cfg.
func NewFirecrackerBackend(cfg FirecrackerConfig) *FirecrackerBackend {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &FirecrackerBackend{cfg: cfg, log: log}
}

// Name implements Backend.
func (*FirecrackerBackend) Name() string { return "firecracker" }

// firecrackerHandle carries per-VM metadata.
type firecrackerHandle struct {
	id        string
	agentAddr string
	env       map[string]string
	memoryMB  int
	vcpus     int
	bootedAt  time.Time
}

func (h *firecrackerHandle) ID() string { return h.id }

// ErrFirecrackerNotImplemented is returned by FirecrackerBackend.Exec until
// the firecracker-go-sdk integration lands.
var ErrFirecrackerNotImplemented = errors.New("emberbox/sandbox: firecracker agent communication not yet implemented")

// Boot implements Backend. Today it constructs a handle without launching a
// VMM. The real impl will call firecracker-go-sdk here and wait for the
// agent's vsock listener to be ready.
func (b *FirecrackerBackend) Boot(_ context.Context, req AllocRequest) (Handle, error) {
	id := uuid.New().String()[:12]
	mem := req.MemoryMB
	if mem == 0 {
		mem = b.cfg.DefaultMemoryMB
	}
	vcpus := req.VCPUs
	if vcpus == 0 {
		vcpus = b.cfg.DefaultVCPUs
	}
	b.log.Info("[Emberbox/firecracker] booting microVM (STUB — no real VMM launched)", "vm_id", id, "memory_mb", mem, "vcpus", vcpus)
	return &firecrackerHandle{
		id:        id,
		agentAddr: fmt.Sprintf("vsock://%s:%d", id, 10000),
		env:       req.Env,
		memoryMB:  mem,
		vcpus:     vcpus,
		bootedAt:  time.Now(),
	}, nil
}

// Exec implements Backend.
func (b *FirecrackerBackend) Exec(_ context.Context, h Handle, _ string, _ json.RawMessage) (*ExecResult, error) {
	fh, ok := h.(*firecrackerHandle)
	if !ok {
		return nil, fmt.Errorf("emberbox/sandbox: FirecrackerBackend got handle of type %T", h)
	}
	if len(fh.env) > 0 {
		b.log.Debug("[Emberbox/firecracker] env injection deferred (stub)", "vm_id", fh.id, "env_count", len(fh.env))
	}
	return nil, ErrFirecrackerNotImplemented
}

// Destroy implements Backend.
func (b *FirecrackerBackend) Destroy(_ context.Context, h Handle) error {
	b.log.Debug("[Emberbox/firecracker] destroying microVM (stub)", "vm_id", h.ID())
	return nil
}
