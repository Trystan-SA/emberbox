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
	if err := parseInput(input, &in); err != nil {
		return nil, err
	}
	if in.Path == "" || in.OldString == "" {
		return errResult("path and old_string are required"), nil
	}
	if in.OldString == in.NewString {
		return errResult("old_string and new_string must differ"), nil
	}

	content, err := os.ReadFile(in.Path)
	if err != nil {
		return errResult("cannot read file: %s", err), nil
	}

	text := string(content)
	count := strings.Count(text, in.OldString)

	if count == 0 {
		return errResult("old_string not found in file"), nil
	}
	if count > 1 {
		return errResult("old_string matches %d locations — provide more context to make it unique", count), nil
	}

	newText := strings.Replace(text, in.OldString, in.NewString, 1)
	if err := os.WriteFile(in.Path, []byte(newText), 0o644); err != nil { //nolint:gosec // 0644 is correct for user-editable files inside the VM
		return errResult("write error: %s", err), nil
	}

	return &tool.Result{Content: fmt.Sprintf("edited %s (1 replacement)", in.Path)}, nil
}
