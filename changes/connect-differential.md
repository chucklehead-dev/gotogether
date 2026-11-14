- `connect.Differential` replays each step on several implementations of one
  interface and fails where one fails and another does not, or their
  observations differ (`connect.Observer`'s `Observe`, else `State`); with
  `IgnoreModel`, traces of a looser model reach what the design's model never
  does (ROADMAP N7, golem's #144). connect's diffs name both sides.
