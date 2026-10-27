// examples/wallet-grist: examples/wallet's bank on grist, as one tenant's
// log. examples/wallet/model/wallet.qnt keeps balances, each request's
// outcome and its moves as variables; here they are defs over the log
// (grist's convention: a read model is a def, so the model cannot
// disagree with itself), each request's outcome is grist's marker
// (answered, freshKey), and the ledger's posts are the post processor's
// effect steps and the ledger's own record of what it posted. Written by
// hand: grist's generator (`grist model`) is not built yet.

// The events, the bank's rules and the read model, shared by the commit
// side (wallet) and the client side (walletCalls).
module walletLib {
  import grist.* from "./grist"

  // the accounts the bank holds, each opening with INITIAL (cents); JOINT
  // share one credit line. The app states the same (wallet.go).
  pure val ACCOUNTS = Set("a", "b", "c")
  pure val JOINT = Set("a", "b")
  pure val INITIAL = 5

  pure def moved(account: str, amount: int, id: str): Event =
    { typ: "moved", strs: [account], ints: [amount], tags: Set(("account", account)), cause: ("command", id) }
  // the ledger's confirmation of the move at position at
  pure def posting(account: str, amount: int, at: int): Event =
    { typ: "posted", strs: [account], ints: [amount, at], tags: Set(("account", account)), cause: ("processor", "post") }

  // balances, keyed by account: INITIAL and its moves (0 for an account
  // the bank does not hold)
  pure def balanceOf(log: List[Event], a: str): int =
    if (ACCOUNTS.contains(a))
      log.foldl(INITIAL, (s, e) => if (e.typ == "moved" and e.strs[0] == a) s + e.ints[0] else s)
    else 0

  // what a transfer from src may spend: its own balance, or its credit
  // line's
  pure def funds(log: List[Event], src: str): int =
    if (JOINT.contains(src)) JOINT.fold(0, (s, a) => s + balanceOf(log, a)) else balanceOf(log, src)

  // a request the bank must refuse, whatever the balances
  pure def validTransfer(src: str, dest: str, amount: int): bool =
    ACCOUNTS.contains(src) and ACCOUNTS.contains(dest) and src != dest and amount > 0
  pure def validDeposit(account: str, amount: int): bool =
    ACCOUNTS.contains(account) and amount > 0

  // what a transfer's rule reads: the source's moves, or every account's
  // of its credit line. A committed step's read set must cover each
  // (grist's covers), so a decision that reads one account of the line
  // and not the other is caught at every commit, not only when a race
  // hits it.
  pure def moveRead(a: str): Read = { typ: "moved", tags: Set(("account", a)) }
  pure def needs(src: str): Set[Read] =
    if (JOINT.contains(src)) JOINT.map(a => moveRead(a)) else Set(moveRead(src))

  pure def transferMoves(src: str, dest: str, amount: int, id: str): List[Event] =
    [moved(src, -amount, id), moved(dest, amount, id)]

  // no account outside the credit line goes negative, nor the line in sum
  pure def noOverdraftIn(log: List[Event]): bool =
    ACCOUNTS.forall(a => JOINT.contains(a) or balanceOf(log, a) >= 0) and
    JOINT.fold(0, (s, a) => s + balanceOf(log, a)) >= 0
}

// The commit side: every step recorded where it took effect (commits
// relayed from the store's step log, refusals at their commit, queries on
// return, effects, and the ledger's posts at the ledger).
module wallet {
  import grist.* from "./grist"
  import walletLib.*
  export grist.*
  export walletLib.*

  var log: List[Event]
  // each processor's checkpoint: the last trigger it handled
  var checkpoints: str -> int
  // the batch the last step appended
  var appended: List[Event]
  // the effect steps: (processor, trigger, key, ok)
  var effects: List[Effect]
  // the command ids answered so far, by a commit or a committed refusal
  var answered: Set[str]
  // the ledger's posts, as it recorded them (key, account, amount)
  var ledger: List[(str, str, int)]

  action init = all {
    log' = [],
    checkpoints' = Map("post" -> 0),
    appended' = [],
    effects' = [],
    answered' = Set(),
    ledger' = [],
  }

  action appends(b: List[Event], id: str): bool = all {
    log' = log.concat(b),
    appended' = b,
    checkpoints' = checkpoints,
    effects' = effects,
    answered' = answered.union(Set(id)),
    ledger' = ledger,
  }

