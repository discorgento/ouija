//go:build !unix

package app

import "os"

// foreignOwner is always false where file ownership is not a uid (Windows):
// there ouija does not check who owns a discovered config.
func foreignOwner(os.FileInfo) bool { return false }
