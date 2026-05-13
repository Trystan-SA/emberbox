package tools

import (
	"encoding/json"
	"fmt"

	"github.com/Trystan-SA/emberbox/tool"
)

// parseInput unmarshals raw JSON into v, wrapping the error with the
// "invalid input: …" prefix every built-in tool returns for malformed calls.
func parseInput(input json.RawMessage, v any) error {
	if err := json.Unmarshal(input, v); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	return nil
}

// requireField returns an IsError result of the form "<name> is required"
// when value is empty, or nil when the field is present.
func requireField(name, value string) *tool.Result {
	if value == "" {
		return &tool.Result{Content: name + " is required", IsError: true}
	}
	return nil
}

// errResult builds an IsError result with a formatted message. Callers still
// return (result, nil) so the agent treats this as a tool-reported error
// rather than a transport failure.
func errResult(format string, args ...any) *tool.Result {
	return &tool.Result{Content: fmt.Sprintf(format, args...), IsError: true}
}
