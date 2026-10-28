# Mutants as build overlays

Read this when you plant a bug by hand: the taught way for every system,
and the only one when the code under test must stay as it is (a service
that imports the standard library alone, code another team owns).
examples/hello, examples/payd and every grist example do it this way.

A mutant is a directory of `testdata/mutants/` whose files replace the
package's files of the same names: the whole file, the bug written as
the developer who made it would have written it. `go test -overlay`
builds the test binary with them, and the package's own files are never
touched. The core's harness does the rest:

```go
// TestMutant states what catches the mutant this binary was built with.
func TestMutant(t *testing.T) {
	switch qtest.Mutant() {
	case "":
		t.Skip("the system as it is: TestMutants runs this test in each mutant's binary")
	case "noDedupe":
		system().Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, qtest.Some(qtest.Rejects("apply")))
	default:
		t.Fatalf("mutant %s: say what catches it", qtest.Mutant())
	}
}

func TestMutants(t *testing.T) { qtest.Mutants{}.Run(t) }
```

- `qtest.Mutants` finds the mutants from the directories of
  `testdata/mutants` (`Dir`): a new directory is a new mutant, and
  `TestMutant`'s `default` fails until it says what catches it. Keep them
  under `testdata/`: the go tool skips it, so `go vet ./...` does not
  build them as packages of their own.
- `Run` writes each overlay with absolute paths, runs `go test -overlay
  <it> -run '^TestMutant$'` once per mutant with `GOTOGETHER_MUTANT`
  naming it (`qtest.Mutant()`), `-short` and `-timeout` passed through
  and `GOTOGETHER_SEEDS` left out (a mutant's claim needs its stated miss
  chance, not a nightly's 500 seeds), and fails with the binary's output.
- `System.Mutant: qtest.Mutant()` keeps a mutant's runs out of the
  suite's evidence. A faithful test that must not run in a mutant's
  binary skips when `qtest.Mutant() != ""`.
- `Package` names the directory whose files a mutant replaces, when the
  tests live in another package of the module (payd's and webjobs'
  harness packages: `qtest.Mutants{Package: "payd"}`). `Overlay(t, name)`
  gives the overlay to `go build` a mutant program (webjobs' server).
  `FullOnly` are mutants built in full mode only; `Generated` are mutants
  a binary makes of itself with no overlay (grist's dropped rules,
  `gristtest.RuleMutants(App)`); `Show` names output lines to log.
- A failing run in a mutant's binary is reproduced through its harness
  (its reproduce line runs `TestMutants`' subtest of the mutant, which
  rebuilds it and passes `GOTOGETHER_RUN` on) and navigated in the
  mutant: its navigate line sets `GOTOGETHER_MUTANT=<name>`, and
  `gotogether nav` and `gotogether-nav` build the test binary with the
  mutant's overlay.
- Each mutant costs a link (about a second, cached after). Its binary is
  the service's own: nothing of the toolkit is woven in.

**Overlays or qweave `-mutate`.** qweave's mutants (qmut) are the
generated way: a dropped call, a negated condition, a changed boundary,
all of them in one woven build, a mutant per run. Use them to find what
the model does not constrain (a survivor). Use overlays for planted,
named design bugs that no operator writes, such as a dedupe rewritten as
check-then-insert or a debit moved ahead of the check that decides who
completes, and where the service's build must stay its own. A `Mutation`
option in the code under test is the third way, older: the code carries
its bugs, and a reader cannot tell them from its features.
