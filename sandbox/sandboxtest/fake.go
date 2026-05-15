// Package sandboxtest provides test helpers for downstream consumers of
// emberbox/sandbox. NewFake returns a Pool configured in host mode with a
// caller-supplied tool registry.
package sandboxtest

import (
	"context"
	"testing"
	"time"

	"github.com/Trystan-SA/emberbox/sandbox"
	"github.com/Trystan-SA/emberbox/tool"
)

// NewFake returns a *sandbox.Pool wired to run tools in-process. If r is nil,
// an empty registry is used (callers can register tools after the fact via
// the registry they pass in).
func NewFake(t testing.TB, r *tool.Registry) *sandbox.Pool {
	t.Helper()
	if r == nil {
		r = tool.NewRegistry()
	}
	p, err := sandbox.New(sandbox.Config{
		Mode:           sandbox.ModeHost,
		Tools:          r,
		DefaultTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("emberbox/sandboxtest: New: %v", err)
	}
	t.Cleanup(func() { p.Shutdown(context.Background()) })
	return p
}
