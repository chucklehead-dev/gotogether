//go:build race

package raftkv

// raceBuild: the test binary has the race detector, and so does the
// raftkv-node it builds (TestRealCluster).
const raceBuild = true
