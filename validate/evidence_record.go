package validate

import "github.com/chucklehead-dev/gotogether/validate/diagnostic"

// Record inspects an exact captured identity without moving the model cursor.
// Counterfactual transitions never create captured records.
func (n *Navigator) Record(ref diagnostic.RecordRef) (*diagnostic.RecordDetail, error) {
	return diagnostic.LookupRecord(n.records, ref)
}
