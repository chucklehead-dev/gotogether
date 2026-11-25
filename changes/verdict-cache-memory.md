- qtest's verdict cache, which keeps every distinct run a test binary judged,
  keeps no run's records: its keys are hashed, its steps are the positions its
  reports name, and its reports' steps carry no effect records. Since qenv's
  doubles record every send (qobs.Effect) it held thousands of records a run
  with the messages they sent, and a 500-seed nightly of examples/raftkv ran
  its runner out of memory.
