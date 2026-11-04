# Provenance

The inputs are exact repository blobs from two commits in
`chucklehead-dev/gotogether`:

| Side | Commit | Repository files |
|---|---|---|
| before | `dfeda31a27b9019c6d77cf31bf68ba421fd65ba0` | `examples/hello/hello.binding.yaml`, `examples/hello/model/hello.qnt` |
| after | `5ead75826986a5ad37403c094c55db3ba6eed963` | the same paths |

The after commit is titled **X8: hello's mutation test ends in NoSurvivors**.
Its recorded motivation was a real mutation-test gap: a client sending only two
of three requests conformed because nothing said it finished. The model gained
`done`, enabled only when every request was applied. The client records it when
it has its answers. Occurrence claims also detect dropping `done` or recording
nothing. This is historical agent-assisted development evidence, not a new
production defect or an independently authored adoption study.

The files under `before/` and `after/` have not been normalized, modernized,
rewritten, or given different constants. The binding's original relative model
path still resolves in each copied tree. Source identities:

| Frozen path | SHA-256 |
|---|---|
| `before/hello.binding.yaml` | `72d43f8d76c7e649f5b661689e2d825c65b51ce01f4e35c265b35d3520bbbe33` |
| `before/model/hello.qnt` | `dac59ec456e0a1707445112d00d3cb851add06b7e9613cd95fd665f73b3868ab` |
| `after/hello.binding.yaml` | `f06e64fc555e521606da435c0767d05d0795191dc734f989a08f09cc4e5ca3f1` |
| `after/model/hello.qnt` | `8d9bad09b1aa1d6c3648fdab8852e7cf0ce3b36788b6fc98c6c0b7aef8339e93` |

`SOURCE.sha256` makes these four source identities executable checks. Freezing
historical inputs preserves the review evidence; it is not a promise to maintain
old SDKs, binding formats, or command behavior. The reproducer builds the current
checkout and records its Go build metadata and revision separately.

Expected bounded result: `equivalent` for `applied,count`, eight states per
instance, 24 before transitions and 25 after transitions. Both directional
statuses are `included`, with no witness or native replay claim. `done` changes
action-level completion obligations while preserving this observed state
language; those two statements are compatible.
