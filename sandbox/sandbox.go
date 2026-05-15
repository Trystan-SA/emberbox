// Pool is the host-side orchestrator. It owns a warm pool of pre-booted
// sandboxes and dispatches Allocate/Execute/Release calls to a Backend.

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
)

// Pool manages tool execution environments via a Backend.
type Pool struct {
	cfg     Config
	backend Backend
	log     *slog.Logger
	mu      sync.Mutex
	warm    []Handle
	active  map[string]*allocation
}

// allocation pairs a Backend Handle with the AllocRequest it was booted for.
type allocation struct {
	handle Handle
	req    AllocRequest
}

// New constructs a Pool from cfg. If cfg.Backend is set it's used as-is;
// otherwise cfg.Mode (default ModeHost) selects a built-in backend.
func New(cfg Config) (*Pool, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	backend, err := selectBackend(cfg, log)
	if err != nil {
		return nil, err
	}

	p := &Pool{
		cfg:     cfg,
		backend: backend,
		log:     log,
		active:  make(map[string]*allocation),
	}

	log.Info("[Emberbox] pool starting", "backend", backend.Name(), "warm_pool_size", cfg.PoolSize)

	for i := range cfg.PoolSize {
		h, err := backend.Boot(context.Background(), AllocRequest{
			MemoryMB: cfg.DefaultMemoryMB,
			VCPUs:    cfg.DefaultVCPUs,
		})
		if err != nil {
			log.Warn("[Emberbox] warm-pool pre-boot failed", "index", i, "backend", backend.Name(), "error", err)
			continue
		}
		p.warm = append(p.warm, h)
	}
	if cfg.PoolSize > 0 {
		log.Info("[Emberbox] warm pool ready", "available", len(p.warm), "requested", cfg.PoolSize)
	}
	return p, nil
}

func selectBackend(cfg Config, log *slog.Logger) (Backend, error) {
	if cfg.Backend != nil {
		return cfg.Backend, nil
	}
	mode := cfg.Mode
	if mode == "" {
		mode = ModeHost
	}
	switch mode {
	case ModeHost:
		if cfg.Tools == nil {
			return nil, errors.New("emberbox/sandbox: Config.Tools is required when using the default HostBackend")
		}
		workdir, _ := os.Getwd()
		return NewHostBackend(cfg.Tools, workdir), nil
	case ModeFirecracker:
		return NewFirecrackerBackend(FirecrackerConfig{
			KernelPath:      cfg.KernelPath,
			RootfsPath:      cfg.RootfsPath,
			DefaultMemoryMB: cfg.DefaultMemoryMB,
			DefaultVCPUs:    cfg.DefaultVCPUs,
			Logger:          log,
		}), nil
	default:
		return nil, fmt.Errorf("emberbox/sandbox: unknown mode %q", mode)
	}
}

// Backend returns the Backend driving this Pool. Useful for tests.
func (p *Pool) Backend() Backend { return p.backend }

// Allocate assigns a sandbox from the warm pool or boots a fresh one.
func (p *Pool) Allocate(ctx context.Context, req AllocRequest) (string, error) {
	if req.Timeout == 0 {
		req.Timeout = p.cfg.DefaultTimeout
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = p.cfg.DefaultMemoryMB
	}
	if req.VCPUs == 0 {
		req.VCPUs = p.cfg.DefaultVCPUs
	}

	p.mu.Lock()
	if len(p.warm) > 0 {
		h := p.warm[len(p.warm)-1]
		p.warm = p.warm[:len(p.warm)-1]
		p.active[h.ID()] = &allocation{handle: h, req: req}
		p.mu.Unlock()
		go p.replenishWarmPool()
		p.log.Debug("[Emberbox] sandbox allocated from warm pool", "id", h.ID(), "backend", p.backend.Name())
		return h.ID(), nil
	}
	p.mu.Unlock()

	h, err := p.backend.Boot(ctx, req)
	if err != nil {
		return "", fmt.Errorf("emberbox/sandbox: boot: %w", err)
	}
	p.mu.Lock()
	p.active[h.ID()] = &allocation{handle: h, req: req}
	p.mu.Unlock()
	p.log.Debug("[Emberbox] sandbox allocated (fresh boot)", "id", h.ID(), "backend", p.backend.Name())
	return h.ID(), nil
}

// Execute dispatches a tool call to the named sandbox.
func (p *Pool) Execute(ctx context.Context, id, toolName string, input json.RawMessage) (*ExecResult, error) {
	p.mu.Lock()
	a, ok := p.active[id]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("emberbox/sandbox: %w: %s", ErrVMNotFound, id)
	}

	execCtx := ctx
	if a.req.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, a.req.Timeout)
		defer cancel()
	}

	start := time.Now()
	result, err := p.backend.Exec(execCtx, a.handle, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("emberbox/sandbox: exec: %w", err)
	}
	if result.DurationMS == 0 {
		result.DurationMS = time.Since(start).Milliseconds()
	}
	return result, nil
}

// Release tears down or returns to pool. Idempotent.
func (p *Pool) Release(ctx context.Context, id string) {
	p.mu.Lock()
	a, ok := p.active[id]
	if ok {
		delete(p.active, id)
	}
	p.mu.Unlock()

	if !ok {
		return
	}
	if err := p.backend.Destroy(ctx, a.handle); err != nil {
		p.log.Error("[Emberbox] sandbox destroy failed", "id", id, "backend", p.backend.Name(), "error", err)
	}
	p.log.Debug("[Emberbox] sandbox released", "id", id, "backend", p.backend.Name())
}

// Shutdown stops all active sandboxes and drains the warm pool.
func (p *Pool) Shutdown(ctx context.Context) {
	p.mu.Lock()
	active := p.active
	warm := p.warm
	p.active = make(map[string]*allocation)
	p.warm = nil
	p.mu.Unlock()

	for id, a := range active {
		if err := p.backend.Destroy(ctx, a.handle); err != nil {
			p.log.Error("[Emberbox] shutdown: destroy failed (active sandbox)", "id", id, "backend", p.backend.Name(), "error", err)
		}
	}
	for _, h := range warm {
		if err := p.backend.Destroy(ctx, h); err != nil {
			p.log.Error("[Emberbox] shutdown: destroy failed (warm pool entry)", "id", h.ID(), "backend", p.backend.Name(), "error", err)
		}
	}
	p.log.Info("[Emberbox] pool shut down", "backend", p.backend.Name())
}

// Status returns the current warm-pool size and active count.
func (p *Pool) Status() (warmSize, activeCount int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.warm), len(p.active)
}

// DefaultTimeout returns the configured per-task default.
func (p *Pool) DefaultTimeout() time.Duration {
	return p.cfg.DefaultTimeout
}

func (p *Pool) replenishWarmPool() {
	p.mu.Lock()
	needed := p.cfg.PoolSize - len(p.warm)
	p.mu.Unlock()

	for range needed {
		h, err := p.backend.Boot(context.Background(), AllocRequest{
			MemoryMB: p.cfg.DefaultMemoryMB,
			VCPUs:    p.cfg.DefaultVCPUs,
		})
		if err != nil {
			p.log.Warn("[Emberbox] warm-pool replenish failed", "backend", p.backend.Name(), "error", err)
			return
		}
		p.mu.Lock()
		p.warm = append(p.warm, h)
		p.mu.Unlock()
	}
}
