// Pool is the host-side orchestrator: in firecracker mode it manages a pool
// of pre-booted microVMs; in local mode it dispatches in-process.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Pool manages tool execution environments.
type Pool struct {
	cfg    Config
	mode   Mode
	log    *slog.Logger
	local  *localExecutor
	mu     sync.Mutex
	pool   []*vm
	active map[string]*vm
}

type status string

const (
	vmReady    status = "ready"
	vmRunning  status = "running"
	vmStopping status = "stopping"
)

type vm struct {
	id        string
	status    status
	cfg       AllocRequest
	bootedAt  time.Time
	agentAddr string
	env       map[string]string
}

// New constructs a Pool from cfg. Returns an error if cfg is invalid (e.g.
// local mode without a tool registry).
func New(cfg Config) (*Pool, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = ModeLocal
	}
	if mode != ModeLocal && mode != ModeFirecracker {
		return nil, fmt.Errorf("emberbox/sandbox: unknown mode %q", mode)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	p := &Pool{
		cfg:    cfg,
		mode:   mode,
		log:    log,
		active: make(map[string]*vm),
	}

	if mode == ModeLocal {
		if cfg.Tools == nil {
			return nil, errors.New("emberbox/sandbox: Config.Tools is required in local mode")
		}
		workdir, _ := os.Getwd()
		p.local = newLocalExecutor(cfg.Tools, workdir)
		log.Info("emberbox/sandbox: starting in LOCAL mode (no isolation)")
		return p, nil
	}

	log.Info("emberbox/sandbox: starting in FIRECRACKER mode", "pool_size", cfg.PoolSize)
	for i := 0; i < cfg.PoolSize; i++ {
		v, err := p.bootVM(context.Background(), &AllocRequest{
			MemoryMB: cfg.DefaultMemoryMB,
			VCPUs:    cfg.DefaultVCPUs,
		})
		if err != nil {
			log.Warn("emberbox/sandbox: pre-boot failed", "index", i, "error", err)
			continue
		}
		p.pool = append(p.pool, v)
	}
	log.Info("emberbox/sandbox: pool ready", "available", len(p.pool))
	return p, nil
}

// Allocate assigns a sandbox from the warm pool or boots a fresh one.
// In local mode, returns an opaque ID immediately.
func (p *Pool) Allocate(ctx context.Context, req AllocRequest) (string, error) {
	if req.Timeout == 0 {
		req.Timeout = p.cfg.DefaultTimeout
	}

	if p.mode == ModeLocal {
		id := "local-" + uuid.New().String()[:8]
		p.mu.Lock()
		p.active[id] = &vm{
			id:       id,
			status:   vmRunning,
			cfg:      req,
			bootedAt: time.Now(),
			env:      req.Env,
		}
		p.mu.Unlock()
		return id, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if req.MemoryMB == 0 {
		req.MemoryMB = p.cfg.DefaultMemoryMB
	}
	if req.VCPUs == 0 {
		req.VCPUs = p.cfg.DefaultVCPUs
	}

	if len(p.pool) > 0 {
		v := p.pool[len(p.pool)-1]
		p.pool = p.pool[:len(p.pool)-1]
		v.status = vmRunning
		v.cfg = req
		v.env = req.Env
		p.active[v.id] = v
		go p.replenishPool()
		p.log.Debug("emberbox/sandbox: allocated VM from pool", "vm_id", v.id)
		return v.id, nil
	}

	p.log.Warn("emberbox/sandbox: pool exhausted, booting on-demand")
	v, err := p.bootVM(ctx, &req)
	if err != nil {
		return "", fmt.Errorf("emberbox/sandbox: boot vm: %w", err)
	}
	v.status = vmRunning
	p.active[v.id] = v
	return v.id, nil
}

// Execute dispatches a tool call to the named sandbox.
func (p *Pool) Execute(ctx context.Context, vmID, toolName string, input json.RawMessage) (*ExecResult, error) {
	p.mu.Lock()
	v, ok := p.active[vmID]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("emberbox/sandbox: %w: %s", ErrVMNotFound, vmID)
	}

	execCtx, cancel := context.WithTimeout(ctx, v.cfg.Timeout)
	defer cancel()

	if p.mode == ModeLocal {
		return p.local.Execute(execCtx, toolName, input, v.env)
	}

	if len(v.env) > 0 {
		p.log.Debug("emberbox/sandbox: env injection deferred (firecracker mode)", "vm_id", v.id, "env_count", len(v.env))
	}
	start := time.Now()
	result, err := p.callAgent(execCtx, v, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("emberbox/sandbox: agent call: %w", err)
	}
	result.DurationMS = time.Since(start).Milliseconds()
	return result, nil
}

