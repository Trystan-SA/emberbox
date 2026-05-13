// Package sandbox: Backend defines the host-side abstraction over an
// isolation mechanism. The Pool drives a Backend through the lifecycle of
// each sandboxed task; concrete backends (local, firecracker, ...) live in
// their own files and are selected by Config.
package sandbox

import (
	"context"
	"encoding/json"
)

// Backend is the contract every sandbox implementation must satisfy.
// Implementations must be safe for concurrent use.
type Backend interface {
	// Name returns a stable identifier for the backend (used in logs).
	Name() string
	// Boot prepares a new sandbox. The returned Handle is opaque to the Pool;
	// backends store whatever per-sandbox state they need on it.
	Boot(ctx context.Context, req AllocRequest) (Handle, error)
	// Exec dispatches a single tool call to the sandbox identified by h.
	Exec(ctx context.Context, h Handle, toolName string, input json.RawMessage) (*ExecResult, error)
	// Destroy tears down the sandbox. Must be idempotent.
	Destroy(ctx context.Context, h Handle) error
}

// Handle is a per-sandbox token returned by Backend.Boot. The Pool only
// relies on ID() for identity and logging; backends embed any additional
// state they need on their concrete handle type.
type Handle interface {
	ID() string
}
