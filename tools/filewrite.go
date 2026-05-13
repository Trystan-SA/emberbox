package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Trystan-SA/emberbox/tool"
)

// FileWriteTool creates or overwrites files.
type FileWriteTool struct{}

type fileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Name returns the tool identifier.
func (t *FileWriteTool) Name() string { return "file_write" }

// Execute creates or overwrites a file with the given content.
func (t *FileWriteTool) Execute(ctx context.Context, input json.RawMessage) (*tool.Result, error) {
	var in fileWriteInput
	if err := parseInput(input, &in); err != nil {
		return nil, err
	}
	if res := requireField("path", in.Path); res != nil {
		return res, nil
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(in.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // 0755 is correct for workspace directories inside the VM
		return errResult("cannot create directory: %s", err), nil
	}

	if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil { //nolint:gosec // 0644 is correct for user-editable files inside the VM
		return errResult("write error: %s", err), nil
	}

	return &tool.Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(in.Content), in.Path)}, nil
}
