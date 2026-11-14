# Unreleased changes, one file each

A change that a user of a published module would notice (an exported API,
a CLI flag or report line, a format, a default) adds one file here,
`changes/<topic>.md`, holding its CHANGELOG entry: one or more bullets, as
the CHANGELOG writes them.

```markdown
- `connect.Config.Require` names boolean vals the run's traces must reach
  (ROADMAP N1): ...
```

One file per change, never an edit of CHANGELOG.md: two pull requests that
each added a line under "Unreleased" conflicted there every time. A file of
its own conflicts with nothing.

- The name is the topic in kebab case (`required-states.md`); a second
  change to the same topic edits that file.
- Bullets start at column 0 with `- `, continuation lines are indented two
  spaces; no headings. `go test ./internal/ciconfig/` checks the form
  (`TestChangeFragments`), and `ci/preflight.sh` runs it.
- At a release, `ci/changelog.sh -write` moves every fragment under
  CHANGELOG.md's "Unreleased" heading, newest first (by the commit that
  added it), and deletes them; without `-write` it prints what it would
  insert.
