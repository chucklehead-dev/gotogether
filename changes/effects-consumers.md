- Effect records are no steps for the tools that list a run's steps: a
  navigation node's `Steps` (and gotogether-nav's "steps so far" and its
  count) hold steps only, each one's effects under it
  (`navproto.Node.StepEffects`, by qtrace's attribution) and those made by
  no step so far apart (`Node.Unattributed`); `qhegel.Render` lists an
  effect record unnumbered, as an effect, marking no restart; a run that
  recorded only effect records reads as one that recorded no step, and
  a navigation opened at `fail` finds its failing step's choice again
  (effect records take seqs but are not counted in `Choice.Step`: it
  opened past the failing step).
- qhegel's case checker reuses no prefix's verdict for a case with a late
  effect (one made under a call after it completed), which changes the
  effects of a step the prefix judged.
- actionmap finds `qobs.Invoke` sites as it finds `qobs.Record`'s: a call
  of a constant action names its function a site of that action.
