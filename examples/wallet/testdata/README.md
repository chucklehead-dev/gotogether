`seed20764114.steps.jsonl` is the original collected wallet history from
`TestAllFaults`, seed 20764114, with all fault families enabled: 40 records,
19 completed calls, no pending calls, and a final settle record. The source
campaign used seeds 20764001–20764500; its 1594 recorded scheduling/fault
choices reproduced this history. This fixture retains the observations and
recorder ordering directly, without coupling the search regression to a
particular scheduler implementation or running a campaign.

The original search exhausted its 200-order complete-phase budget without a
verdict. Exact accepted-prefix state recovery finds a conforming order within
the same budget. The regression replays that order independently with Quint,
checks all three wallet invariants, and verifies recorded precedence edges.
