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

// Mode selects the default Backend when Config.Backend is nil.
type Mode string

const (
	// ModeFirecracker selects the default FirecrackerBackend.
	ModeFirecracker Mode = "firecracker"
	// ModeHost selects the default HostBackend. NOT isolated — uses the host
	// process directly. For users who already have their environment set up
	// locally and explicitly opt out of isolation.
	ModeHost Mode = "host"
)

// Config configures a Pool.
type Config struct {
	// Backend, if non-nil, drives the Pool. Lets callers plug in their own
	// isolation backend (e.g. a third-party Firecracker, cloud-hypervisor,
	// or kata implementation). When nil, Mode picks one of the built-ins.
	Backend Backend
	// Mode picks a default Backend when Backend is nil. Defaults to ModeHost.
	Mode Mode
	// PoolSize is the number of sandboxes to pre-boot at startup. Backends for
	// which Boot is expensive (firecracker) benefit; HostBackend ignores it in
	// practice (Boot is constant-time).
	PoolSize int
	// KernelPath is the guest kernel image. Used only when Backend is nil and
	// Mode == ModeFirecracker.
	KernelPath string
	// RootfsPath is the guest root filesystem. Used only when Backend is nil
	// and Mode == ModeFirecracker.
	RootfsPath string
	// DefaultMemoryMB applies to allocations that omit MemoryMB.
	DefaultMemoryMB int
	// DefaultVCPUs applies to allocations that omit VCPUs.
	DefaultVCPUs int
	// DefaultTimeout applies to allocations that omit Timeout.
	DefaultTimeout time.Duration
	// Tools is the registry the default HostBackend dispatches against.
	// Required when Backend is nil and Mode == ModeHost; ignored otherwise
	// (in firecracker mode the in-VM agent owns its own registry).
	Tools *tool.Registry
	// Logger is optional. Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// AllocRequest configures a single sandbox allocation.
type AllocRequest struct {
	MemoryMB           int
	VCPUs              int
	Timeout            time.Duration
	NetworkAccess      bool
	ControlPlaneAccess bool
	// Env carries per-allocation environment variables. Delivery depends on
	// the backend: HostBackend makes them available to in-process tools via
	// EnvFromContext; FirecrackerBackend logs the count and currently ignores
	// the values (guest-side injection via MMDS/cmdline isn't wired through
	// yet). Consumers use this to pass per-task secrets like API tokens.
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
