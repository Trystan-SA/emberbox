package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Trystan-SA/emberbox/tool"
)

// FileEditTool performs exact string replacement in files.
type FileEditTool struct{}

type fileEditInput struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// Name returns the tool identifier.
func (t *FileEditTool) Name() string { return "file_edit" }

// Execute performs an exact string replacement in a file.
func (t *FileEditTool) Execute(ctx context.Context, input json.RawMessage) (*tool.Result, error) {
	var in fileEditInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if in.Path == "" || in.OldString == "" {
		return &tool.Result{Content: "path and old_string are required", IsError: true}, nil
	}
	if in.OldString == in.NewString {
		return &tool.Result{Content: "old_string and new_string must differ", IsError: true}, nil
	}

	content, err := os.ReadFile(in.Path)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("cannot read file: %s", err), IsError: true}, nil
	}

	text := string(content)
	count := strings.Count(text, in.OldString)

	if count == 0 {
		return &tool.Result{Content: "old_string not found in file", IsError: true}, nil
	}
	if count > 1 {
		return &tool.Result{
			Content: fmt.Sprintf("old_string matches %d locations — provide more context to make it unique", count),
			IsError: true,
		}, nil
	}

	newText := strings.Replace(text, in.OldString, in.NewString, 1)
	if err := os.WriteFile(in.Path, []byte(newText), 0o644); err != nil { //nolint:gosec // 0644 is correct for user-editable files inside the VM
		return &tool.Result{Content: fmt.Sprintf("write error: %s", err), IsError: true}, nil
	}

	return &tool.Result{Content: fmt.Sprintf("edited %s (1 replacement)", in.Path)}, nil
}
