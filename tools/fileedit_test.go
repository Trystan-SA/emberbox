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

func TestFileEditTool_Name(t *testing.T) {
	assert.Equal(t, "file_edit", (&FileEditTool{}).Name())
}

func TestFileEditTool_MissingPath_ReturnsError(t *testing.T) {
	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{"old_string": "x", "new_string": "y"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "required")
}

func TestFileEditTool_MissingOldString_ReturnsError(t *testing.T) {
	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{"path": "/some/file", "old_string": "", "new_string": "y"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestFileEditTool_OldEqualsNew_ReturnsError(t *testing.T) {
	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{"path": "/some/file", "old_string": "abc", "new_string": "abc"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "must differ")
}

func TestFileEditTool_FileNotFound_ReturnsError(t *testing.T) {
	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{
		"path":       "/nonexistent/file.txt",
		"old_string": "foo",
		"new_string": "bar",
	})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "cannot read file")
}

func TestFileEditTool_OldStringNotFound_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello world"), 0o644))

	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "notpresent",
		"new_string": "replacement",
	})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "not found")
}

func TestFileEditTool_MultipleMatches_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, []byte("foo bar foo"), 0o644))

	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "foo",
		"new_string": "baz",
	})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "2 locations")
}

func TestFileEditTool_SuccessfulReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello world"), 0o644))

	tool := &FileEditTool{}
	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"old_string": "world",
		"new_string": "Go",
	})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, "1 replacement")

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello Go", string(content))
}

func TestFileEditTool_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &FileEditTool{}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	require.Error(t, err)
}
