package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// PartitionInfo identifies one partition without parsing a human display name.
// Value preserves the recorded JSON type (for example, 1 differs from "1").
// Instance is a receipt-local table ID, omitted on a standalone actor receipt.
type PartitionInfo struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Instance int    `json:"instance,omitempty"`
}

// UnmarshalJSON retains numeric partition identities exactly when a parent
// navigation/report decoder otherwise defaults interface values to float64.
func (p *PartitionInfo) UnmarshalJSON(data []byte) error {
	type plain PartitionInfo
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode((*plain)(p))
}

// ConstantSubstitution records an effective replay literal and why replacing the
// original whole-history value was safe on this execution's evaluated prefixes.
type ConstantSubstitution struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Basis string `json:"basis"` // unread_by_evaluated_prefixes
}

// PartitionInstance describes one source actor's partitioned judgment. Large
// original literals live here once, rather than once per partition result.
// Factoring covers this execution's evaluated prefixes, not other executions.
type PartitionInstance struct {
	ID                  int                    `json:"id"`
	SourceActor         string                 `json:"sourceActor"`
	Key                 string                 `json:"key"`
	Factoring           string                 `json:"factoring"` // checked_on_evaluated_prefixes
	OriginalConstants   map[string]string      `json:"originalConstants,omitempty"`
	ReplaySubstitutions []ConstantSubstitution `json:"replaySubstitutions,omitempty"`
}

// Clone owns all partition identity storage, retaining exact recorded values.
func (p *PartitionInfo) Clone() *PartitionInfo {
	if p == nil {
		return nil
	}
	out := *p
	if p.Value != nil {
		out.Value = (argumentCloner{seen: map[argumentRef]reflect.Value{}}).clone(reflect.ValueOf(p.Value)).Interface()
	}
	return &out
}

// Clone owns the instance table's maps and substitutions. ID remains local to
// the same receipt; a merge assigns a new ID rather than matching actor names.
func (p *PartitionInstance) Clone() *PartitionInstance {
	if p == nil {
		return nil
	}
	out := *p
	out.OriginalConstants = maps.Clone(p.OriginalConstants)
	out.ReplaySubstitutions = slices.Clone(p.ReplaySubstitutions)
	return &out
}

// ReplayConstants derives the effective instance without duplicating original
// literals on the wire. It returns an independently mutable map.
func (p PartitionInstance) ReplayConstants() map[string]string {
	out := maps.Clone(p.OriginalConstants)
	if len(p.ReplaySubstitutions) > 0 && out == nil {
		out = map[string]string{}
	}
	for _, sub := range p.ReplaySubstitutions {
		out[sub.Name] = sub.Value
	}
	return out
}

// String renders the same partition provenance as JSON, with bounded literal
// display. The full exact literals remain in the structured receipt.
func (p PartitionInstance) String() string {
	var b strings.Builder
	source := p.SourceActor
	if source == "" {
		source = "default"
	}
	fmt.Fprintf(&b, "partition instance %d: source actor %q, key %s; factoring checked on evaluated prefixes of this run", p.ID, source, p.Key)
	if p.Factoring != "checked_on_evaluated_prefixes" {
		b.Reset()
		fmt.Fprintf(&b, "partition instance %d: source actor %q, key %s; factoring %s", p.ID, source, p.Key, p.Factoring)
	}
	var original []string
	for _, name := range slices.Sorted(maps.Keys(p.OriginalConstants)) {
		original = append(original, name+" = "+partitionLiteral(p.OriginalConstants[name]))
	}
	if len(original) > 0 {
		fmt.Fprintf(&b, "\n  original whole-history instance: %s", strings.Join(original, ", "))
	}
	if len(p.ReplaySubstitutions) == 0 {
		b.WriteString("\n  effective replay instance: original values (no substitutions)")
	} else {
		for _, sub := range p.ReplaySubstitutions {
			basis := strings.ReplaceAll(sub.Basis, "_", " ")
			fmt.Fprintf(&b, "\n  effective replay override: %s = %s (%s, this run only)", sub.Name, partitionLiteral(sub.Value), basis)
		}
	}
	return b.String()
}

func partitionLiteral(value string) string {
	chars := []rune(value)
	if len(chars) <= 180 {
		return value
	}
	return string(chars[:180]) + "… (full literal in structured receipt)"
}

// JudgmentCounts distinguishes known unique source actor identities from model
// judgments. Actors is a lower bound when UnknownSourceJudgments is nonzero.
// Partitions count partition judgments, not source actors.
type JudgmentCounts struct {
	Actors                 int `json:"actors"`
	Judgments              int `json:"judgments"`
	Partitions             int `json:"partitions"`
	UnknownSourceJudgments int `json:"unknownSourceJudgments,omitempty"`
}

// Counts derives scope from typed source identities, never partition labels.
func (s Summary) Counts() JudgmentCounts { return judgmentCounts(s.Actors) }

// Counts derives the scope of this component's own history.
func (h History) Counts() JudgmentCounts { return judgmentCounts(h.Actors) }

func judgmentCounts(actors []Actor) JudgmentCounts {
	out := JudgmentCounts{Judgments: len(actors)}
	sources := map[string]bool{}
	for _, actor := range actors {
		partitioned := actor.PartitionInfo != nil || actor.Partition != ""
		if partitioned {
			out.Partitions++
		}
		source := actor.SourceActor
		if source == "" && actor.PartitionInfo == nil && actor.Partition != "" {
			out.UnknownSourceJudgments++
			continue // a display label is not source-identity evidence
		}
		if source == "" && actor.PartitionInfo == nil {
			source = actor.Actor
		}
		if source == "" {
			source = "default"
		}
		sources[source] = true
	}
	out.Actors = len(sources)
	return out
}

// MarshalJSON emits derived counts so editing actor storage cannot leave a
// stale cached count that disagrees with the judgment scope.
func (s Summary) MarshalJSON() ([]byte, error) {
	type plain Summary
	return json.Marshal(struct {
		plain
		Counts JudgmentCounts `json:"counts"`
	}{plain(s), s.Counts()})
}

// MarshalJSON emits this history's derived counts independently of any outer
// composition or navigation cursor.
func (h History) MarshalJSON() ([]byte, error) {
	type plain History
	return json.Marshal(struct {
		plain
		Counts JudgmentCounts `json:"counts"`
	}{plain(h), h.Counts()})
}

func scopeQuantity(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// partitionScopeText is shared by primary and component history rendering.
func partitionScopeText(actors []Actor, instances []PartitionInstance) string {
	var b strings.Builder
	counts := judgmentCounts(actors)
	if counts.Partitions > 0 || counts.Judgments != counts.Actors {
		actorLabel := "source actor"
		if counts.UnknownSourceJudgments > 0 {
			actorLabel = "known source actor"
		}
		fmt.Fprintf(&b, "  scope: %s, %s, %s total\n", scopeQuantity(counts.Actors, actorLabel), scopeQuantity(counts.Partitions, "partition judgment"), scopeQuantity(counts.Judgments, "judgment"))
		if counts.UnknownSourceJudgments > 0 {
			fmt.Fprintf(&b, "  source identities unavailable for %s\n", scopeQuantity(counts.UnknownSourceJudgments, "judgment"))
		}
	}
	for _, instance := range instances {
		for _, line := range strings.Split(instance.String(), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return b.String()
}
