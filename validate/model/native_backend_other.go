//go:build !linux

package model

import (
	"context"
	"os"
)

// The current accounted tool layout and socket ownership check are Linux-only.
// Other platforms keep their ordinary uncached verification behavior.
func observeNativeBackend(context.Context, *os.Process, string, nativeBackendIdentity) func() bool {
	return func() bool { return false }
}
