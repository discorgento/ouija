package engine

import (
	"encoding/json"
	"sync"
)

// doomCall is a single entry in the doom-loop detector.
type doomCall struct {
	name  string
	input string // JSON-serialized input fingerprint
}

// DoomDetector tracks consecutive identical tool calls (same name + same input)
// to detect prompt-injection loops or model bugs. Thread-safe.
type DoomDetector struct {
	mu     sync.Mutex
	last   doomCall
	repeat int
}

// Check records a tool call and returns (repeat, blocked).
// repeat is how many times this identical call has been seen in a row.
// blocked is true when repeat >= 3 (the call should be rejected).
// Blocked calls are still counted so the model doesn't reset by calling again.
func (d *DoomDetector) Check(name string, input map[string]any) (repeat int, blocked bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	fp := fingerprint(input)
	if fp != "" && fp == d.last.input && name == d.last.name {
		d.repeat++
	} else {
		d.last.name = name
		d.last.input = fp
		d.repeat = 1
	}

	return d.repeat, d.repeat >= 3
}

// Reset zeroes all state. Called before each new agent run.
func (d *DoomDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.last = doomCall{}
	d.repeat = 0
}

// ResetCount zeroes the repeat counter without clearing the last call.
// Used after manual approval to let the model retry the same tool.
func (d *DoomDetector) ResetCount() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.repeat = 0
}

// fingerprint produces a stable string fingerprint of a tool-call input map
// for doom-loop detection. It is the deterministic JSON representation of the
// input, or "" when the input is nil or empty (no fingerprint = no loop check).
func fingerprint(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "" // best-effort; skip loop detection on marshal failure
	}
	return string(b)
}
