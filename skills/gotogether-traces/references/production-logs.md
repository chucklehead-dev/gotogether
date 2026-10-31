# Production logs: select and sanitize first


```sh
GOTOGETHER_SALT=$SECRET gotogether steps -actors 'checkout-*' -since 2026-10-01T00:00:00Z \
  -pseudonymize account,customer -binding app.binding.yaml -out clean.jsonl spans.jsonl
gotogether validate -binding app.binding.yaml clean.jsonl
```

- `-binding` keeps only the fields the binding reads, and drops error
  texts: what a check needs, nothing more.
- Sanitizing (`-pseudonymize` or `-binding`) drops steps' refs
  (`quint.ref`): free text, often a path or URL with a user's name in it,
  which no pseudonym reaches. `-keep-refs` (`Filter.KeepRefs`) keeps them;
  a filter that only selects (`-actors`, `-since`) keeps them anyway.
- Pseudonyms keep equality, so interned ids (`intern:`) still work. A
  model constant naming concrete ids (`ACCOUNTS: Set("alice", ...)`) must
  name the pseudonyms instead (`qtrace.Filter.Pseudonym`), or intern the
  field.

## A bounded incident walkthrough

The [jobqueue runbook](https://github.com/chucklehead-dev/gotogether/blob/main/examples/jobqueue/INCIDENT.md)
exercises native report JSON, exact captured-record inspection, TUI evidence,
relocated replay and a real repaired rerun under an unchanged model. It is a
planted implementation incident, not a claim of production capture completeness.
Select failures by native verdict/check/kind and actor/recorder/sequence;
inferred restart means a model-step index can differ from capture sequence.
TUI evidence stays tied to the original checked history across hypothetical
model moves. Exit 1 can mean either model failure or no verdict: inspect the
native `verdict`, and keep tool/data errors separate.

