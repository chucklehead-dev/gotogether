package names

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/navproto"

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
		{navproto.Protocol, formats.Nav},
		{validate.CompiledFormat, formats.Model},
		{validate.ReportFormat, formats.Report},
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
