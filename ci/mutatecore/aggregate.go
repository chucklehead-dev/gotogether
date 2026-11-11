package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// aggregateReceipt is the one verdict of a split campaign: the package's max
// applied once, to the survivors of every shard summed. Like a shard's
// receipt it is completion evidence, never a survivor approval or admission.
type aggregateReceipt struct {
	Version              int              `json:"version"`
	AdmissionEligible    bool             `json:"admission_eligible"`
	Limitations          []string         `json:"limitations"`
	Package              string           `json:"package"`
	Max                  int              `json:"max_survivors"`
	Shards               int              `json:"shards"`
	JobsPerShard         int              `json:"jobs_per_shard"`
	AggregateConcurrency int              `json:"aggregate_concurrency"`
	Shared               sharedIdentity   `json:"shared"`
	Executions           []shardExecution `json:"executions"`
	Total                int              `json:"total"`
	Survivors            int              `json:"survivors"`
	Survived             []sharedSource   `json:"survived"`
	GatePassed           bool             `json:"gate_passed"`
}

// shardExecution is one runner's exact execution identity, kept as recorded:
// it differs between runners and is not compared.
type shardExecution struct {
	Shard                    int             `json:"shard"`
	Receipt                  fileIdentity    `json:"receipt"`
	Journal                  fileIdentity    `json:"journal"`
	RecordedJournalPath      string          `json:"recorded_journal_path"`
	InputDigest              string          `json:"input_digest"`
	RuntimeEnvironmentDigest string          `json:"runtime_environment_digest"`
	Root                     string          `json:"root"`
	Producer                 fileIdentity    `json:"producer"`
	ExecutionTools           []fileIdentity  `json:"execution_tools"`
	WorkerExecution          executionResult `json:"worker_execution"`
	Selected                 int             `json:"selected"`
	Survivors                int             `json:"survivors"`
}

type shardEvidence struct {
	receipt  completionReceipt
	file     fileIdentity
	journal  fileIdentity
	shared   sharedIdentity
	outcomes []outcomeRecord
	start    startRecord
}

const (
	receiptSuffix = ".receipt.json"
	inputsSuffix  = ".inputs.json"
)

// runAggregate: mutatecore aggregate <package> <max> <shards> <evidence-dir>.
func runAggregate(ctx context.Context, args []string) error {
	if len(args) != 4 {
		return errors.New(usage)
	}
	pkg, dir := args[0], args[3]
	max, err := strconv.Atoi(args[1])
	if err != nil || max < 0 {
		return errors.New("aggregate: max-survivors must be nonnegative")
	}
	shards, err := strconv.Atoi(args[2])
	if err != nil || shards < 2 {
		return errors.New("aggregate: shards must be 2 or more (an unsharded campaign applies its own max)")
	}
	receipt, err := aggregate(pkg, max, shards, dir)
	if err != nil {
		return fmt.Errorf("aggregate %s: %w", pkg, err)
	}
	out := filepath.Join(dir, strings.ReplaceAll(pkg, "/", "-")+".aggregate.json")
	if err := publishJSONContext(ctx, out, receipt); err != nil {
		return fmt.Errorf("seal aggregate receipt: %w", err)
	}
	for _, s := range receipt.Survived {
		fmt.Printf("survived: %s [file ordinal %d]\n", s.Descriptor, s.Ordinal)
	}
	fmt.Printf("%s: %d of %d mutants survived %d test suites, across %d shards (%d x JOBS=%d = %d concurrent source mutants)\n", pkg, receipt.Survivors, receipt.Total, len(receipt.Shared.Suites), shards, shards, receipt.JobsPerShard, receipt.AggregateConcurrency)
	fmt.Fprintf(os.Stderr, "mutation aggregate receipt: %s (not admission eligible)\n", out)
	if !receipt.GatePassed {
		return fmt.Errorf("%d survivors across %d shards exceed limit %d: review source mutations and missing behavioral controls", receipt.Survivors, shards, max)
	}
	return nil
}

