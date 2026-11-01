// Package counted observes the real Goo executor through its HTTP source seam.
// It contains no classification oracle: the separate model judges the observations.
package counted

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"

	"github.com/casselc/goo/exploration"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type Case struct {
	Name   string   `json:"name"`
	Total  string   `json:"total"`
	Groups []string `json:"groups"`
}

type Observation struct {
	Case   Case                             `json:"case"`
	Result *exploration.CountedValuesResult `json:"result"`
	Reads  int                              `json:"reads"`
}

func LoadCases(path string) ([]Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []Case
	if err = json.Unmarshal(b, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

type transport func(*http.Request) (*http.Response, error)

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }

func canonicalNumber(text string) (json.Number, error) {
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != text {
		return "", fmt.Errorf("noncanonical uint64 count %q", text)
	}
	return json.Number(text), nil
}
func integerList(texts []string) (string, error) {
	nums := make([]json.Number, len(texts))
	for i, s := range texts {
		n, err := canonicalNumber(s)
		if err != nil {
			return "", err
		}
		nums[i] = n
	}
	b, err := json.Marshal(nums)
	return string(b), err
}
func stringList(texts []string) string {
	if texts == nil {
		texts = []string{}
	}
	b, _ := json.Marshal(texts)
	return string(b)
}

// Observe calls FetchCountedValues once per case. Each source response is served
// at the actual HTTP boundary; unexpected requests or executor errors abort the
// run rather than manufacturing an observed model result.
func Observe(ctx context.Context, cases []Case) ([]qtrace.Step, []Observation, error) {
	steps := make([]qtrace.Step, 0, len(cases))
	observations := make([]Observation, 0, len(cases))
	for i, c := range cases {
		total, err := canonicalNumber(c.Total)
		if err != nil {
			return nil, nil, err
		}
		groups, err := integerList(c.Groups)
		if err != nil {
			return nil, nil, err
		}
		calls := 0
		client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			calls++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			if r.Method != "POST" || r.URL.Query().Get("readonly") != "1" || r.URL.Query().Get("param_environment") != "ordinary" || !strings.Contains(string(body), "otel_traces") {
				return nil, fmt.Errorf("unexpected typed source request")
			}
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", "application/json")
			switch calls {
			case 1:
				b, _ := json.Marshal(map[string]string{"count": c.Total})
				fmt.Fprintln(w, string(b))
			case 2:
				for j, count := range c.Groups {
					b, _ := json.Marshal(map[string]string{"g0": fmt.Sprintf("value-%d", j), "count": count})
					fmt.Fprintln(w, string(b))
				}
			default:
				return nil, fmt.Errorf("unexpected source read %d", calls)
			}
			return w.Result(), nil
		})}
		query := exploration.Query{Signal: "spans", Environment: "ordinary", FromMS: 1000, ToMS: 61000, Limit: 17, GroupBy: []exploration.Field{{Kind: "attribute", Key: "work.category"}}}
		actual, err := (exploration.Source{Endpoint: "http://counted.invalid", Client: client}).FetchCountedValues(ctx, query)
		if err != nil {
			return nil, nil, fmt.Errorf("%s executor: %w", c.Name, err)
		}
		if calls != 2 {
			return nil, nil, fmt.Errorf("%s made %d reads, want two", c.Name, calls)
		}
		count, err := canonicalNumber(actual.Count)
		if err != nil {
			return nil, nil, err
		}
		actualGroups := make([]string, len(actual.Groups))
		for j, g := range actual.Groups {
			actualGroups[j] = g.Count
		}
		groupNumbers, err := integerList(actualGroups)
		if err != nil {
			return nil, nil, err
		}
		steps = append(steps, qtrace.Step{Action: "lookup", Seq: uint64(i + 1), Recorder: "counted", Actor: "counted", Outcome: "ok", Ref: fmt.Sprintf("case:%s", c.Name), Args: map[string]any{"total": total, "groups": groups}, Obs: map[string]any{"state": actual.State, "count": count, "groups": groupNumbers, "rawCount": actual.Count, "rawGroups": stringList(actualGroups)}})
		observations = append(observations, Observation{Case: c, Result: actual, Reads: calls})
	}
	return steps, observations, nil
}

func WriteTrace(w io.Writer, steps []qtrace.Step) error { return qtrace.WriteJSONL(w, steps) }
