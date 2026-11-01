// Package membership observes Goo's real process-local tail-membership API.
// The driver records returned data; the independent model judges its contract.
package membership

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/casselc/goo/exploration"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

var epoch = time.Unix(200, 0).UTC()

// Ref names an issued slot and original row; a negative row forges a handle.
type Ref struct {
	Slot int `json:"slot"`
	Row  int `json:"row"`
}

// Case describes an authored operation, without expected answers.
type Case struct {
	Name       string   `json:"name"`
	Action     string   `json:"action"`
	Slot       int      `json:"slot,omitempty"`
	Observed   int64    `json:"observed,omitempty"`
	Now        int64    `json:"now,omitempty"`
	Connection string   `json:"connection,omitempty"`
	Selector   string   `json:"selector,omitempty"`
	Bodies     []string `json:"bodies,omitempty"`
	Limited    bool     `json:"limited,omitempty"`
	Refs       []Ref    `json:"refs,omitempty"`
}

// Member directly projects trusted input or a returned native member.
type Member struct {
	Key      string `json:"key"`
	Body     string `json:"body"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Observed int64  `json:"observed"`
	Basis    string `json:"basis"`
	Late     bool   `json:"late"`
	Limited  bool   `json:"limited"`
}

// Observation retains complete native responses before caller-side mutation.
type Observation struct {
	Refs       []exploration.TailMembershipRef       `json:"refs,omitempty"`
	Case       Case                                  `json:"case"`
	Input      *exploration.TailMembershipBatch      `json:"input,omitempty"`
	Capability *exploration.TailMembershipCapability `json:"capability,omitempty"`
	Draft      *exploration.TailSelectionDraft       `json:"draft,omitempty"`
	Error      string                                `json:"error,omitempty"`
}
type issued struct {
	batch exploration.TailMembershipBatch
	cap   exploration.TailMembershipCapability
	keys  []string
}

// LoadCases reads the authored operation sequence.
func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	err = json.Unmarshal(data, &cases)
	return cases, err
}
func copied[T any](value T) T {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var out T
	if err = json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}
func status(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, exploration.ErrTailMembershipUnavailable):
		return "unavailable"
	case errors.Is(err, exploration.ErrTailMembershipCapacity):
		return "capacity"
	default:
		return "invalid"
	}
}
func list(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func relative(t time.Time) int64 { return t.Unix() - epoch.Unix() }
func inputMembers(batch exploration.TailMembershipBatch) []Member {
	members := make([]Member, 0, len(batch.Rows))
	for _, row := range batch.Rows {
		members = append(members, Member{Key: row.Key, Body: base64.StdEncoding.EncodeToString(row.Record), From: batch.FromMS/1000 - epoch.Unix(), To: batch.ToMS/1000 - epoch.Unix(), Observed: relative(batch.ObservedAt), Late: row.Late, Limited: batch.Limited})
	}
	return members
}
func draftMembers(d *exploration.TailSelectionDraft) []Member {
	members := []Member{}
	if d != nil {
		for _, m := range d.Members {
			members = append(members, Member{Key: m.Ref.Key, Body: base64.StdEncoding.EncodeToString(m.Record), From: m.FromMS/1000 - epoch.Unix(), To: m.ToMS/1000 - epoch.Unix(), Observed: relative(m.ObservedAt), Basis: m.Basis, Late: m.Late, Limited: m.Limited})
		}
	}
	// Opaque token order is random. The projection preserves every member; the
	// original ordered native draft remains separately in Observation.Draft.
	sort.Slice(members, func(i, j int) bool { return list(members[i]) < list(members[j]) })
	return members
}

// Observe executes real API calls with an injected clock. Setup errors abort;
// domain rejections remain observations for the independent model to judge.
func Observe(ctx context.Context, cases []Case) ([]qtrace.Step, []Observation, error) {
	now := epoch
	bank := exploration.NewTailMembershipSnapshots(func() time.Time { return now })
	slots := map[int]*issued{}
	var last *exploration.TailSelectionDraft
	steps := []qtrace.Step{}
	observations := []Observation{}
	for _, c := range cases {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		args := map[string]any{}
		obs := map[string]any{}
		native := Observation{Case: c}
		var callErr error
		switch c.Action {
		case "reset":
			now = epoch
			bank = exploration.NewTailMembershipSnapshots(func() time.Time { return now })
			slots = map[int]*issued{}
			last = nil
		case "advance":
			now = epoch.Add(time.Duration(c.Now) * time.Second)
			args["now"] = c.Now
		case "issue":
			at := epoch.Add(time.Duration(c.Observed) * time.Second)
			raw := make([]json.RawMessage, len(c.Bodies))
			for i, body := range c.Bodies {
				data, err := json.Marshal(map[string]string{"Body": body, "timestamp_ns": fmt.Sprint(at.Add(-time.Second).UnixNano())})
				if err != nil {
					return nil, nil, err
				}
				raw[i] = data
			}
			tracker := exploration.TailTracker{}
			rows, err := tracker.Observe(raw)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: tracker: %w", c.Name, err)
			}
			batch := exploration.TailMembershipBatch{Connection: c.Connection, Selector: exploration.TailSelector{Environment: c.Selector}, FromMS: at.Add(-time.Minute).UnixMilli(), ToMS: at.UnixMilli(), ObservedAt: at, Rows: rows, Limited: c.Limited}
			before := copied(batch)
			native.Input = &before
			cap, err := bank.Freeze(batch)
			if cap.ExpiresAt.Nanosecond() != 0 {
				return nil, nil, fmt.Errorf("%s: capability expiry cannot be represented as whole seconds", c.Name)
			}
			callErr = err
			native.Capability = &cap
			keys := make([]string, len(rows))
			for i, row := range rows {
				keys[i] = row.Key
			}
			slots[c.Slot] = &issued{batch: batch, cap: cap, keys: keys}
			args = map[string]any{"slot": c.Slot, "observed": c.Observed, "connection": c.Connection, "selector": c.Selector, "members": list(inputMembers(before))}
			obs = map[string]any{"status": status(err), "expires": relative(cap.ExpiresAt), "basis": cap.Basis}
		case "resolve":
			refs := []exploration.TailMembershipRef{}
			slotIDs := []int{}
			keys := []string{}
			for _, r := range c.Refs {
				issue, ok := slots[r.Slot]
				if !ok {
					return nil, nil, fmt.Errorf("%s: missing authored slot %d", c.Name, r.Slot)
				}
				key := "forged"
				if r.Row >= 0 && r.Row < len(issue.keys) {
					key = issue.keys[r.Row]
				}
				refs = append(refs, exploration.TailMembershipRef{Token: issue.cap.Token, Key: key})
				slotIDs = append(slotIDs, r.Slot)
				keys = append(keys, key)
			}
			native.Refs = append([]exploration.TailMembershipRef(nil), refs...)
			draft, err := bank.Resolve(c.Connection, exploration.TailSelector{Environment: c.Selector}, refs)
			callErr = err
			if draft != nil {
				for _, member := range draft.Members {
					if member.FromMS%1000 != 0 || member.ToMS%1000 != 0 || member.ObservedAt.Nanosecond() != 0 {
						return nil, nil, fmt.Errorf("%s: member time cannot be represented as whole seconds", c.Name)
					}
				}
			}
			last = draft
			if draft != nil {
				copy := copied(*draft)
				native.Draft = &copy
			}
			conn, selector := "", ""
			if draft != nil {
				conn = draft.Connection
				selector = draft.Selector.Environment
			}
			args = map[string]any{"slots": list(slotIDs), "keys": list(keys), "connection": c.Connection, "selector": c.Selector}
			obs = map[string]any{"status": status(err), "hasDraft": draft != nil, "connection": conn, "selector": selector, "members": list(draftMembers(draft))}
		case "mutateInput":
			issue, ok := slots[c.Slot]
			if !ok {
				return nil, nil, fmt.Errorf("%s: missing mutation slot", c.Name)
			}
			for i := range issue.batch.Rows {
				if len(issue.batch.Rows[i].Record) > 0 {
					issue.batch.Rows[i].Record[0] = '!'
				}
				issue.batch.Rows[i].Key = "mutated"
			}
			issue.batch.Selector.Environment = "mutated"
			issue.batch.Limited = !issue.batch.Limited
			observations = append(observations, native)
			continue
		case "mutateDraft":
			if last == nil {
				return nil, nil, fmt.Errorf("%s: no draft to mutate", c.Name)
			}
			for i := range last.Members {
				if len(last.Members[i].Record) > 0 {
					last.Members[i].Record[0] = '!'
				}
				last.Members[i].Basis = "mutated"
				last.Members[i].Limited = !last.Members[i].Limited
			}
			last.Selector.Environment = "mutated"
			last.Connection = "mutated"
			observations = append(observations, native)
			continue
		default:
			return nil, nil, fmt.Errorf("unknown operation %q", c.Action)
		}
		if callErr != nil {
			native.Error = callErr.Error()
		}
		steps = append(steps, qtrace.Step{Action: c.Action, Seq: uint64(len(steps) + 1), Recorder: "membership", Actor: "membership", Outcome: "ok", Ref: "case:" + c.Name, Args: args, Obs: obs})
		observations = append(observations, native)
	}
	return steps, observations, nil
}

// WriteTrace writes native observed step-log bytes.
func WriteTrace(w io.Writer, steps []qtrace.Step) error { return qtrace.WriteJSONL(w, steps) }
