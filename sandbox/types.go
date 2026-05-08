// Package sandbox provides a host-side pool of microVMs (or in-process
// executors) for running emberbox/tool.Tool implementations. Use New(Config)
// to construct a Pool, Allocate to obtain a sandbox, Execute to dispatch a
// tool call, and Release when done.
package sandbox

import (
	"errors"
	"log/slog"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
)

// Mode selects how the Pool runs tools.
type Mode string

const (
	// ModeFirecracker runs each task inside a Firecracker microVM.
	ModeFirecracker Mode = "firecracker"
	// ModeLocal runs tools in the host process. NOT isolated — dev/test only.
	ModeLocal Mode = "local"
)

// Config configures a Pool.
type Config struct {
	// Mode is "local" (default) or "firecracker".
	Mode Mode
	// PoolSize is the number of pre-booted warm VMs (firecracker only).
	PoolSize int
	// KernelPath is the guest kernel image (firecracker only).
	KernelPath string
	// RootfsPath is the guest root filesystem (firecracker only).
	RootfsPath string
	// DefaultMemoryMB applies to firecracker allocations that omit MemoryMB.
	DefaultMemoryMB int
	// DefaultVCPUs applies to firecracker allocations that omit VCPUs.
	DefaultVCPUs int
	// DefaultTimeout applies to allocations that omit Timeout.
	DefaultTimeout time.Duration
	// Tools is the registry the local executor dispatches against. Required
	// when Mode == ModeLocal; ignored when Mode == ModeFirecracker (the in-VM
	// agent owns its own registry inside the guest).
	Tools *tool.Registry
	// Logger is optional. Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// AllocRequest configures a single VM allocation.
type AllocRequest struct {
	MemoryMB           int
	VCPUs              int
	Timeout            time.Duration
	NetworkAccess      bool
	ControlPlaneAccess bool
	// Env is injected into the guest (firecracker) or made available to local
	// tools via EnvFromContext (local mode). Consumers use this to pass
	// per-task secrets like API tokens.
	Env map[string]string
}

// ExecResult is the output of a tool execution.
type ExecResult struct {
	Content    string
	IsError    bool
	DurationMS int64
}

// Sentinel errors. Consumers match via errors.Is.
var (
	ErrPoolExhausted = errors.New("emberbox/sandbox: vm pool exhausted")
	ErrVMNotFound    = errors.New("emberbox/sandbox: vm not found")
	ErrVMTimeout     = errors.New("emberbox/sandbox: vm execution timed out")
)
