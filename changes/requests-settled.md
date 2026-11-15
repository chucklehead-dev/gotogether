- The request log (`gotogether.requests/1`) takes a `settled` record,
  `{"event": "settled", "time": ..., "ref": ...}` (`qdriver.Settled`): a
  driver says the system has settled, and it reads as the settle
  `qobs.Settled` records, so a binding's eventual properties are judged in
  the run's last state. It carries only its time and ref; a record after
  it voids it, as a step after any settle does. An added record kind, so
  the protocol's major is unchanged. The Playwright adapter writes it with
  `settled(ref)`, after waiting for the answers being read (ROADMAP O1;
  Goo's drain bridge on #220).
