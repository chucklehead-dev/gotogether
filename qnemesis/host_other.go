//go:build !linux

package qnemesis

import "time"

// cpuStats reads nothing outside Linux: a late tick is a stall with no
// reading of why.
type cpuStats struct{ sched, cgroup bool }

func newCPUStats() *cpuStats { return &cpuStats{} }

func (c *cpuStats) delta() (waited, throttled time.Duration) { return 0, 0 }
