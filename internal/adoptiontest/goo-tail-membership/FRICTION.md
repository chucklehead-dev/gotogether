# Observed friction and limits

- The external checkout is sparse. Missing exploration files in its working tree
  did not mean the API was absent; published modules and pinned Git objects contain
  it. No change to Goo was needed.
- Whole canonical JSON strings cannot be Quint string literals: the language has
  no quote escapes. The first faithful validation refused the input with exit 2.
  Lossless base64 preserves byte equality without dropping fields, rewriting
  quotes or inventing a coherent Boolean. Raw native results remain available.
- Seconds are a deliberate bounded projection, not an implicit precision loss.
  The driver refuses subsecond timestamps and windows before projecting them.
- Domain rejections are actual library returns, recorded separately from observer
  setup or tool failures. A failed process or missing observation cannot count as
  the independent model detecting a source contrast.
- This uses an isolated synchronous API and authored histories. Existing native
  and Hegel coverage retains credit; neither concurrency nor durable publication
  correctness follows from this rehearsal.
- A copied module-cache directory retains read-only permissions. The source
  contrast makes only its disposable copy's directories writable so successful
  reproduction also cleans up successfully; it never changes cache permissions.

The frozen model rejects the planted exact-expiry defect at sequence 10. Faithful
and failing bundles preserve their respective verdicts after relocation; an
omitted members observation is an evidence error rather than a conformance fail.
These controls establish this bounded workflow, not a production defect.

No new core API is currently required. Further capability work should follow a
demonstrated adopter blocker, rather than another adjacent model or format.
