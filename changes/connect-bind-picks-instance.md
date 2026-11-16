- `connect.Bind`'s per-action pick check works under an instance
  module's Main (`module gateway { import attempts(CALLS = ...).* }`, the
  Main the skills recommend). quint aliases its step (`q::step =
  gateway::attempts::step`), which the attribution followed as an action
  and passed all of step's nondets to every call it applies: every
  action's picks came out as the whole model's, so a borrowed pick
  (failed's `outcome` bound to attempt) passed silently (golem, #144). A
  called action's own calls now take what flows into it, not every
  nondet its body binds.
- An action whose picks are checked against the whole model's (applied
  under a `match`, a let-bound or recursive action, one `step` never
  applies) is logged once per run: "connect.Bind: picks of attempt not
  attributed (applied under matchVariant, which the attribution does not
  follow): checked against the whole model's". So is every action when
  each came out with all of step's nondets although step applies
  several: the attribution's failure is reported, its picks accepted.
