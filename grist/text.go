package grist

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// checkText is s's error as a what grist stores: PostgreSQL's TEXT
// refuses a NUL byte and invalid UTF-8 (22021), where SQLite and the
// in-memory stores take them, and encoding/json turns invalid UTF-8 into
// U+FFFD, so a tag written raw and read back from its JSON would differ.
// grist refuses them on every tier alike, before any store sees them.
func checkText(what, s string) error {
	switch {
	case strings.IndexByte(s, 0) >= 0:
		return fmt.Errorf("grist: %s %q holds a NUL byte, which PostgreSQL cannot store: give text without one (encode binary data, e.g. as hex or base64)", what, s)
	case !utf8.ValidString(s):
		return fmt.Errorf("grist: %s %q is not valid UTF-8, which PostgreSQL cannot store and JSON rewrites: give valid UTF-8 (encode binary data, e.g. as hex or base64)", what, s)
	}
	return nil
}

// CheckTenant is the error of a tenant name grist refuses, naming the
// fix; nil for a name it takes: non-empty, valid UTF-8 without NUL (what
// every store holds alike) and without a slash, which no tenant name
// holds, so that a step's actor (<app>/<tenant>, and a run's client side
// <app>/<tenant>/clients: gristsim.ClientsActor) names one tenant. A node relays no tenant it
// refuses (Relay.Err names it), and a command or query to one is refused.
func CheckTenant(t string) error {
	if t == "" {
		return fmt.Errorf("grist: an empty tenant name: name every tenant")
	}
	if err := checkText("tenant", t); err != nil {
		return err
	}
	if strings.ContainsRune(t, '/') {
		return fmt.Errorf("grist: tenant %q holds a slash, which grist keeps for its actors (<app>/<tenant>, <app>/<tenant>/clients): name tenants without one", t)
	}
	return nil
}

// checkEnvelope is env's error for a command or query: its tenant's, or
// its id's (the idempotency key, stored as the marker's id and the
// events' cause).
func checkEnvelope(env Envelope) error {
	if err := CheckTenant(env.Tenant); err != nil {
		return err
	}
	return checkID(env.ID)
}

// reservedID is the prefix of the ids grist gives its own markers (an
// at-most-once effect's outcome, keyed by its effect key): no command
// takes one.
const reservedID = "grist:"

// maxIDBytes is the longest command id (an Idempotency-Key) grist takes,
// in bytes: an id is a marker's key and every event's cause, indexed by
// every store, and PostgreSQL's btree refuses an index row past about
// 2.7 kB (54000), which a retry would never fix. 255, as the IETF
// Idempotency-Key draft's examples and most APIs bound it.
const maxIDBytes = 255

// checkID is the error of a command id (an idempotency key) grist
// refuses: text no store holds alike, longer than maxIDBytes, or an id of
// grist's own.
func checkID(id string) error {
	if err := checkText("id", id); err != nil {
		return err
	}
	if len(id) > maxIDBytes {
		return fmt.Errorf("grist: an id of %d bytes, past grist's bound of %d: give a shorter id (a UUID, or a hash of a longer one)", len(id), maxIDBytes)
	}
	if strings.HasPrefix(id, reservedID) {
		return fmt.Errorf("grist: id %q starts with %q, which grist keeps for its own markers: choose another id", id, reservedID)
	}
	return nil
}

// checkEvents is the error of an event a block emits whose tag value no
// store holds alike.
func checkEvents(block string, events []Event) error {
	for _, e := range events {
		for _, t := range e.Tags {
			if err := checkText(fmt.Sprintf("%s's event %s: tag %s", block, e.Type, t.Key), t.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// modelText is s as a model's string can hold it, its double quotes made
// single: a Quint string literal has no escapes, so a recorded text with
// a double quote (an error quoting a URL, as net/http's do) fails every
// validation of its run. What grist records and stores of an error (an
// unknown outcome's) goes through it, both alike, so the model computes
// the event from the step.
func modelText(s string) string { return strings.ReplaceAll(s, `"`, "'") }
