package walletgrist

import (
	"os"
	"testing"
)

// model/grist.qnt is grist's own (grist/model/grist.qnt), vendored: a
// change there is copied here, or the app is checked against guarantees
// grist no longer states.
func TestGristModelVendored(t *testing.T) {
	ours, err := os.ReadFile("model/grist.qnt")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := os.ReadFile("../../grist/model/grist.qnt")
	if err != nil {
		t.Skip("grist's checkout is not beside the example (a module fetched alone)")
	}
	if string(ours) != string(theirs) {
		t.Fatal("model/grist.qnt differs from grist/model/grist.qnt: copy it (cp ../../grist/model/grist.qnt model/)")
	}
}
