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
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	if in.URL == "" {
		return &tool.Result{Content: "url is required", IsError: true}, nil
	}
	if in.Method == "" {
		in.Method = "GET"
	}

	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, http.NoBody)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("invalid request: %s", err), IsError: true}, nil
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("fetch error: %s", err), IsError: true}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	// Limit response to 1MB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("read error: %s", err), IsError: true}, nil
	}

	return &tool.Result{Content: fmt.Sprintf("HTTP %d\n\n%s", resp.StatusCode, string(body))}, nil
}