// aggregate verifies every shard's evidence under dir, and fails closed,
// naming each problem, unless it is one complete campaign: one sealed shard
// of each index, every journal sealed, one shared identity, every recorded
// outcome valid, and the shards' outcomes disjoint with their union exactly
// the full inventory. Only then is max applied, once, to the summed survivors.
func aggregate(pkg string, max, shards int, dir string) (aggregateReceipt, error) {
	var result aggregateReceipt
	receipts, unsealed, err := scanEvidence(dir)
	if err != nil {
		return result, err
	}
	var problems []string
	for _, journal := range unsealed {
		problems = append(problems, unsealedReason(journal))
	}
	var loaded []shardEvidence
	for _, path := range receipts {
		e, err := loadShard(path, pkg)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		loaded = append(loaded, e)
	}
	if len(receipts) == 0 {
		problems = append(problems, fmt.Sprintf("no sealed shard receipt (*%s) under %s: did the shards' artifacts upload?", receiptSuffix, dir))
	}
	byIndex := map[int]shardEvidence{}
	for _, e := range loaded {
		if e.shared.Shards != shards {
			problems = append(problems, fmt.Sprintf("%s: shard %s of a %d-shard campaign, not one of %d", e.file.Path, e.receipt.Shard, e.shared.Shards, shards))
			continue
		}
		if prior, ok := byIndex[e.receipt.Shard.Index]; ok {
			problems = append(problems, fmt.Sprintf("duplicate shard %s: %s and %s", e.receipt.Shard, prior.file.Path, e.file.Path))
			continue
		}
		byIndex[e.receipt.Shard.Index] = e
	}
	for i := range shards {
		if _, ok := byIndex[i]; !ok {
			problems = append(problems, fmt.Sprintf("missing shard %d/%d: no sealed receipt", i, shards))
		}
	}
	var ordered []shardEvidence
	for i := range shards {
		if e, ok := byIndex[i]; ok {
			ordered = append(ordered, e)
		}
	}
	for _, e := range ordered[min(1, len(ordered)):] {
		if block := differingBlock(ordered[0].shared, e.shared); block != "" {
			problems = append(problems, fmt.Sprintf("shard %s's shared identity differs from shard %s's: %s", e.receipt.Shard, ordered[0].receipt.Shard, block))
		}
	}
	if len(problems) > 0 {
		return result, errors.New(strings.Join(problems, "\n  "))
	}
	inventory := ordered[0].shared.Candidates
	resolved := make([][]outcomeRecord, len(ordered))
	for i, e := range ordered {
		resolved[i] = e.outcomes
	}
	problems = checkUnion(inventory, resolved)
	if len(problems) > 0 {
		slices.Sort(problems)
		return result, errors.New(strings.Join(problems, "\n  "))
	}
	result = aggregateReceipt{Version: receiptVersion, Package: pkg, Max: max, Shards: shards, Shared: ordered[0].shared}
	result.JobsPerShard = result.Shared.Config.Jobs
	result.AggregateConcurrency = shards * result.JobsPerShard
	survived := map[source]bool{}
	for _, e := range ordered {
		result.Survivors += e.receipt.Complete.Survivors
		for _, o := range e.outcomes {
			if o.Outcome == "survived" {
				survived[source{o.Source.File, o.Source.Ordinal}] = true
			}
		}
		for _, l := range e.receipt.Limitations {
			if !slices.Contains(result.Limitations, l) {
				result.Limitations = append(result.Limitations, l)
			}
		}
		r := e.receipt
		result.Executions = append(result.Executions, shardExecution{Shard: r.Shard.Index, Receipt: e.file, Journal: e.journal, RecordedJournalPath: r.Journal.Path, InputDigest: r.InputDigest, RuntimeEnvironmentDigest: r.Inputs.RuntimeEnvironmentDigest, Root: e.start.Root, Producer: r.Producer, ExecutionTools: r.ExecutionTools, WorkerExecution: r.WorkerExecution, Selected: r.Complete.Total, Survivors: r.Complete.Survivors})
		result.Total += r.Complete.Total
	}
	for _, c := range inventory.Sources {
		if survived[source{c.File, c.Ordinal}] {
			result.Survived = append(result.Survived, c)
		}
	}
	if len(result.Survived) != result.Survivors || result.Total != inventory.Count {
		return aggregateReceipt{}, fmt.Errorf("shard completions (%d survivors of %d) do not reconcile with their outcomes (%d of %d)", result.Survivors, result.Total, len(result.Survived), inventory.Count)
	}
	result.Limitations = append(result.Limitations, aggregateLimitations...)
	// The package's unchanged max, applied once, to the sum.
	result.GatePassed = result.Survivors <= max
	return result, nil
}

