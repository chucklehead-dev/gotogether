- `actionmap -roundtrips` (qanalysis) lists each connect driver action whose
  code makes more than one round trip into shared state (a context-taking
  method of an interface or another module's type), which no connect trace
  interleaves (ROADMAP N5, golem's #144); `actionmap.RoundTrips`,
  `Split`, `RoundTrip`, `SplitReport`.
