# bis Fuzzing: Go's fuzzer drives the simulation

```go
func FuzzShop(f *testing.F) { sys.Fuzz(f) }   // sys is a qtest.System, at its Rates
```

- Each fuzz input is one run: its bytes are raw choice entropy (the same
  byte rule as Hegel's chooser), and the model judges it. Generated inputs
  never interpret a byte prefix as a serialized tape header. `go test` runs
  the corpus as regression tests; `go test
  -fuzz FuzzShop -fuzztime 60s` explores, guided by coverage of the code
  under test.
- Verdicts are cached by the run's steps (for the test binary): most
  inputs repeat a run's model-level steps. On checkout, that took the
  fuzzer from 16 runs in 90 s to 14,633.
- Keep a shrunk Hegel failure, once fixed, as a seed:
  `sys.Fuzz(f, must(qenv.EncodeChoices(v.Choices, sys.FaultRates())))`.

  Explicit seed arguments are encoded recordings: Fuzz validates their version
  and decodes their payload once before adding them. Invalid seed versions
  fail setup with the upgrade/re-encode guidance. Saved Go fuzz corpus files
  contain raw entropy; a tape-like prefix participates in the run's choices too.
