package formats_test

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/qdriver"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Every format's exported name is the table's: one scheme, so a bump in
// one place is a bump everywhere.
func TestNamesAreTheTables(t *testing.T) {
	for _, c := range []struct {
		name string
		f    formats.Format
	}{
		{qtrace.StepsFormat, formats.Steps},
		{qdriver.Protocol, formats.Requests},
		{binding.FormatName, formats.Binding},
		{convert.FormatName, formats.Convert},
		{validate.ManifestFormat, formats.Manifest},
		{evidence.Format, formats.Evidence},
		{validate.NavProtocol, formats.Nav},
		{formats.Choices.At(qenv.BytesVersion), formats.Choices},
	} {
		if c.name != c.f.Name() {
			t.Errorf("%s is named %s by its package", c.f.Name(), c.name)
		}
	}
	seen := map[string]bool{}
	for _, f := range formats.All() {
		if seen[f.Thing] {
			t.Errorf("two formats named %s", f.Thing)
		}
		seen[f.Thing] = true
		if f.Oldest > f.Major || f.Oldest < 1 || f.What == "" {
			t.Errorf("%s: reads %d to %d, %q", f.Thing, f.Oldest, f.Major, f.What)
		}
		if f.Oldest > 1 && f.Older == "" {
			t.Errorf("%s reads no major below %d, and says no fix for one", f.Thing, f.Oldest)
		}
	}
}

// A later major is refused with the upgrade, an older unread one with
// how to write it anew, another format with what was wanted.
func TestCheck(t *testing.T) {
	f := formats.Format{Thing: "x", Major: 3, Oldest: 2, What: "x file", Older: "write it again", Aliases: map[string]int{"gotogether.old/1": 2}}
	for _, c := range []struct {
		name, err string
		major     int
	}{
		{"gotogether.x/3", "", 3},
		{"gotogether.x/2", "", 2},
		{"gotogether.old/1", "", 2},
		{"gotogether.x/4", "gotogether.x/4 is a later version than this gotogether reads (gotogether.x/2 to gotogether.x/3): upgrade gotogether", 4},
		{"gotogether.x/1", "gotogether.x/1 is an older version than this gotogether reads (gotogether.x/2 to gotogether.x/3): write it again", 1},
		{"gotogether.y/3", `format "gotogether.y/3" is no x file`, 0},
		{"gotogether.x/03", "is no x file", 0},
		{"x/3", "is no x file", 0},
	} {
		m, err := f.Check(c.name)
		if c.err == "" && (err != nil || m != c.major) || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("Check(%s) = %d, %v; want %d, %q", c.name, m, err, c.major, c.err)
		}
	}
}
