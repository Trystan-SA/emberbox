package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileReadTool_Name(t *testing.T) {
	assert.Equal(t, "file_read", (&FileReadTool{}).Name())
}

func TestFileReadTool_MissingPath_ReturnsError(t *testing.T) {
	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "path is required")
}

func TestFileReadTool_FileNotFound_ReturnsError(t *testing.T) {
	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{"path": "/nonexistent/file.txt"})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "cannot open file")
}

func TestFileReadTool_InvalidJSON_ReturnsError(t *testing.T) {
	tool := &FileReadTool{}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	require.Error(t, err)
}

func TestFileReadTool_ReadsWithLineNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("line one\nline two\nline three"), 0o644))

	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{"path": path})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, "1\tline one")
	assert.Contains(t, res.Content, "2\tline two")
	assert.Contains(t, res.Content, "3\tline three")
}

func TestFileReadTool_OffsetSkipsLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\nline3"), 0o644))

	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "offset": 1})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.NotContains(t, res.Content, "line1")
	assert.Contains(t, res.Content, "2\tline2")
	assert.Contains(t, res.Content, "3\tline3")
}

func TestFileReadTool_LimitCapsOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")

	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o644))

	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "limit": 3})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	assert.Len(t, lines, 3)
}

func TestFileReadTool_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{"path": path})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, "empty")
}

func TestFileReadTool_OffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("only one line"), 0o644))

	tool := &FileReadTool{}
	input, _ := json.Marshal(map[string]any{"path": path, "offset": 100})
	res, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Contains(t, res.Content, "empty")
}
