package tool

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTool struct {
	name string
}

func (s *stubTool) Name() string { return s.name }
func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (*Result, error) {
	return &Result{Content: "ok"}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	tool := &stubTool{name: "bash"}
	r.Register(tool)

	got, ok := r.Get("bash")
	require.True(t, ok)
	assert.Equal(t, "bash", got.Name())
}

func TestRegistry_GetMissingTool(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	assert.False(t, ok)
}

func TestRegistry_ListReturnsAllTools(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "bash"})
	r.Register(&stubTool{name: "glob"})
	r.Register(&stubTool{name: "grep"})

	tools := r.List()
	assert.Len(t, tools, 3)
}

func TestRegistry_ListEmptyRegistry(t *testing.T) {
	r := NewRegistry()
	assert.Empty(t, r.List())
}

func TestRegistry_RegisterOverwritesExisting(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubTool{name: "bash"})
	r.Register(&stubTool{name: "bash"}) // same name, second registration wins

	assert.Len(t, r.List(), 1)
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Register(&stubTool{name: "tool"})
			_, _ = r.Get("tool")
			_ = r.List()
		}()
	}

	wg.Wait()
}