// Release tears down or returns to pool. Idempotent.
func (p *Pool) Release(ctx context.Context, vmID string) {
	p.mu.Lock()
	v, ok := p.active[vmID]
	if ok {
		delete(p.active, vmID)
	}
	p.mu.Unlock()

	if !ok {
		return
	}

	if p.mode == ModeLocal {
		p.log.Debug("emberbox/sandbox: released local executor", "vm_id", vmID)
		return
	}

	v.status = vmStopping
	if err := p.destroyVM(ctx, v); err != nil {
		p.log.Error("emberbox/sandbox: destroy vm", "vm_id", vmID, "error", err)
	}
	p.log.Debug("emberbox/sandbox: released vm", "vm_id", vmID)
}

// Shutdown stops all active VMs and drains the pool.
func (p *Pool) Shutdown(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mode == ModeLocal {
		p.active = make(map[string]*vm)
		p.log.Info("emberbox/sandbox: local pool shut down")
		return
	}

	for id, v := range p.active {
		if err := p.destroyVM(ctx, v); err != nil {
			p.log.Error("emberbox/sandbox: shutdown destroy", "vm_id", id, "error", err)
		}
	}
	for _, v := range p.pool {
		if err := p.destroyVM(ctx, v); err != nil {
			p.log.Error("emberbox/sandbox: shutdown destroy pooled", "vm_id", v.id, "error", err)
		}
	}
	p.active = make(map[string]*vm)
	p.pool = nil
	p.log.Info("emberbox/sandbox: pool shut down")
}

// Status returns the current pool/active counts.
func (p *Pool) Status() (poolSize, activeCount int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pool), len(p.active)
}

// DefaultTimeout returns the configured per-task default.
func (p *Pool) DefaultTimeout() time.Duration {
	return p.cfg.DefaultTimeout
}

// --- Firecracker internals (only used when mode == ModeFirecracker) ---

func (p *Pool) bootVM(ctx context.Context, req *AllocRequest) (*vm, error) {
	id := uuid.New().String()[:12]
	p.log.Debug("emberbox/sandbox: booting vm", "vm_id", id, "memory_mb", req.MemoryMB, "vcpus", req.VCPUs)

	v := &vm{
		id:        id,
		status:    vmReady,
		cfg:       *req,
		bootedAt:  time.Now(),
		agentAddr: fmt.Sprintf("vsock://%s:%d", id, 10000),
		env:       req.Env,
	}
	// Firecracker SDK calls land here in a follow-up.
	return v, nil
}

func (p *Pool) destroyVM(ctx context.Context, v *vm) error {
	p.log.Debug("emberbox/sandbox: destroying vm", "vm_id", v.id)
	// Firecracker shutdown lands here in a follow-up.
	return nil
}

func (p *Pool) callAgent(ctx context.Context, v *vm, toolName string, input json.RawMessage) (*ExecResult, error) {
	return nil, errors.New("emberbox/sandbox: firecracker agent communication not yet implemented")
}

func (p *Pool) replenishPool() {
	p.mu.Lock()
	needed := p.cfg.PoolSize - len(p.pool)
	p.mu.Unlock()

	for i := 0; i < needed; i++ {
		v, err := p.bootVM(context.Background(), &AllocRequest{
			MemoryMB: p.cfg.DefaultMemoryMB,
			VCPUs:    p.cfg.DefaultVCPUs,
		})
		if err != nil {
			p.log.Warn("emberbox/sandbox: replenish failed", "error", err)
			return
		}
		p.mu.Lock()
		p.pool = append(p.pool, v)
		p.mu.Unlock()
	}
}
