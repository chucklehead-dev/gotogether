- `connect.Bind` maps a model's actions onto an implementation's methods
  (ROADMAP CW5, the runtime half): `Bind(newImpl).Action("deposit",
  (*Bank).Deposit, "account", "amount")` binds an action to a method, its
  picks to the parameters in order, decoded into their types; a leading
  `context.Context` is the trace's, a pointer parameter optional, a final
  error the step's failure. `Returns` compares a result with a model def
  (`Step.Eval`), and a result nothing checks refuses the driver.
  `Handle`, `Skip`, `State` and `Check` cover the rest; `b.Driver` is the
  factory `connect.Run` and `connect.Test` take. A malformed binding
  panics where it is built, naming the fix.
