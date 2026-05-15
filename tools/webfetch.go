package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Trystan-SA/emberbox/tool"
)

// WebFetchTool fetches HTTP URLs.
type WebFetchTool struct{}

type webFetchInput struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Name returns the tool identifier.
func (t *WebFetchTool) Name() string { return "web_fetch" }

// Execute fetches the given URL and returns status + body.
func (t *WebFetchTool) Execute(ctx context.Context, input json.RawMessage) (*tool.Result, error) {
	var in webFetchInput
	if err := parseInput(input, &in); err != nil {
		return nil, err
	}
	if res := requireField("url", in.URL); res != nil {
		return res, nil
	}
	if in.Method == "" {
		in.Method = "GET"
	}

	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, http.NoBody)
	if err != nil {
		return errResult("invalid request: %s", err), nil
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return errResult("fetch error: %s", err), nil
	}
	defer func() { _ = resp.Body.Close() }()

	// Limit response to 1MB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errResult("read error: %s", err), nil
	}

	return &tool.Result{Content: fmt.Sprintf("HTTP %d\n\n%s", resp.StatusCode, string(body))}, nil
}
