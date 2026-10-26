# Mutants as build overlays

Read this when the code under test must stay as it is (a service that
imports the standard library alone, code another team owns), so a
`Mutation` option cannot be added to it. examples/payd does it this way.

A mutant is a directory whose files replace the package's files of the
same names: the whole file, the bug written as the developer who made it
would have written it. `go test -overlay overlay.json` builds the test
binary with them, and the package's own files are never touched:

```json
{"Replace": {
  "/abs/path/payd/charge.go": "/abs/path/testdata/mutants/precheck/charge.go",
  "/abs/path/zz_mutant_test.go": "/tmp/x/marker.go"
}}
```

- Keep the mutants under `testdata/` (`testdata/mutants/<name>/*.go`): the
  go tool skips it, so `go vet ./...` does not build them as packages of
  their own.
- Add one more file to the overlay, a test file that names the mutant
  (`func init() { mutant = "precheck" }`). Tests read `mutant`: one
  `TestMutant` states each mutant's claims with `.Expect(t, ...)`, and the
  faithful tests skip what only applies to the service as it is.
- A parent test (`TestMutants`) writes each overlay with absolute paths
  (relative ones resolve against the go command's directory), runs `go
  test -overlay <it> -run '^TestMutant$'` once per mutant, and fails with
  that output. Pass `-short` and `-timeout` through. Leave
  `GOTOGETHER_SEEDS` out of the child's environment: a mutant's claim
  needs its stated miss chance, not a nightly's 500 seeds.
- Each mutant costs a link (about a second, cached after). Its binary is
  the service's own: nothing of the toolkit is woven in.

**Overlays or qweave `-mutate`.** qweave's mutants (qmut) are mechanical:
a dropped call, a negated condition, a changed boundary, all of them in
one woven build, a mutant per run. Use them to find what the model does
not constrain (a survivor). Use overlays for planted, named design bugs
that no operator writes, such as a dedupe rewritten as check-then-insert
or a debit moved ahead of the check that decides who completes, and
where the service's build must stay its own.
