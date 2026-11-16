- `gotogether types` generates the Go types of a model's types (ROADMAP
  CW5's static half): `-spec model.qnt -main M` (a literate `.md` too) or
  `-binding b.yaml`, `-package`, `-o` (default standard output). One
  gofmt'd file: `State`, a field per state variable tagged with its name;
  per type declaration a struct for a record (fields tagged
  `itf:"<field>"`) or a tuple (`F0, F1, ...`), an enum as a string type
  with a constant per variant and an `Enum` marker, any other sum as a
  struct with a `Tag` of its own type and a field per variant that carries
  a value, `Option[T]` as `*T`, sets and lists as slices, maps as Go maps
  when the key is comparable and slices of key-value pairs otherwise.
  What Go cannot say there (an uninterpreted type, a type variable, a sum
  whose variants differ in case or underscores alone) is `itf.Value`, with
  a comment saying why. Taken names take a trailing underscore. The header
  carries a digest per type and variable: `-check` exits 1 when the file
  is missing or stale, naming the types that changed and the command that
  writes it again (`FAIL f.go: stale: type Note added, type Phase
  changed: run gotogether types ...`), and 2 for a bad invocation.
