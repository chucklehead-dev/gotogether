package main

import (
	"fmt"
	"math"
)

// graphMemoryBytes checks before conversion so a large MiB input cannot wrap.
func graphMemoryBytes(mib int64) (int64, error) {
	if mib <= 0 || mib > math.MaxInt64/(1<<20) {
		return 0, fmt.Errorf("-memory must be a positive MiB count no larger than %d", math.MaxInt64/(1<<20))
	}
	return mib << 20, nil
}
