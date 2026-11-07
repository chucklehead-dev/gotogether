package receipt

import (
	"strings"
	"testing"
)

func TestProtocolProblemRetainsUniversalClaimScope(t *testing.T) {
	for _, status := range []Status{Fail, Inconclusive} {
		t.Run(string(status), func(t *testing.T) {
			c := Composition{
				Local: []Component{{Target: "shop", Status: Pass, Applicable: true}},
				Protocol: Protocol{
					Component:  Component{Target: "business", Status: status, Applicable: true, Reason: "permitted ordering evidence"},
					Configured: true,
					Quantifier: "all_causal_orders",
					Checked:    2,
					Limit:      2,
				},
			}
			got, reason := c.Problem()
			if got != status || !strings.Contains(reason, "universal protocol claim (all permitted causal orders) business") {
				t.Fatalf("aggregate claim lost its scope: %s %s", got, reason)
			}
			if c.Local[0].Status != Pass || c.Protocol.Target != "business" {
				t.Fatal("aggregate rendering changed component evidence")
			}
		})
	}
}
