package chat

import (
	"context"

	"github.com/discorgento/ouija/internal/engine"
)

// ProviderFactory builds a provider by name (e.g. "anthropic", "openai").
// Injected by the entrypoint so the TUI never reaches into package app —
// the dependency arrow goes app → chat, never chat → app.
type ProviderFactory func(name string) (engine.Provider, error)

// EngineAPI is the slice of *engine.Engine the TUI is allowed to touch.
// Everything else (permission wiring, approval manager, token/tool callbacks,
// logger) is set once by Run on the concrete engine, so the UI cannot
// reconfigure gating at runtime by accident — and a fake can drive the UI in
// tests without an engine.
type EngineAPI interface {
	// Chat runs one turn, continuing the conversation.
	Chat(ctx context.Context, req engine.RunRequest) (string, []engine.Message, error)
	// Config exposes the active configuration (model, provider, root…).
	Config() *engine.Config
	// Spent reports cumulative (tokens, steps, usd) for the footer.
	Spent() (tokens, steps int, usd float64)
	// Tracer exposes the turn tracer for the trace block.
	Tracer() *engine.Tracer
	// PermissionConfig returns the live permission mapping (/permit).
	PermissionConfig() *engine.PermissionConfig
	// ResetApprovals drops the session approval cache (/clear).
	ResetApprovals()
	// SetModel / SetProvider switch the active model/provider (/model, /provider).
	SetModel(name string)
	SetProvider(p engine.Provider)
	// SaveConfigAt persists config changes back to disk.
	SaveConfigAt(path string) error
}
