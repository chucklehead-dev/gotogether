package grist

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBuiltInOutcomeProvenance(t *testing.T) {
	const triggerID = "0123456789abcdef0123456789abcdef"
	trigger := Stored{ID: triggerID, Pos: 7}
	for _, event := range []Event{failedEvent("deliver", trigger), unknownEvent("deliver", trigger, "answer lost")} {
		t.Run(event.Type, func(t *testing.T) {
			var body struct {
				Processor string `json:"processor"`
				Trigger   int64  `json:"trigger"`
				TriggerID string `json:"triggerId"`
			}
			if err := json.Unmarshal(event.Body, &body); err != nil {
				t.Fatal(err)
			}
			if body.Processor != "deliver" || body.Trigger != 7 || body.TriggerID != triggerID {
				t.Fatalf("outcome lost original trigger provenance: %+v", body)
			}
			info := failedInfo()
			wantStrings := []string{"deliver"}
			if event.Type == unknownType {
				info = unknownInfo()
				wantStrings = append(wantStrings, "answer lost")
			}
			if _, _, _, err := info.storedValue(event, 1); err != nil {
				t.Fatal(err)
			}
			strs, ints, err := scalars(info.typ, event.Body)
			if err != nil || !reflect.DeepEqual(strs, wantStrings) || !reflect.DeepEqual(ints, []int64{7}) {
				t.Fatalf("identity-opaque logical projection: strings=%v ints=%v err=%v", strs, ints, err)
			}
			for _, invalid := range []string{"", "x", "0123456789ABCDEF0123456789ABCDEF", strings.Repeat("0", 31)} {
				var fields map[string]any
				if err := json.Unmarshal(event.Body, &fields); err != nil {
					t.Fatal(err)
				}
				if invalid == "" {
					delete(fields, "triggerId")
				} else {
					fields["triggerId"] = invalid
				}
				bad := event.Clone()
				bad.Body, _ = json.Marshal(fields)
				if _, _, _, err := info.storedValue(bad, 1); err == nil || !strings.Contains(err.Error(), "triggerId") || !strings.Contains(err.Error(), "do not infer identity") {
					t.Fatalf("invalid original identity %q not refused with guidance: %v", invalid, err)
				}
			}
		})
	}
}

func TestApplicationTriggerIDIsModelData(t *testing.T) {
	// Only the two built-in runtime outcome types have opaque identity
	// provenance. An application's identically named field remains modeled.
	type applicationEvent struct {
		TriggerID string `json:"triggerId"`
	}
	strs, ints, err := scalars(reflect.TypeFor[applicationEvent](), []byte(`{"triggerId":"application value"}`))
	if err != nil || !reflect.DeepEqual(strs, []string{"application value"}) || len(ints) != 0 {
		t.Fatalf("application identity field was hidden: strings=%v ints=%v err=%v", strs, ints, err)
	}
}
