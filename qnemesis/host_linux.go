package qnemesis

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"
)

// cpuStats reads, on Linux, why a probe's thread was late: the time it
// waited on a run queue (its schedstat's run_delay: a busy machine) and
// the time its cgroup was throttled (cgroup v2's cpu.stat throttled_usec:
// a CPU quota spent). Neither moving while a tick was late says the
// whole host was held (a VM's steal, a frozen process). Either file
// missing (a kernel without schedstats, cgroup v1) leaves its reading
// out.
type cpuStats struct {
	sched, cgroup     bool
	cpuStat           string
	delay, throttling time.Duration
}

func newCPUStats() *cpuStats {
	c := &cpuStats{}
	if b, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if path, ok := strings.CutPrefix(line, "0::"); ok {
				c.cpuStat = "/sys/fs/cgroup" + path + "/cpu.stat"
			}
		}
	}
	c.delay, c.sched = runDelay()
	c.throttling, c.cgroup = c.throttled()
	return c
}

// delta is how long the thread waited for a CPU and the cgroup was
// throttled since the last call. It must be called on the thread
// newCPUStats was made on (the probe locks it).
func (c *cpuStats) delta() (waited, throttled time.Duration) {
	if d, ok := runDelay(); ok && c.sched {
		waited, c.delay = d-c.delay, d
	}
	if t, ok := c.throttled(); ok && c.cgroup {
		throttled, c.throttling = t-c.throttling, t
	}
	return waited, throttled
}

// runDelay is the calling thread's total wait on a run queue.
func runDelay() (time.Duration, bool) {
	b, err := os.ReadFile("/proc/thread-self/schedstat")
	if err != nil {
		return 0, false
	}
	f := bytes.Fields(b)
	if len(f) < 2 {
		return 0, false
	}
	ns, err := strconv.ParseInt(string(f[1]), 10, 64)
	return time.Duration(ns), err == nil
}

func (c *cpuStats) throttled() (time.Duration, bool) {
	if c.cpuStat == "" {
		return 0, false
	}
	b, err := os.ReadFile(c.cpuStat)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "throttled_usec "); ok {
			us, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return time.Duration(us) * time.Microsecond, err == nil
		}
	}
	return 0, false
}
