package validate

import "slices"

// NativeProof identifies a strictly validated positive bounded group proof used
// within one SimulateMany iteration. Digests identify the exact constructed
// request, captured sources, local tool closure and fixed configuration. This is
// provenance for local reuse, not a portable proof certificate or authentication.
type NativeProof struct {
	ProducerBinding string `json:"producerBinding"`
	RequestSHA256   string `json:"requestSHA256"`
	// SourceSHA256 covers the canonical invariant input and its full captured
	// source inventory; framework-owned goal observers are excluded.
	SourceSHA256        string   `json:"sourceSHA256"`
	ToolchainSHA256     string   `json:"toolchainSHA256"`
	ConfigurationSHA256 string   `json:"configurationSHA256"`
	Invariants          []string `json:"invariants"`
	Steps               int      `json:"steps"`
	Reused              bool     `json:"reused"`
}

// Clone owns the group's storage independently of other reports and the session.
func (p *NativeProof) Clone() *NativeProof {
	if p == nil {
		return nil
	}
	out := *p
	out.Invariants = slices.Clone(p.Invariants)
	return &out
}
