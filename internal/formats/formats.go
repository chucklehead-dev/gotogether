// Package formats names the toolkit's formats and holds their version
// policy (docs/DESIGN.md, "Formats and their versions"): every format a
// file or a pipe carries between independently installed pieces is
// named gotogether.<thing>/<major>; a reader refuses a newer major with
// the fix (upgrade the reader) and reads every older major from Oldest
// on; a breaking change bumps the major, an added optional field does
// not. One table, so the policy is changed in one place and `gotogether
// doctor` lists what this version reads.
package formats

import (
	"fmt"
	"strconv"
	"strings"
)

// Prefix begins every format's name.
const Prefix = "gotogether."

// A Format is one format of the toolkit at the version this build
// writes.
type Format struct {
	// Thing is the format's name between Prefix and the major ("steps").
	Thing string
	// Major is the version this build writes; it reads Oldest to Major.
	Major, Oldest int
	// What says what the format is, for doctor and the docs.
	What string
	// Older is the fix for a major below Oldest: what writes it anew.
	Older string
	// Upgrade is the fix for a newer major: which reader to upgrade.
	// Empty: the gotogether module and CLI.
	Upgrade string
	// Aliases are older names of a major (an old corpus entry's
	// "gotogether.run/1" is corpus/1), read as that major forever.
	Aliases map[string]int
}

// upgradeCore is the fix for a newer major read by the core.
const upgradeCore = "upgrade gotogether where it reads it (go get github.com/chucklehead-dev/gotogether@latest in the module, go install github.com/chucklehead-dev/gotogether/cmd/gotogether@latest for the CLI)"

// The formats, at the versions this build writes. Read-only.
var (
	// Steps is the step log (qobs.JSONLSink, qtrace.ReadJSONL) and the
	// attributes of OTel step events: production data, so every major is
	// read forever. A log without a format record is steps/1, the format
	// as it was before it had a name.
	Steps = Format{Thing: "steps", Major: 1, Oldest: 1, What: "step log (JSON lines; OTel span events)"}
	// Requests is an external driver's request log (qdriver).
	Requests = Format{Thing: "requests", Major: 1, Oldest: 1, What: "driver request log (qdriver)"}
	// Binding is a binding file's optional format key; absent, binding/1.
	Binding = Format{Thing: "binding", Major: 1, Oldest: 1, What: "binding YAML (format: key optional)"}
	// Convert is a span conversion rule file's optional format key.
	Convert = Format{Thing: "convert", Major: 1, Oldest: 1, What: "span conversion rules YAML (format: key optional)"}
	// Corpus is an exploration's corpus entry (qtest), checked into
	// testdata: it was named gotogether.run/1 until it had a name of its
	// own, and that name is read as corpus/1 forever.
	Corpus = Format{Thing: "corpus", Major: 1, Oldest: 1, What: "exploration corpus entry (testdata/corpus)",
		Aliases: map[string]int{"gotogether.run/1": 1}}
	// Choices is the byte rule of an encoded run (qenv.EncodeChoices),
	// whose binary header carries the major (qenv.BytesVersion). A rule's
	// change makes old bytes another run, so only the current is read.
	Choices = Format{Thing: "choices", Major: 1, Oldest: 1, What: "encoded choices (qenv.EncodeChoices; binary header)",
		Older: "encode the run again (qenv.EncodeChoices) from its choices"}
	// Manifest is a validated run's manifest (validate.Manifest), written
	// anew by every run: only the current major is read.
	Manifest = Format{Thing: "run", Major: 2, Oldest: 2, What: "run manifest (validate -manifest)",
		Older: "write it anew with this version (gotogether validate -manifest, or the test under GOTOGETHER_EVIDENCE)"}
	// Evidence is an exploration's evidence (qtest.Evidence), written
	// anew by every run: only the current major is read.
	Evidence = Format{Thing: "evidence", Major: 2, Oldest: 2, What: "exploration evidence (GOTOGETHER_EVIDENCE)",
		Older: "run the tests again to write it anew"}
	// Model is a compiled model (gotogether compile; validate
	// -compiled): the Go evaluator's IR of a binding's model, compiled by
	// quint. A breaking change makes an artifact another check, so only
	// the current major is read.
	Model = Format{Thing: "model", Major: 1, Oldest: 1, What: "compiled model (gotogether compile; validate -compiled)",
		Older: "compile it again (gotogether compile -binding B -o model.json)"}
	// Report is a command's machine-readable report (validate -json,
	// simulate -json; validate.Document), read by alert routing, CI and
	// the Action's job summary. An added field is optional; a changed or
	// removed one is a new major, and a reader keeps reading the old.
	Report = Format{Thing: "report", Major: 1, Oldest: 1, What: "a command's machine-readable report (validate -json, simulate -json)"}
	// Nav is the run navigation protocol between a test binary
	// (GOTOGETHER_NAV=serve) and gotogether-nav: a session's first line
	// names it. A session that names none is nav/1.
	Nav = Format{Thing: "nav", Major: 1, Oldest: 1, What: "run navigation protocol (GOTOGETHER_NAV=serve, gotogether-nav)",
		Upgrade: "install the navigator at the module's gotogether version (go install github.com/chucklehead-dev/gotogether/cmd/gotogether@<version> for gotogether nav, github.com/chucklehead-dev/gotogether/qnav/cmd/gotogether-nav@<version> for the interactive one; gotogether doctor names the versions)"}
)

