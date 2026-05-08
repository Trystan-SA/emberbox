// Package tool defines the shared contract between Emberbox sandbox (host)
// and agent (guest): a Tool is something that takes JSON input and produces
// a Result; a Registry is a thread-safe map of named tools.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Tool is the interface every tool implementation must satisfy.
type Tool interface {
	Name() string
	Execute(ctx context.Context, input json.RawMessage) (*Result, error)
}

// Result is the output of a tool execution.
type Result struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ErrToolNotFound is returned by Registry.Get when a tool name is not registered.
var ErrToolNotFound = errors.New("emberbox/tool: tool not found")

// Registry holds the set of tools available to a sandbox or agent. Safe for
// concurrent use.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds (or replaces) a tool. The tool's Name() is used as the key.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get returns a tool by name. Second return is false if not registered.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns a snapshot of all registered tools, in unspecified order.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}
