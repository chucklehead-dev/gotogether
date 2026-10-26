// -*- mode: Bluespec; -*-
// The kvstore example's key-value service: puts, deletes, reads, and
// compare-and-swap on a store-wide revision (like etcd's). Every write
// gets the next revision; an entry remembers the revision that wrote it;
// CAS succeeds only if the key's current version is the expected one.
module kv {
  const KEYS: Set[str]
  const VALUES: Set[str]

  type Entry = { value: str, version: int }
  type Outcome = Ok | Conflict | Missing

  var store: str -> Entry
  var revision: int

  action init = all { store' = Map(), revision' = 0 }

  action write(key: str, value: str): bool = all {
    KEYS.contains(key),
    store' = store.put(key, { value: value, version: revision + 1 }),
    revision' = revision + 1,
  }

  // (write is the put operation: `put` and `set` are Quint built-ins.)
  // The outcome is a parameter: the implementation reports it, and the
  // model allows exactly one outcome for each state.
  action cas(key: str, expected: int, value: str, o: Outcome): bool = all {
    KEYS.contains(key),
    if (store.keys().contains(key) and store.get(key).version == expected)
      all { o == Ok, store' = store.put(key, { value: value, version: revision + 1 }), revision' = revision + 1 }
    else
      all { o == Conflict, store' = store, revision' = revision },
  }

  action del(key: str, o: Outcome): bool = all {
    KEYS.contains(key),
    if (store.keys().contains(key))
      all { o == Ok, store' = store.keys().exclude(Set(key)).mapBy(k => store.get(k)), revision' = revision + 1 }
    else
      all { o == Missing, store' = store, revision' = revision },
  }

  // A read changes nothing; what it returned is checked by the binding.
  action read(key: str): bool = all { KEYS.contains(key), store' = store, revision' = revision }

  // A rotated log's head (the binding's snapshot): the whole state, as
  // the store recorded it when it started the log. Values and versions
  // are two maps over the same keys (a recorded map renders as a Quint
  // map of plain values); every version is at most the revision.
  action restore(values: str -> str, versions: str -> int, rev: int): bool = all {
    values.keys() == versions.keys(),
    versions.keys().forall(k => versions.get(k) >= 1 and versions.get(k) <= rev),
    store' = values.keys().mapBy(k => { value: values.get(k), version: versions.get(k) }),
    revision' = rev,
  }

  action step = {
    nondet key = KEYS.oneOf()
    nondet value = VALUES.oneOf()
    nondet expected = 0.to(revision).oneOf()
    any {
      write(key, value),
      cas(key, expected, value, if (store.keys().contains(key) and store.get(key).version == expected) Ok else Conflict),
      del(key, if (store.keys().contains(key)) Ok else Missing),
      read(key),
    }
  }

  // Properties.
  val versionsAreRevisions = store.keys().forall(k => store.get(k).version >= 1 and store.get(k).version <= revision)
  val versionsUnique = store.keys().forall(a => store.keys().forall(b => a == b or store.get(a).version != store.get(b).version))
}

module kvDesign {
  import kv(KEYS = Set("a", "b"), VALUES = Set("x", "y", "z")).*
}
