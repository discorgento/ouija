//go:build windows

package chat

import "errors"

// canRestart is false on Windows: there is no exec, so a new theme applies
// the next time ouija starts.
const canRestart = false

func restartSelf(_, _ []string) error { return errors.New("restart is not supported on Windows") }
