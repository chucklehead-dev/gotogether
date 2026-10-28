package grist

import (
	_ "embed"

	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
)

// gristQnt is grist/model/grist.qnt, grist's Quint library, which the
// generator vendors into an application's model/ (read-only).
//
//go:embed model/grist.qnt
var gristQnt []byte

// The generator (grist/internal/gen) cannot embed a file of a parent
// directory, and whoever calls it holds an App, so has imported grist.
func init() { gen.Library = gristQnt }
