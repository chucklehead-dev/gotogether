// Command inspect-itf checks exact native ITF values against the actual public
// responses captured by observe. It does not derive a classification verdict.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"

	counted "example.com/counted-values"
	"github.com/chucklehead-dev/gotogether/itf"
)

type row struct {
	Case   string   `json:"case"`
	Count  string   `json:"count"`
	Groups []string `json:"groups"`
	State  string   `json:"state"`
}
type summary struct {
	States       int   `json:"states"`
	Observations []row `json:"observations"`
}

func main() {
	trace := flag.String("trace", "", "native ITF trace")
	results := flag.String("results", "observations.json", "actual public responses from observe")
	flag.Parse()
	report, err := inspect(*trace, *results)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func integer(v itf.Value, want, label string) error {
	n, ok := new(big.Int).SetString(want, 10)
	if !ok || n.String() != want {
		return fmt.Errorf("%s: public count %q is not a canonical integer", label, want)
	}
	if v.Kind != itf.Int || v.I == nil {
		return fmt.Errorf("%s: native ITF value is not an integer", label)
	}
	if v.I.Cmp(n) != 0 {
		return fmt.Errorf("%s: ITF integer %s differs from public %s", label, v.I.String(), want)
	}
	return nil
}
func integers(v itf.Value, want []string, label string) error {
	if v.Kind != itf.List || len(v.Elems) != len(want) {
		return fmt.Errorf("%s: ITF list length %d differs from public %d", label, len(v.Elems), len(want))
	}
	for i, text := range want {
		if err := integer(v.Elems[i], text, fmt.Sprintf("%s[%d]", label, i)); err != nil {
			return err
		}
	}
	return nil
}
func inspect(tracePath, resultsPath string) (*summary, error) {
	if tracePath == "" {
		return nil, fmt.Errorf("-trace is required")
	}
	trace, err := itf.ReadFile(tracePath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(resultsPath)
	if err != nil {
		return nil, err
	}
	var actual []counted.Observation
	if err := json.Unmarshal(raw, &actual); err != nil {
		return nil, err
	}
	if len(actual) == 0 {
		return nil, fmt.Errorf("no actual public observations")
	}
	if len(trace.States) != len(actual)+1 {
		return nil, fmt.Errorf("ITF has %d states, want %d public observations plus initial state", len(trace.States), len(actual))
	}
	report := &summary{States: len(trace.States), Observations: make([]row, 0, len(actual))}
	for i, observation := range actual {
		if observation.Result == nil {
			return nil, fmt.Errorf("observation %d has no public result", i)
		}
		state := trace.States[i+1]
		label := observation.Case.Name
		if state.Action != "lookup" {
			return nil, fmt.Errorf("%s: ITF post-state action %q, want lookup", label, state.Action)
		}
		if err := integer(state.Vars["count"], observation.Result.Count, label+" count"); err != nil {
			return nil, err
		}
		groups := make([]string, len(observation.Result.Groups))
		for j, g := range observation.Result.Groups {
			groups[j] = g.Count
		}
		if err := integers(state.Vars["lastGroups"], groups, label+" groups"); err != nil {
			return nil, err
		}
		value := state.Vars["state"]
		if value.Kind != itf.Str || value.S != observation.Result.State {
			return nil, fmt.Errorf("%s: ITF state %q differs from public %q", label, value.S, observation.Result.State)
		}
		if err := integer(state.Picks["total"], observation.Case.Total, label+" total pick"); err != nil {
			return nil, err
		}
		if err := integers(state.Picks["groups"], observation.Case.Groups, label+" groups pick"); err != nil {
			return nil, err
		}
		report.Observations = append(report.Observations, row{Case: label, Count: observation.Result.Count, Groups: groups, State: observation.Result.State})
	}
	return report, nil
}
