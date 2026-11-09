# Native counterexample unit encoding

The edgepublish pair retains all seven states from a pinned Quint 0.33.1 /
Apalache 0.62.4 counterexample to `sealMatchesManifests`, bound 8, after mode,
default 4 GiB JVM policy. The actual native exit was 1. The structured result
contains only its unchanged `stage`, `status`, `errors`, and `trace` fields;
compiled module/type tables are omitted. The ITF file is unchanged.

Source: gotogether PR130 head `42c5696294d80ff72135477030dce113a99fb4d6`.
The initial adapter rejected state 1's writer because a nested nullary phase
payload is ITF `{tag: "UNIT"}` but the structured literal is an empty tuple.
The complete raw result and bounded reproduction receipts are retained outside
Git under `/workspace/review-artifacts/native-edgepublish-counterexample/`.
This is a counterexample-decoding regression, not a proof of the property.