  action refuses(readPos: int, id: str): bool = all {
    readPos <= log.length(),
    appends([], id),
  }

  action readsThrough(readPos: int): bool = all {
    log.length() == readPos,
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects,
    answered' = answered,
    ledger' = ledger,
  }

  // A transfer: an invalid request refused before anything is read; a
  // valid one's read set covers its funds' accounts, and it commits its
  // moves while the funds cover it, else refuses. Every committed outcome
  // needs its id fresh (a retry answers from the marker and records
  // nothing) and its condition to have held; a conflict needs it not to.
  action transfer(src: str, dest: str, amount: int, id: str, readPos: int, reads: List[Read], result: str): bool =
    if (result == "idempotency-key-reused") all { answered.contains(id), readsThrough(readPos) }
    else if (result == "conflict") all {
      freshKey(answered, id),
      validTransfer(src, dest, amount),
      needs(src).forall(n => covers(reads, n)),
      not(conditionHolds(log, readPos, reads)),
      refuses(readPos, id),
    } else all {
      freshKey(answered, id),
      conditionHolds(log, readPos, reads),
      if (not(validTransfer(src, dest, amount))) all { result == "invalid", refuses(readPos, id) }
      else all {
        needs(src).forall(n => covers(reads, n)),
        if (funds(log, src) >= amount) all { result == "ok", appends(transferMoves(src, dest, amount, id), id) }
        else all { result == "insufficient-funds", refuses(readPos, id) },
      },
    }

  // A deposit reads nothing: refused when invalid, else its move.
  action deposit(account: str, amount: int, id: str, readPos: int, reads: List[Read], result: str): bool =
    if (result == "idempotency-key-reused") all { answered.contains(id), readsThrough(readPos) }
    else all {
      freshKey(answered, id),
      conditionHolds(log, readPos, reads),
      if (validDeposit(account, amount)) all { result == "ok", appends([moved(account, amount, id)], id) }
      else all { result == "invalid", refuses(readPos, id) },
    }

  // a balance read: it reads through readPos and changes nothing; its
  // answer is checked against balanceOf (the binding's expect)
  action read(account: str, readPos: int): bool = readsThrough(readPos)

  // the processors' steps change neither the commands' keys nor the ledger
  action processes(b: List[Event], p: str, trigger: int): bool = all {
    log' = log.concat(b),
    appended' = b,
    checkpoints' = checkpoints.set(p, trigger),
    effects' = effects,
    answered' = answered,
    ledger' = ledger,
  }

