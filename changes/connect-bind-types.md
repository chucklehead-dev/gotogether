- `connect.Run` and `connect.Test` check a `connect.Bind` binding against
  the model before any trace runs (ROADMAP CW5's static half, first
  slice): every bound action is one of the model's, every pick a nondet
  of `step` or a parameter of the model's action, every Go type one
  `itf.Decode` fills from the model's type for it, and `Returns`' def a
  val or def of the given arity whose type decodes into the result. All
  mismatches fail the test at once, one line each naming the action, the
  pick, both types and the fix ("connect.Bind: action deposit, parameter
  2 (pick amount): Go type string does not fit the model's int ..."), so a
  typo in an action no trace sampled no longer passes. The check reuses
  the compile `Step.Eval` and `Config.Require` make; it accepts what it
  cannot be sure of (type variables, interfaces, `itf.Unmarshaler`s), and
  logs, not refuses, the model's actions bound to nothing.
