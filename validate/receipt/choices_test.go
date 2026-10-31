package receipt

import (
	"strings"
	"testing"
)

func TestChoicesDoNotDisappearWithPassingPartitions(t *testing.T) {
	s := Summary{Status: Pass, Scope: "collected_history", Actors: []Actor{{Actor: "a", Partition: "key=x", Status: Pass, Ordering: "reconstructed", Choices: &ChoiceSearch{Status: "matched", Limit: 10, Explored: 2, Exhaustive: true, Confirmation: "path_confirmed"}}}}
	if got := s.String(); !strings.Contains(got, "model choices: matched") {
		t.Fatal(got)
	}
}

func TestChoicesNegativeProofDoesNotClaimQuintExhaustiveness(t *testing.T) {
	c := &ChoiceSearch{Status: "rejected", Limit: 100, Explored: 8, Exhaustive: true, Confirmation: "not_performed"}
	if got := c.String(); !strings.Contains(got, "exhaustive Go search") || strings.Contains(got, "confirmed by Quint") {
		t.Fatal(got)
	}
}
