//go:build !linux && !darwin && !windows

package atomicdir

import "fmt"

func renameExclusive(from, to string) error {
	return fmt.Errorf("atomic directory publication without replacement is unsupported on this platform")
}
