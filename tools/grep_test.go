package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func hasRipgrep() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

func TestGrepTool_FindsMatches(t *testing.T) {
	if !hasRipgrep() {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\nfoo bar\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nothing here\n"), 0o644))

	in, err := json.Marshal(map[string]any{"pattern": "hello", "path": dir})
	require.NoError(t, err)

	res, err := (&GrepTool{}).Execute(context.Background(), in)
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content, "a.txt")
	require.NotContains(t, res.Content, "b.txt")
}

func TestGrepTool_NoMatches(t *testing.T) {
	if !hasRipgrep() {
		t.Skip("rg not available")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644))

	in, err := json.Marshal(map[string]any{"pattern": "missing", "path": dir})
	require.NoError(t, err)

	res, err := (&GrepTool{}).Execute(context.Background(), in)
	require.NoError(t, err)
	require.False(t, res.IsError)
}

func TestGrepTool_MissingPattern(t *testing.T) {
	in, err := json.Marshal(map[string]any{"path": "."})
	require.NoError(t, err)

	res, err := (&GrepTool{}).Execute(context.Background(), in)
	require.NoError(t, err)
	require.True(t, res.IsError)
}