// checkUnion requires the shards' resolved sources to be disjoint and their
// union exactly the full inventory, by source identity (file and file-local
// ordinal, so repeated identical descriptors stay distinct) with matching
// descriptors. It is checked against the inventory each shard recomputed from
// its own suites, not against local IDs or the shards' own selection claims.
func checkUnion(inventory candidateInventory, shards [][]outcomeRecord) []string {
	var problems []string
	owner := map[source]int{}
	descriptor := map[source]string{}
	for shard, outcomes := range shards {
		for _, o := range outcomes {
			s := source{o.Source.File, o.Source.Ordinal}
			if prior, ok := owner[s]; ok {
				problems = append(problems, fmt.Sprintf("source %s ordinal %d resolved by shards %d and %d: shards are not disjoint", s.file, s.ordinal, prior, shard))
				continue
			}
			owner[s], descriptor[s] = shard, o.Source.Descriptor
		}
	}
	listed := map[source]bool{}
	for _, c := range inventory.Sources {
		s := source{c.File, c.Ordinal}
		if listed[s] {
			problems = append(problems, fmt.Sprintf("inventory lists source %s ordinal %d twice", c.File, c.Ordinal))
		}
		listed[s] = true
		if _, ok := owner[s]; !ok {
			problems = append(problems, fmt.Sprintf("source %s ordinal %d (%s) resolved by no shard: the union is not the inventory", c.File, c.Ordinal, c.Descriptor))
		} else if descriptor[s] != c.Descriptor {
			problems = append(problems, fmt.Sprintf("source %s ordinal %d: outcome descriptor %q, inventory %q", c.File, c.Ordinal, descriptor[s], c.Descriptor))
		}
	}
	for s, shard := range owner {
		if !listed[s] {
			problems = append(problems, fmt.Sprintf("source %s ordinal %d resolved by shard %d is not in the inventory", s.file, s.ordinal, shard))
		}
	}
	if len(owner) != inventory.Count || len(inventory.Sources) != inventory.Count {
		problems = append(problems, fmt.Sprintf("%d sources resolved, inventory of %d", len(owner), inventory.Count))
	}
	slices.Sort(problems)
	return problems
}

// aggregateLimitations is read-only after initialization.
var aggregateLimitations = []string{
	"aggregate of shard completion receipts: the package's max is applied once to the summed survivors; no shard applies it",
	"aggregate concurrency is shards x JOBS concurrent source mutants on separate runners; per-test memory caps and timeouts are each shard's",
	"shard receipts are not authenticated: the aggregate checks their consistency with each other and with their journals, not who wrote them",
}

// scanEvidence finds every sealed receipt and every journal started without
// one (its inputs sidecar is written before the worker starts).
func scanEvidence(dir string) (receipts, unsealed []string, err error) {
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasSuffix(d.Name(), ".build-inputs") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(path, receiptSuffix):
			receipts = append(receipts, path)
		case strings.HasSuffix(path, inputsSuffix):
			journal := strings.TrimSuffix(path, inputsSuffix)
			if _, err := os.Lstat(journal + receiptSuffix); os.IsNotExist(err) {
				unsealed = append(unsealed, journal)
			} else if err != nil {
				return err
			}
		}
		return nil
	})
	slices.Sort(receipts)
	slices.Sort(unsealed)
	return receipts, unsealed, err
}

