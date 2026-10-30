package runprofile

import "encoding/json"

// Phase records a named workload prerequisite stage, independently of model
// judgment. Times are true virtual durations from the simulation epoch.
type Phase struct {
	Name string `json:"name"`
	// Status is not_started, running, completed or failed.
	Status       string        `json:"status"`
	Faults       string        `json:"faults"`
	Timeout      string        `json:"timeout"`
	Started      string        `json:"started,omitempty"`
	Deadline     string        `json:"deadline,omitempty"`
	Finished     string        `json:"finished,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	Requirements []Requirement `json:"requirements"`
}

// Requirement is a driver-observed call outcome or query result. Satisfying one
// does not establish a model invariant. RequestID is the driver's identity; a
// query does not use it as an idempotency key or native trace identity.
type Requirement struct {
	Caller    string `json:"caller,omitempty"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	App       string `json:"app"`
	Tenant    string `json:"tenant"`
	Action    string `json:"action"`
	RequestID string `json:"requestId,omitempty"`
	// InFlight identifies a current request without inventing its response.
	InFlight bool `json:"inFlight"`
	// AttemptOutcome belongs to the latest logical call, which may have no answer.
	AttemptOutcome    string          `json:"attemptOutcome,omitempty"`
	ObservedRequestID string          `json:"observedRequestId,omitempty"`
	Status            string          `json:"status"`
	ExpectedOutcome   string          `json:"expectedOutcome,omitempty"`
	ObservedOutcome   string          `json:"observedOutcome,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	Expected          json.RawMessage `json:"expected,omitempty"`
	Observed          json.RawMessage `json:"observed,omitempty"`
	Attempts          int             `json:"attempts"`
}

// Clone returns independent phase observations, including query JSON buffers.
func (p Phase) Clone() Phase {
	p.Requirements = append([]Requirement{}, p.Requirements...)
	for i := range p.Requirements {
		r := &p.Requirements[i]
		r.Arguments = append(json.RawMessage(nil), r.Arguments...)
		r.Expected = append(json.RawMessage(nil), r.Expected...)
		r.Observed = append(json.RawMessage(nil), r.Observed...)
	}
	return p
}
