package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileWriteTool_Name(t *testing.T) {
	assert.Equal(t, "file_write", (&FileWriteTool{}).Name())
}

func TestFileWriteTool_MissingPath_ReturnsError(t *testing.T) {
	tool := &FileWriteTool{}
	input, _ := json.Marshal(map[string]any{"content": "hello"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "path is required")
}

func TestFileWriteTool_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &FileWriteTool{}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	require.Error(t, err)
}

func TestFileWriteTool_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	tool := &FileWriteTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "content": "hello world"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, "11") // "hello world" is 11 bytes

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}

func TestFileWriteTool_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "file.txt")

	tool := &FileWriteTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "content": "data"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)

	_, err = os.Stat(path)
	require.NoError(t, err, "file must exist")
}

func TestFileWriteTool_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o644))

	tool := &FileWriteTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "content": "replaced"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "replaced", string(data))
}

func TestFileWriteTool_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	tool := &FileWriteTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "content": ""})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, fmt.Sprintf("wrote 0 bytes to %s", path))
}
