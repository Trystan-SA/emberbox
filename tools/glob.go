package tools

import (
	"context"
	"encoding/json"
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
	if err := parseInput(input, &in); err != nil {
		return nil, err
	}
	if res := requireField("pattern", in.Pattern); res != nil {
		return res, nil
	}

	base := in.Path
	if base == "" {
		base = "."
	}

	fullPattern := filepath.Join(base, in.Pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return errResult("glob error: %s", err), nil
	}

	if len(matches) == 0 {
		return &tool.Result{Content: "no matches found"}, nil
	}

	return &tool.Result{Content: strings.Join(matches, "\n")}, nil
}