// All is every format, for doctor and the docs.
func All() []Format {
	return []Format{Steps, Requests, Binding, Convert, Corpus, Choices, Manifest, Evidence, Model, Report, Nav}
}

// Name is the format's name at the major this build writes.
func (f Format) Name() string { return f.At(f.Major) }

// At is the format's name at major m.
func (f Format) At(m int) string { return Prefix + f.Thing + "/" + strconv.Itoa(m) }

// Family is the prefix of the format's names at every major.
func (f Format) Family() string { return Prefix + f.Thing + "/" }

// Parse splits a format's name into its thing and major.
func Parse(name string) (thing string, major int, ok bool) {
	rest, ok := strings.CutPrefix(name, Prefix)
	if !ok {
		return "", 0, false
	}
	thing, m, ok := strings.Cut(rest, "/")
	if !ok || thing == "" {
		return "", 0, false
	}
	major, err := strconv.Atoi(m)
	if err != nil || major < 0 || strconv.Itoa(major) != m {
		return "", 0, false
	}
	return thing, major, true
}

// Is reports whether name is one of f's names, at any major.
func (f Format) Is(name string) bool {
	if _, ok := f.Aliases[name]; ok {
		return true
	}
	thing, _, ok := Parse(name)
	return ok && thing == f.Thing
}

// Check returns the major of name, one f reads, or an error that names
// the fix: a newer major's says to upgrade the reader, an older one's
// how to write it anew, another format's what was wanted.
func (f Format) Check(name string) (int, error) {
	if m, ok := f.Aliases[name]; ok {
		return m, nil
	}
	thing, m, ok := Parse(name)
	switch {
	case !ok || thing != f.Thing:
		return 0, fmt.Errorf("format %q is no %s (%s): want %s", name, f.What, f.Family()+"<major>", f.Name())
	case m > f.Major:
		up := f.Upgrade
		if up == "" {
			up = upgradeCore
		}
		return m, fmt.Errorf("%s is a later version than this gotogether reads (%s): %s", name, f.Range(), up)
	case m < f.Oldest:
		return m, fmt.Errorf("%s is an older version than this gotogether reads (%s): %s", name, f.Range(), f.Older)
	}
	return m, nil
}

// Range is the names this build reads, "gotogether.steps/1" or
// "gotogether.x/1 to gotogether.x/3".
func (f Format) Range() string {
	if f.Oldest == f.Major {
		return f.Name()
	}
	return f.At(f.Oldest) + " to " + f.Name()
}
