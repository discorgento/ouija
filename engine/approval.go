package engine

import (
	"context"
	"sync"
	"time"
)

// ─── Approval types ─────────────────────────────────────────────────

// ApprovalResult carries the user's decision.
type ApprovalResult struct {
	Allowed       bool
	ForSession    bool          // remember for the rest of the session
	Duration      time.Duration // when > 0, session expires after this duration (e.g. 30s for bash)
	Cancelled     bool          // user pressed Cancel (different from explicit No)
	SkipExecution bool          // tool already applied externally (e.g. editor); skip execution
	Cached        bool          // served from the session cache (no prompt was shown)
}

// ApprovalCallback is the bridge between the engine and the TUI.
// It blocks until the user approves or denies the request.
// Returns Approved + ForSession on yes, or error on deny/ctx cancel.
// The input map carries the tool call arguments (path, command, pattern, etc.)
// and is used by the TUI to display a meaningful description to the user.
type ApprovalCallback func(ctx context.Context, tool string, input map[string]any) (ApprovalResult, error)

// ─── sessionEntry ───────────────────────────────────────────────────

// sessionEntry stores an approved tool with optional expiry.
// Zero expiry means permanent (until Reset).
type sessionEntry struct {
	expiresAt time.Time // zero = no expiry
}

// expired returns true if the entry has a non-zero expiry that has passed.
func (e sessionEntry) expired() bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}

// ─── ApprovalManager ────────────────────────────────────────────────

// ApprovalManager manages session-cached tool approvals.
// Supports permanent session storage and time-based windows (e.g. 30s for bash).
// Thread-safe for concurrent access from engine + TUI goroutines.
//
// The cache key is built by the caller (ToolRunner / escape approval) and is
// scoped to the *target* — e.g. "write_file\x00/abs/path" or "bash\x00<cmd>" —
// so approving one path/command never silently approves another. Keying by
// bare tool name (the old behaviour) meant a single "Session" click granted
// the tool unlimited reach for the rest of the run.
type ApprovalManager struct {
	mu       sync.Mutex
	session  map[string]sessionEntry // tool → session entry with optional expiry
	callback ApprovalCallback
}

// NewApprovalManager creates an ApprovalManager with an empty session cache.
func NewApprovalManager() *ApprovalManager {
	return &ApprovalManager{
		session: make(map[string]sessionEntry),
	}
}

// SetCallback sets the callback to invoke when user approval is needed.
// The callback is expected to block and return the user's decision.
func (am *ApprovalManager) SetCallback(cb ApprovalCallback) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.callback = cb
}

// Reset clears all session approvals.
func (am *ApprovalManager) Reset() {
	am.mu.Lock()
	defer am.mu.Unlock()
	clear(am.session) // zero in place — no alloc, no GC churn
}

// Request checks the session cache first; if not cached, invokes the
// callback to ask the user. Blocks until the user responds or ctx is done.
// The input map carries the tool call arguments and is forwarded to the callback for display.
func (am *ApprovalManager) Request(ctx context.Context, tool string, input map[string]any) (ApprovalResult, error) {
	// Fast path: check session cache.
	am.mu.Lock()
	if entry, ok := am.session[tool]; ok && !entry.expired() {
		am.mu.Unlock()
		return ApprovalResult{Allowed: true, ForSession: true, Cached: true}, nil
	}
	delete(am.session, tool)

	// Read callback under lock to avoid data race with SetCallback.
	cb := am.callback
	am.mu.Unlock()

	if cb == nil {
		// No UI to ask — deny by default (fail closed).
		return ApprovalResult{Allowed: false}, nil
	}

	result, err := cb(ctx, tool, input)
	if err != nil {
		return ApprovalResult{}, err
	}

	if result.Allowed && result.ForSession {
		am.mu.Lock()
		if result.Duration > 0 {
			am.session[tool] = sessionEntry{
				expiresAt: time.Now().Add(result.Duration),
			}
		} else {
			am.session[tool] = sessionEntry{} // permanent
		}
		am.mu.Unlock()
	}

	return result, nil
}