// unsealedReason says what an unsealed journal shows. None of them is a
// verdict for its sources.
func unsealedReason(journal string) string {
	b, err := os.ReadFile(journal)
	if err != nil {
		return fmt.Sprintf("%s: started without a journal or receipt (%v): the shard did not complete", journal, err)
	}
	reason := "incomplete: no completion receipt (killed, timed out or not sealed)"
	for line := range bytes.Lines(b) {
		var e struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Event {
		case "aborted":
			var d struct{ Error string }
			_ = json.Unmarshal(e.Data, &d)
			reason = "aborted (canceled or failed): " + d.Error
		case "complete":
			reason = "complete but never sealed: no receipt was published after the parent's checks"
		}
	}
	return fmt.Sprintf("%s: %s", journal, reason)
}

// readReceipt accepts only the current strict shape: a receipt must be
// exactly what this helper writes (no field missing, null, aliased,
// duplicated or unknown). There is no reader for earlier shapes.
func readReceipt(path string) (completionReceipt, fileIdentity, error) {
	var r completionReceipt
	file, err := identifyFile(path)
	if err != nil {
		return r, file, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return r, file, err
	}
	if err := readJSONFile(path, &r); err != nil {
		return r, file, err
	}
	again, err := json.Marshal(r)
	if err != nil {
		return r, file, err
	}
	if !bytes.Equal(raw, append(again, '\n')) {
		return r, file, errors.New("receipt is not in the current strict shape: rerun the shard with the current helper")
	}
	return r, file, nil
}

func loadShard(path, pkg string) (shardEvidence, error) {
	var e shardEvidence
	r, file, err := readReceipt(path)
	if err != nil {
		return e, err
	}
	e.receipt, e.file = r, file
	if r.Version != receiptVersion {
		return e, fmt.Errorf("receipt version %d, current %d", r.Version, receiptVersion)
	}
	if r.AdmissionEligible {
		return e, errors.New("receipt claims admission eligibility: completion receipts never admit or approve survivors")
	}
	start, err := decodeStart(r.Manifest.Start)
	if err != nil {
		return e, err
	}
	e.start = start
	if start.Package != pkg {
		return e, fmt.Errorf("evidence of package %s, not %s", start.Package, pkg)
	}
	if start.Shard != r.Shard {
		return e, fmt.Errorf("receipt shard %s, journal settings shard %s", r.Shard, start.Shard)
	}
	if r.WorkerExecution.validate() != nil || !r.WorkerExecution.passed() {
		return e, fmt.Errorf("worker execution %s", r.WorkerExecution)
	}
	digest, err := valueDigest(r.Inputs)
	if err != nil {
		return e, err
	}
	if r.InputDigest != r.Manifest.InputDigest || digest != r.InputDigest {
		return e, errors.New("execution identity does not match its recorded inputs")
	}
	journal := strings.TrimSuffix(path, receiptSuffix)
	var sidecar provenanceContext
	if err := readJSONFile(journal+inputsSuffix, &sidecar); err != nil {
		return e, fmt.Errorf("inputs sidecar: %w", err)
	}
	if d, err := valueDigest(sidecar); err != nil || d != r.InputDigest {
		return e, errors.New("inputs sidecar differs from the receipt's execution identity")
	}
	e.journal, err = identifyFile(journal)
	if err != nil {
		return e, fmt.Errorf("journal: %w", err)
	}
	if e.journal.SHA256 != r.Journal.SHA256 || e.journal.Size != r.Journal.Size {
		return e, errors.New("journal differs from the one its receipt sealed")
	}
	complete, outcomes, err := validateJournalContent(journal, r.Manifest)
	if err != nil {
		return e, fmt.Errorf("journal: %w", err)
	}
	if complete != r.Complete {
		return e, errors.New("receipt and journal completion disagree")
	}
	e.outcomes = outcomes
	if r.Shared == nil {
		return e, fmt.Errorf("no shared identity: %s", r.SharedUnavailable)
	}
	if e.shared, err = sharedIdentityOf(r); err != nil {
		return e, err
	}
	if block := differingBlock(e.shared, *r.Shared); block != "" {
		return e, fmt.Errorf("recorded shared identity differs from its own evidence: %s", block)
	}
	return e, nil
}
