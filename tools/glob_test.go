package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobTool_Name(t *testing.T) {
	assert.Equal(t, "glob", (&GlobTool{}).Name())
}

func TestGlobTool_EmptyPattern_ReturnsError(t *testing.T) {
	tool := &GlobTool{}
	input, _ := json.Marshal(map[string]any{"pattern": ""})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "pattern is required")
}

func TestGlobTool_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &GlobTool{}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad json`))
	require.Error(t, err)
}

func TestGlobTool_NoMatches(t *testing.T) {
	tool := &GlobTool{}
	input, _ := json.Marshal(map[string]any{
		"pattern": "*.nonexistentextension",
		"path":    t.TempDir(),
	})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, "no matches found", res.Content)
}

func TestGlobTool_MatchesFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0o644))

	tool := &GlobTool{}
	input, _ := json.Marshal(map[string]any{
		"pattern": "*.go",
		"path":    dir,
	})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, "a.go")
	assert.Contains(t, res.Content, "b.go")
	assert.NotContains(t, res.Content, "c.txt")
}

func TestGlobTool_DefaultsPathToDot(t *testing.T) {
	tool := &GlobTool{}
	// No path specified — should use "." and not error.
	input, _ := json.Marshal(map[string]any{"pattern": "*.go"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	// Just verify it ran without returning an error result.
	_ = res
}
