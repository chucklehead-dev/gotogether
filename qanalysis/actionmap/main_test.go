package actionmap_test

import (
	"os"
	"testing"
)

// The fixtures are modules of their own (their replace points at this
// checkout), outside the repository's go.work: in the workspace the go
// command would refuse them.
func TestMain(m *testing.M) {
	os.Setenv("GOWORK", "off")
	os.Exit(m.Run())
}
