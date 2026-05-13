package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"

	"github.com/Trystan-SA/emberbox/tool"
)

// GrepTool searches file contents using ripgrep.
type GrepTool struct{}

type grepInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Glob    string `json:"glob,omitempty"`
	Type    string `json:"type,omitempty"` // file type filter (e.g., "go", "py")
}

// Name returns the tool identifier.
func (t *GrepTool) Name() string { return "grep" }

// Execute searches file contents using ripgrep.
func (t *GrepTool) Execute(ctx context.Context, input json.RawMessage) (*tool.Result, error) {
	var in grepInput
	if err := parseInput(input, &in); err != nil {
		return nil, err
	}
	if res := requireField("pattern", in.Pattern); res != nil {
		return res, nil
	}

	args := []string{"--color=never", "-n", "--max-count=100"}
	if in.Glob != "" {
		args = append(args, "--glob", in.Glob)
	}
	if in.Type != "" {
		args = append(args, "--type", in.Type)
	}
	args = append(args, in.Pattern)

	searchPath := in.Path
	if searchPath == "" {
		searchPath = "."
	}
	args = append(args, searchPath)

	cmd := exec.CommandContext(ctx, "rg", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Exit code 1 means no matches (not an error).
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 {
			return &tool.Result{Content: "no matches found"}, nil
		}
		return errResult("grep error: %s", stderr.String()), nil
	}

	return &tool.Result{Content: stdout.String()}, nil
}
