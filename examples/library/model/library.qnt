// The library app's invariants: what its bindings judge, over grist's
// generated actions (library_steps.qnt, `go run ./cmd/library model`: an
// action per block with its outcome branches and frame conditions, step)
// and the app's rules (library_rules.qnt). Regenerating the skeleton never
// touches this file.

// The store's side (library.binding.yaml).
module library {
  import grist.* from "./grist"
  import libraryEvents.* from "./library_gen"
  import libraryRules.* from "./library_rules"
  import libraryGen.* from "./library_steps"
  export libraryGen.*

  // copies on loan never exceed the copies of the book
  // grist: invariant
  val copiesHeld = copiesHeldIn(log)

  // no member holds more than LIMIT loans
  // grist: invariant
  val withinLimit = withinLimitIn(log)

  // every receipt the mailer sent is for a borrow that happened, of its
  // member and book: no invariant, but the generated mailerSendServed's
  // own guard (its request receiptsMailerSend's for its trigger, a borrow
  // in the log), which rejects the mailer's record of any other receipt

  // after a settle: every borrow's receipt sent
  // grist: eventually
  val everyBorrowReceipted = log.indices().forall(i => log[i].typ == "book-borrowed" implies
    mailerSendRecords.foldl(false, (b, r) => b or (r.trigger == i + 1 and r.result == "ok")))
}

// The clients' side (library.calls.binding.yaml), linearized.
module libraryCalls {
  import grist.* from "./grist"
  import libraryEvents.* from "./library_gen"
  import libraryRules.* from "./library_rules"
  import libraryCallsGen.* from "./library_steps"
  export libraryCallsGen.*

  // grist: invariant
  val copiesHeld = copiesHeldIn(log)

  // grist: invariant
  val withinLimit = withinLimitIn(log)
}
