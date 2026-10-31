// Package atomicdir publishes a complete staged directory without replacing
// existing evidence. Staging and destination must have the same parent.
package atomicdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Publish renames stage to dst atomically, refusing any existing destination,
// including an empty directory or a symlink. It leaves stage intact on error.
// The caller owns staging, verification and cleanup; Publish does not claim
// that the contents are durable after a machine or filesystem failure.
func Publish(stage, dst string) error {
	from, err := filepath.Abs(stage)
	if err != nil {
		return err
	}
	to, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if from == to || filepath.Dir(from) != filepath.Dir(to) {
		return fmt.Errorf("publish directory: staging and destination must be distinct siblings")
	}
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("publish directory: staging %s is not a directory", from)
	}
	if err := renameExclusive(from, to); err != nil {
		return fmt.Errorf("publish directory %s: %w", to, err)
	}
	return nil
}
