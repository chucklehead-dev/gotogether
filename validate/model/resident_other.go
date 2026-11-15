//go:build !unix

package model

// peakResident is 0 where the platform reports no peak resident set.
func peakResident() int64 { return 0 }
