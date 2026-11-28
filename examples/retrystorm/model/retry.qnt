// -*- mode: Bluespec; -*-
// A retry budget: a token bucket that new requests fill and retries drain.
// The requirement is the bound on amplification: over a run, retries are
// at most RATIO/1000 of the requests (the bucket starts empty), and the
// bucket never holds more than CAP.
module retry {
  const RATIO: int // milli-tokens per request
  const CAP: int   // the most milli-tokens kept
  // neither is negative: Veil proves bounded and capped for every RATIO
  // and CAP these allow (assumes of the main module; DESIGN §116)
  assume ratioNat = RATIO >= 0
  assume capNat = CAP >= 0

  pure val UNIT = 1000

  var tokens: int
  // ghost counters for the amplification bound
  var requests: int
  var retries: int

  action init = all { tokens' = 0, requests' = 0, retries' = 0 }

  action request = all {
    tokens' = if (tokens + RATIO > CAP) CAP else tokens + RATIO,
    requests' = requests + 1,
    retries' = retries,
  }

  // a retry the budget admits
  action retry = all {
    tokens >= UNIT,
    tokens' = tokens - UNIT,
    retries' = retries + 1,
    requests' = requests,
  }

  // a retry the budget refuses: nothing changes
  action refuse = all {
    tokens < UNIT,
    tokens' = tokens, requests' = requests, retries' = retries,
  }

  action step = any { request, retry, refuse }

  val bounded = UNIT * retries <= RATIO * requests
  val capped = tokens <= CAP

  run burstTest =
    init
      .then(10.reps(_ => request))
      .then(retry)
      .then(refuse)
      .expect(tokens == 0 and retries == 1)
}

// What makes bounded inductive (simulate.inductive, qtest.Inductive): the
// tokens left are what the requests earned and the retries did not spend.
module retryProof {
  import retry.*
  // retry's assumes, restated: Veil reads only the main module's
  assume ratioNatProof = RATIO >= 0
  assume capNatProof = CAP >= 0
  val tokensAccounted = tokens >= 0 and UNIT * retries + tokens <= RATIO * requests
}

// The budget as deployed: a retry per 10 requests, at most 10 kept.
module retryDesign {
  import retry(RATIO = 100, CAP = 10000).*
}
