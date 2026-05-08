package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Trystan-SA/emberbox/tool"
)

// GlobTool finds files matching a glob pattern.
type GlobTool struct{}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

// Name returns the tool identifier.
func (t *GlobTool) Name() string { return "glob" }

// Execute finds files matching a glob pattern.
func (t *GlobTool) Execute(ctx context.Context, input json.RawMessage) (*tool.Result, error) {
	var in globInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if in.Pattern == "" {
		return &tool.Result{Content: "pattern is required", IsError: true}, nil
	}

	base := in.Path
	if base == "" {
		base = "."
	}

	fullPattern := filepath.Join(base, in.Pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("glob error: %s", err), IsError: true}, nil
	}

	if len(matches) == 0 {
		return &tool.Result{Content: "no matches found"}, nil
	}

	return &tool.Result{Content: strings.Join(matches, "\n")}, nil
}
