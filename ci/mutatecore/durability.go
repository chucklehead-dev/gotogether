package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// Sync every ancestor, including the parent containing a newly created
// directory's name. Syncing only the leaf cannot preserve its ancestry after
// power loss. Existing ancestors are included so MkdirAll/MkdirTemp callers do
// not need to guess which component was created by a concurrent mkdir.
func syncDirectoryAncestry(path string, syncDir func(string) error) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		if err := syncDir(path); err != nil {
			return fmt.Errorf("sync directory ancestry %s: %w", path, err)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func durableMkdirAll(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return syncDirectoryAncestry(path, syncDirectory)
}
