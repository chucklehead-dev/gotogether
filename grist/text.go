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
// holds, so that "<tenant>/clients" (a run's client-side actor,
// gristsim.ClientsActor) names no tenant. A node relays no tenant it
// refuses (Relay.Err names it), and a command or query to one is refused.
func CheckTenant(t string) error {
	if t == "" {
		return fmt.Errorf("grist: an empty tenant name: name every tenant")
	}
	if err := checkText("tenant", t); err != nil {
		return err
	}
	if strings.ContainsRune(t, '/') {
		return fmt.Errorf("grist: tenant %q holds a slash, which grist keeps for actors beside a tenant's (<tenant>/clients): name tenants without one", t)
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
	return checkText("id", env.ID)
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
