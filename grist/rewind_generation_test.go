package grist

import (
	"context"
	"testing"
)

// A store read or append admitted before a rewind can answer after resume.
// Neither answer may repopulate the new generation's rewind detector memory.
func TestRetiredProcessorObservationsAfterResume(t *testing.T) {
	for _, answer := range []string{"read", "commit"} {
		t.Run(answer, func(t *testing.T) {
			var c clocks
			ctx := c.workContext(context.Background(), "t")
			if rewound, current := c.observeWork(ctx, "t", "p", "poller", 4, 10); rewound != nil || !current {
				t.Fatalf("initial observation: rewind=%v current=%v", rewound, current)
			}
			c.stopFor("t", &RewoundError{Tenant: "t", Seen: 10, Head: 2})
			c.resume("t")
			if answer == "read" {
				if rewound, current := c.observeWork(ctx, "t", "p", "poller", 4, 10); rewound != nil || current {
					t.Fatalf("retired observation accepted: rewind=%v current=%v", rewound, current)
				}
			} else {
				c.committedWork(context.WithoutCancel(ctx), "t", "p", "poller", 4, 10)
			}
			c.mu.Lock()
			tc := c.of("t")
			contaminated := tc.head != 0 || len(tc.cps) != 0 || len(tc.marks) != 0
			c.mu.Unlock()
			if contaminated {
				t.Fatal("retired store answer repopulated resumed memory")
			}
			fresh := c.workContext(context.Background(), "t")
			if rewound, current := c.observeWork(fresh, "t", "p", "poller", 0, 2); rewound != nil || !current {
				t.Fatalf("fresh observation falsely rewound: rewind=%v current=%v", rewound, current)
			}
			if c.workCurrent(ctx, "t") {
				t.Fatal("resume revived retired work")
			}
			c.committedWork(fresh, "t", "p", "poller", 1, 3)
			c.mu.Lock()
			tc = c.of("t")
			advanced := tc.head == 3 && tc.cps["p"] == 1 && tc.marks["poller"].cp == 1
			c.mu.Unlock()
			if !advanced {
				t.Fatal("fresh commit acknowledgment did not advance memory")
			}
		})
	}
}
