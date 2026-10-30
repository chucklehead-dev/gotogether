package inventory

import "github.com/chucklehead-dev/gotogether/grist"

// Allocation is the inventory owner's current allocation and terminal decision.
type Allocation struct {
	Units  int    `json:"units"`
	Status string `json:"status"`
}

// AllocationQuery observes the current durable decision without changing it.
// An unknown order is explicit rather than an ambiguous empty status.
var AllocationQuery = grist.NewQuery(App, "allocation", func(r *grist.Reads, q Order) (Allocation, error) {
	s := Allocations.For(r, q.Order)
	if s.Status == "" {
		s.Status = "none"
	}
	return Allocation{Units: s.Units, Status: s.Status}, nil
}, grist.Authorize(grist.Authenticated))
