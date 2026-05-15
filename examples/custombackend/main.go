// Custom backend example: plug your own sandbox.Backend into the Pool.
// This is the same seam the real FirecrackerBackend slots into — and what
// you'd implement if you wanted to drive a different isolation mechanism
// (cloud-hypervisor, kata, gVisor, a remote sandbox service, ...).
//
//	go run ./examples/custombackend
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Trystan-SA/emberbox/sandbox"
	"github.com/Trystan-SA/emberbox/tool"
	"github.com/Trystan-SA/emberbox/tools"
)

// auditBackend wraps an inner Backend and counts every call. In production
// you'd swap the inner HostBackend for a real FirecrackerBackend (or any
// other sandbox.Backend implementation).
type auditBackend struct {
	inner    sandbox.Backend
	boots    atomic.Int64
	execs    atomic.Int64
	destroys atomic.Int64
}

type auditHandle struct {
	id    string
	inner sandbox.Handle
}

func (h *auditHandle) ID() string { return h.id }

func (b *auditBackend) Name() string { return "audit(" + b.inner.Name() + ")" }

func (b *auditBackend) Boot(ctx context.Context, req sandbox.AllocRequest) (sandbox.Handle, error) {
	inner, err := b.inner.Boot(ctx, req)
	if err != nil {
		return nil, err
	}
	b.boots.Add(1)
	return &auditHandle{id: inner.ID(), inner: inner}, nil
}

func (b *auditBackend) Exec(ctx context.Context, h sandbox.Handle, toolName string, input json.RawMessage) (*sandbox.ExecResult, error) {
	ah := h.(*auditHandle)
	b.execs.Add(1)
	return b.inner.Exec(ctx, ah.inner, toolName, input)
}

func (b *auditBackend) Destroy(ctx context.Context, h sandbox.Handle) error {
	ah := h.(*auditHandle)
	b.destroys.Add(1)
	return b.inner.Destroy(ctx, ah.inner)
}

func main() {
	r := tool.NewRegistry()
	tools.RegisterDefaults(r)

	backend := &auditBackend{
		inner: sandbox.NewHostBackend(r, ""),
	}

	pool, err := sandbox.New(sandbox.Config{
		Backend:        backend,
		DefaultTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer pool.Shutdown(context.Background())

	ctx := context.Background()
	for range 3 {
		id, err := pool.Allocate(ctx, sandbox.AllocRequest{})
		if err != nil {
			panic(err)
		}
		res, err := pool.Execute(ctx, id, "bash", json.RawMessage(`{"command":"echo hi"}`))
		if err != nil {
			panic(err)
		}
		fmt.Printf("  %s: %s", id, res.Content)
		pool.Release(ctx, id)
	}

	fmt.Printf("backend=%s boots=%d execs=%d destroys=%d\n",
		backend.Name(), backend.boots.Load(), backend.execs.Load(), backend.destroys.Load())
}
