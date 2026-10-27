// The library app's model: the rules and invariants grist's generated
// skeleton (library_gen.qnt, `go run ./cmd/library model`: the events, an
// action per block with its outcome branches and frame conditions, step)
// calls. Regenerating the skeleton never touches this file.

// The rules: each hole of the skeleton, and the read models' folds.
module libraryRules {
  import grist.* from "./grist"
  import libraryEvents.* from "./library_gen"

  // what the model on its own (gotogether simulate) draws arguments from
  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = (-1).to(3)

  // how many loans a member may hold at once (the app's Limit)
  pure val LIMIT = 3

  // whether m joined
  pure def isMember(log: List[Event], m: str): bool =
    log.foldl(false, (b, e) => b or (e.typ == "member-joined" and memberJoinedMember(e) == m))

  // books, keyed by book: its copies, and how many are on loan
  pure def copies(log: List[Event], b: str): int =
    booksEvents(log, b).foldl(0, (s, e) => if (e.typ == "book-added") s + bookAddedCopies(e) else s)
  pure def onLoan(log: List[Event], b: str): int =
    booksEvents(log, b).foldl(0, (s, e) =>
      if (e.typ == "book-borrowed") s + 1 else if (e.typ == "book-returned") s - 1 else s)

  // loans, keyed by member: how many copies of b m holds, and how many loans
  pure def held(log: List[Event], m: str, b: str): int =
    loansEvents(log, m).foldl(0, (s, e) =>
      if (e.typ == "book-borrowed" and bookBorrowedBook(e) == b) s + 1
      else if (e.typ == "book-returned" and bookReturnedBook(e) == b) s - 1
      else s)
  pure def loanCount(log: List[Event], m: str): int =
    loansEvents(log, m).foldl(0, (s, e) => if (e.typ == "book-borrowed") s + 1 else s - 1)

  // join: refused for a member who joined, on the member's join
  pure def joinOutcome(log: List[Event], c: JoinArgs): str =
    if (isMember(log, c.member)) "already-member" else "ok"
  pure def joinEvents(log: List[Event], c: JoinArgs): List[Event] = [memberJoined(c.member)]
  pure def joinNeeds(c: JoinArgs, result: str): Set[Read] = Set(memberJoinedByMember(c.member))

  // add-book: copies that are not positive refused before any read
  pure def addBookOutcome(log: List[Event], c: AddBookArgs): str = if (c.copies <= 0) "invalid" else "ok"
  pure def addBookEvents(log: List[Event], c: AddBookArgs): List[Event] = [bookAdded(c.book, c.copies)]
  pure def addBookNeeds(c: AddBookArgs, result: str): Set[Read] = Set()

  // borrow: a stranger refused on the member's join, a member at the limit
  // on their loans too, a book with no copy free on its shelf too
  pure def borrowOutcome(log: List[Event], c: BorrowArgs): str =
    if (not(isMember(log, c.member))) "not-member"
    else if (loanCount(log, c.member) >= LIMIT) "limit"
    else if (copies(log, c.book) - onLoan(log, c.book) <= 0) "unavailable"
    else "ok"
  pure def borrowEvents(log: List[Event], c: BorrowArgs): List[Event] = [bookBorrowed(c.member, c.book)]
  pure def borrowNeeds(c: BorrowArgs, result: str): Set[Read] =
    if (result == "not-member") Set(memberJoinedByMember(c.member))
    else if (result == "limit") Set(memberJoinedByMember(c.member)).union(loansReads(c.member))
    else Set(memberJoinedByMember(c.member)).union(loansReads(c.member)).union(booksReads(c.book))

  // return-book: refused unless the member holds a copy, on their loans
  pure def returnBookOutcome(log: List[Event], c: ReturnBookArgs): str =
    if (held(log, c.member, c.book) > 0) "ok" else "not-held"
  pure def returnBookEvents(log: List[Event], c: ReturnBookArgs): List[Event] = [bookReturned(c.member, c.book)]
  pure def returnBookNeeds(c: ReturnBookArgs, result: str): Set[Read] = loansReads(c.member)

  // member-loans: how many loans the member holds
  pure def memberLoansAnswer(log: List[Event], q: MemberLoansArgs): MemberLoansResult =
    { count: loanCount(log, q.member) }

  // receipts skips no borrow (its handler never Skips)
  pure def receiptsSkips(prefix: List[Event], trigger: int): bool = false

  // the receipt the mailer is asked to send for the borrow at the trigger:
  // its member and book (the mailer sends each key once: Send is Keyed, and
  // the generated mailerSendAdmits says so)
  pure def receiptsMailerSend(prefix: List[Event], trigger: int): MailerSendRequest =
    { member: bookBorrowedMember(prefix[trigger - 1]), book: bookBorrowedBook(prefix[trigger - 1]) }

  // the books and members the log names
  pure def booksIn(log: List[Event]): Set[str] =
    log.foldl(Set(), (s, e) => if (e.typ == "book-added") s.union(Set(bookAddedBook(e))) else s)
  pure def membersIn(log: List[Event]): Set[str] =
    log.foldl(Set(), (s, e) => if (e.typ == "book-borrowed") s.union(Set(bookBorrowedMember(e))) else s)

  // copies on loan never exceed the book's copies (nor go below none)
  pure def copiesHeldIn(log: List[Event]): bool =
    booksIn(log).forall(b => 0 <= onLoan(log, b) and onLoan(log, b) <= copies(log, b))
      and log.foldl(true, (ok, e) => ok and (e.typ != "book-borrowed" or copies(log, bookBorrowedBook(e)) > 0))
  // no member holds more than LIMIT loans
  pure def withinLimitIn(log: List[Event]): bool =
    membersIn(log).forall(m => loanCount(log, m) <= LIMIT)
}

// The store's side (library.binding.yaml).
module library {
  import grist.* from "./grist"
  import libraryEvents.* from "./library_gen"
  import libraryRules.*
  import libraryGen.* from "./library_gen"
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
  import libraryRules.*
  import libraryCallsGen.* from "./library_gen"
  export libraryCallsGen.*

  // grist: invariant
  val copiesHeld = copiesHeldIn(log)

  // grist: invariant
  val withinLimit = withinLimitIn(log)
}
