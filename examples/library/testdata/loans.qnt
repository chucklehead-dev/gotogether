// library_rules.qnt's read-model folds against the definitions they
// replaced (docs/performance.md, the library's proofs): the earlier
// definitions, verbatim, and a machine that grows a log of every event
// the models append, whose invariant is that each new definition equals
// its earlier one at every member and book. The models' logs hold only
// events their generated constructors make (memberJoined, bookAdded,
// bookBorrowed, bookReturned, grist.failed, each batch stamped with its
// cause by caused), and each constructor tags an event with the strings it
// stores: a borrow's member and book (its first and second strings) are
// the ones it is tagged with (loansEvents' and booksEvents' selections).
// The machine makes events the same way, with every member, book, count
// and cause. loans_test.go samples it (quint run); quint verify checks it
// for every log of a bounded length; and two planted mistakes (planted)
// show that the check fails when a new definition is wrong.
module loansOld {
  import libraryEvents.* from "../model/library_gen"
  import libraryRules.* from "../model/library_rules"

  // the earlier definitions
  pure def oldCopies(log: List[Event], b: str): int =
    booksEvents(log, b).foldl(0, (s, e) => if (e.typ == "book-added") s + bookAddedCount(e) else s)
  pure def oldOnLoan(log: List[Event], b: str): int =
    booksEvents(log, b).foldl(0, (s, e) =>
      if (e.typ == "book-borrowed") s + 1 else if (e.typ == "book-returned") s - 1 else s)
  pure def oldHeld(log: List[Event], m: str, b: str): int =
    loansEvents(log, m).foldl(0, (s, e) =>
      if (e.typ == "book-borrowed" and bookBorrowedBook(e) == b) s + 1
      else if (e.typ == "book-returned" and bookReturnedBook(e) == b) s - 1
      else s)
  pure def oldLoanCount(log: List[Event], m: str): int =
    loansEvents(log, m).foldl(0, (s, e) => if (e.typ == "book-borrowed") s + 1 else s - 1)
  pure def oldLoanBooks(log: List[Event], m: str): List[str] =
    loansEvents(log, m).foldl([], (books, e) =>
      if (e.typ == "book-borrowed") books.append(bookBorrowedBook(e))
      else removeFirst(books, bookReturnedBook(e)))
  pure def oldCopiesHeldIn(log: List[Event]): bool =
    booksIn(log).forall(b => 0 <= oldOnLoan(log, b) and oldOnLoan(log, b) <= oldCopies(log, b))
      and log.foldl(true, (ok, e) => ok and (e.typ != "book-borrowed" or oldCopies(log, bookBorrowedBook(e)) > 0))
  pure def oldWithinLimitIn(log: List[Event]): bool =
    membersIn(log).forall(m => oldLoanCount(log, m) <= LIMIT)

  // each definition the rules, queries and invariants read equals its
  // earlier one on log
  pure def agreeOn(log: List[Event]): bool = and {
    STRS.forall(b => copies(log, b) == oldCopies(log, b) and onLoan(log, b) == oldOnLoan(log, b)),
    STRS.forall(m => loanCount(log, m) == oldLoanCount(log, m) and loanBooks(log, m) == oldLoanBooks(log, m)),
    STRS.forall(m => STRS.forall(b => held(log, m, b) == oldHeld(log, m, b))),
    copiesHeldIn(log) == oldCopiesHeldIn(log),
    withinLimitIn(log) == oldWithinLimitIn(log),
  }
}

module loans {
  import libraryEvents.* from "../model/library_gen"
  import libraryRules.* from "../model/library_rules"
  import loansOld.*

  pure val CAUSES: Set[Cause] = Set(("command", "i1"), ("command", "i2"), ("processor", "receipts"))

  var log: List[Event]

  action init = log' = []
  action step = any {
    {
      nondet m = STRS.oneOf()
      nondet c = CAUSES.oneOf()
      log' = log.concat(caused([memberJoined(m)], c))
    },
    {
      nondet b = STRS.oneOf()
      nondet n = INTS.oneOf()
      nondet c = CAUSES.oneOf()
      log' = log.concat(caused([bookAdded(b, n, "main")], c))
    },
    {
      nondet m = STRS.oneOf()
      nondet b = STRS.oneOf()
      nondet c = CAUSES.oneOf()
      log' = log.concat(caused([bookBorrowed(m, b)], c))
    },
    {
      nondet m = STRS.oneOf()
      nondet b = STRS.oneOf()
      nondet c = CAUSES.oneOf()
      log' = log.concat(caused([bookReturned(m, b)], c))
    },
    {
      nondet t = 1.to(3).oneOf()
      log' = log.append(failed("receipts", t))
    },
  }

  val agree = agreeOn(log)

  // planted mistakes, each a definition the check must reject: a loan
  // count that ignores its member, and copies that count every addition
  // whatever its book
  pure def plantedLoanCount(log: List[Event], m: str): int =
    log.foldl(0, (s, e) => if (e.typ == "book-borrowed") s + 1 else if (e.typ == "book-returned") s - 1 else s)
  pure def plantedCopies(log: List[Event], b: str): int =
    log.foldl(0, (s, e) => if (e.typ == "book-added") s + bookAddedCount(e) else s)
  val plantedLoanCountAgrees = STRS.forall(m => plantedLoanCount(log, m) == oldLoanCount(log, m))
  val plantedCopiesAgree = STRS.forall(b => plantedCopies(log, b) == oldCopies(log, b))
}

// the same equalities in every state the models reach (sampled by
// loans_test.go): the store's side and the clients'
module loansStore {
  import library.* from "../model/library"
  import loansOld.*
  val agree = agreeOn(log)
}

module loansCalls {
  import libraryCalls.* from "../model/library"
  import loansOld.*
  val agree = agreeOn(log)
}
