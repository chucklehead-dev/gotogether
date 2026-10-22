//go:build sim

// The checkout saga of ../../checkout, deployed unchanged (checkout.Deploy)
// on qenv's native runtime in gosim: its services' tasks are goroutines,
// its processes gosim machines that crash with all their goroutines, its
// time gosim's, and every choice the host's Chooser (see host_test.go).
package checkout_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/checkout"
	"github.com/chucklehead-dev/gotogether/examples/checkout/shop"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qgosim"
)

func env(key string) int {
	n, _ := strconv.Atoi(os.Getenv(key))
	return n
}

func TestCheckout(t *testing.T) {
	cfg := checkout.Config{Isolation: qenv.Isolation(env("CHECKOUT_ISOLATION")), Mutation: shop.Mutation(env("CHECKOUT_MUTATION"))}
	if _, err := checkout.RunOn(qgosim.New(), cfg); err != nil {
		t.Fatal(err)
	}
}
