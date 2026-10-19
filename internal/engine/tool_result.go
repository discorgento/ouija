package engine

import "fmt"

// ToolError is a structured error from a tool execution.
// The agent loop can inspect the Code to react differently (e.g. not retry
// on PERMISSION_DENIED) while still presenting a clear message to the model.
type ToolError struct {
	Code    string `json:"code"`    // machine-readable: "NOT_FOUND", "PERMISSION_DENIED", etc.
	Message string `json:"message"` // human-readable description
}

func (e *ToolError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// ToolResult is the structured output of a single tool call.
// It replaces the ad-hoc "(string, error)" pattern where failures were
// encoded as "ERROR: ..." text — now the agent loop can inspect Error
// and react accordingly.
type ToolResult struct {
	Data      string     `json:"data,omitempty"`      // primary output text
	Mime      string     `json:"mime,omitempty"`      // content type hint ("text/plain", "application/json")
	Truncated bool       `json:"truncated,omitempty"` // output was capped
	Error     *ToolError `json:"error,omitempty"`     // non-nil when the tool failed
}

// String formats the ToolResult as text for the model.
// On success: just the Data.
// On failure: "ERROR [Code]: Message" — same format the model is used to.
func (r ToolResult) String() string {
	if r.Error != nil {
		return "ERROR [" + r.Error.Code + "]: " + r.Error.Message
	}
	return r.Data
}

// Common tool error codes used across the engine.
const (
	ErrCodeNotFound         = "NOT_FOUND"
	ErrCodePermissionDenied = "PERMISSION_DENIED"
	ErrCodeDoomLoop         = "DOOM_LOOP"
	ErrCodeNotAllowed       = "NOT_ALLOWED"
	ErrCodeRejected         = "REJECTED"
	ErrCodeTimeout          = "TIMEOUT"
	ErrCodeInvalidInput     = "INVALID_INPUT"
	ErrCodeInternal         = "INTERNAL"
	ErrCodeSandboxEscape    = "SANDBOX_ESCAPE"
)

// toolResult helpers for common error patterns.
func errNotFound(msg string) ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeNotFound, Message: msg}}
}

func errPermissionDenied(msg string) ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodePermissionDenied, Message: msg}}
}

func errRejected(msg string) ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeRejected, Message: msg}}
}

func errInvalidInput(msg string) ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeInvalidInput, Message: msg}}
}

func errInternal(msg string) ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeInternal, Message: msg}}
}

func errSandboxEscape(msg string) ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeSandboxEscape, Message: msg}}
}
