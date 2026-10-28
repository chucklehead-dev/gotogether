package simlog

// Break makes l break its contract in one way, for the check to catch:
// "dedupe" lands an idempotent producer's duplicates, "holes" skips a
// record at each poll, "fence" takes an evicted consumer's commits.
func Break(l *Log, what string) { l.broken = what }