  // post's effect (the ledger's post), at least once: any number of tries
  // for the next move before its checkpoint, every one under the
  // trigger's key
  action postEffect(trigger: int, key: str, ok: bool): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("post"), "moved"),
    keyStable(effects, "post", trigger, key),
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects.append(("post", trigger, key, ok)),
    answered' = answered,
    ledger' = ledger,
  }

  // post's checkpoint, only after effect steps: the move's posting after
  // an ok one, grist.failed after EFFECT_TRIES failed ones
  action post(trigger: int, ok: bool): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("post"), "moved"),
    atLeastOnceDone(effects, "post", trigger, ok, EFFECT_TRIES),
    processes(atLeastOnceOutcome("post", trigger, ok,
      [posting(log[trigger - 1].strs[0], log[trigger - 1].ints[0], trigger)]), "post", trigger),
  }

  // post is at least once: an outcome step of its own (an at-most-once
  // post) is no step of this model
  action postOutcome(trigger: int, ok: bool): bool = all {
    false,
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects,
    answered' = answered,
    ledger' = ledger,
  }

  // the ledger's post, recorded where it is made: once per key (the
  // ledger deduplicates; a ledger that does not posts a retried move
  // twice, which no step of this model is)
  action ledgerPost(key: str, account: str, amount: int): bool = all {
    ledger.select(p => p._1 == key).length() == 0,
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects,
    answered' = answered,
    ledger' = ledger.append((key, account, amount)),
  }

  // the model on its own (gotogether simulate)
  action step = {
    nondet src = Set("a", "b", "c", "z").oneOf()
    nondet dest = Set("a", "b", "c").oneOf()
    nondet amount = (-1).to(6).oneOf()
    nondet id = Set("i1", "i2", "i3", "i4").oneOf()
    nondet key = Set("k1", "k2", "k3").oneOf()
    nondet ok = Bool.oneOf()
    val reads = needs(src).fold([], (l, r) => l.append(r))
    any {
      transfer(src, dest, amount, id, log.length(), if (validTransfer(src, dest, amount)) reads else [],
        if (not(validTransfer(src, dest, amount))) "invalid" else if (funds(log, src) >= amount) "ok" else "insufficient-funds"),
      deposit(src, amount, id, log.length(), [], if (validDeposit(src, amount)) "ok" else "invalid"),
      read(src, log.length()),
      postEffect(nextOf(log, checkpoints.get("post"), "moved"), key, ok),
      post(nextOf(log, checkpoints.get("post"), "moved"), ok),
      ledgerPost(key, src, amount),
    }
  }

  // the design's: no overdraft, in any state
  val noOverdraft = noOverdraftIn(log)

  // eventually (after a settle): every move was posted (an ok effect
  // step, checkpointed), or its post failed every try and says so
  val allPosted =
    log.indices().forall(i => log[i].typ == "moved" implies and {
      checkpoints.get("post") >= i + 1,
      effectsOf(effects, "post", i + 1, true) > 0 or holds(log, failed("post", i + 1)),
    })

  // eventually: every post the ledger made is a move's, under that move's
  // effect key (the ledger records before its answer is the effect's
  // step, so this is judged after the settle)
  val postedApplied =
    ledger.foldl(true, (b, p) => b and effects.indices().exists(j => and {
      effects[j]._1 == "post",
      effects[j]._3 == p._1,
      log[effects[j]._2 - 1].strs[0] == p._2,
      log[effects[j]._2 - 1].ints[0] == p._3,
    }))

  // test-only (wallet.exactly.binding.yaml), eventually: the ledger holds
  // every move exactly once, under its effect key. At least once allows a
  // post given up after EFFECT_TRIES failures (grist.failed); a run
  // without ledger faults must not have one.
  val everyMovePostedOnce =
    log.indices().forall(i => log[i].typ == "moved" implies
      effects.indices().exists(j => effects[j]._1 == "post" and effects[j]._2 == i + 1 and
        ledger.select(p => p._1 == effects[j]._3).length() == 1))
}

// The client side: every call recorded where a client saw it (actor
// <tenant>/clients), linearized by qtest.Linearize: no positions, no
// processors, the log the commands' moves alone. A key's first answer is
// final here too.
module walletCalls {
  import grist.* from "./grist"
  import walletLib.*
  export grist.*
  export walletLib.*

  var log: List[Event]
  var answered: Set[str]

  action init = all { log' = [], answered' = Set() }

  action answers(b: List[Event], id: str): bool = all {
    freshKey(answered, id),
    log' = log.concat(b),
    answered' = answered.union(Set(id)),
  }

  action transfer(src: str, dest: str, amount: int, id: str, result: str): bool =
    if (result == "conflict") all { validTransfer(src, dest, amount), answers([], id) }
    else if (not(validTransfer(src, dest, amount))) all { result == "invalid", answers([], id) }
    else if (funds(log, src) >= amount) all { result == "ok", answers(transferMoves(src, dest, amount, id), id) }
    else all { result == "insufficient-funds", answers([], id) }

  action deposit(account: str, amount: int, id: str, result: str): bool =
    if (validDeposit(account, amount)) all { result == "ok", answers([moved(account, amount, id)], id) }
    else all { result == "invalid", answers([], id) }

  action read(account: str): bool = all { log' = log, answered' = answered }

  action step = {
    nondet src = Set("a", "b", "c", "z").oneOf()
    nondet dest = Set("a", "b", "c").oneOf()
    nondet amount = (-1).to(6).oneOf()
    nondet id = Set("i1", "i2", "i3").oneOf()
    any {
      transfer(src, dest, amount, id,
        if (not(validTransfer(src, dest, amount))) "invalid" else if (funds(log, src) >= amount) "ok" else "insufficient-funds"),
      deposit(src, amount, id, if (validDeposit(src, amount)) "ok" else "invalid"),
      read(src),
    }
  }

  val noOverdraft = noOverdraftIn(log)
}
