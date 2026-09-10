// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// This file holds the result model the actrace tool computes once and renders
// three ways: the terminal report (the report-only / --strict surface),
// indented JSON (--json, the automation interface), and a generated, committed
// traceability matrix (--matrix, docs/acceptance/traceability.md). The model is
// populated from the same plan walk the forward gate uses, so all three views
// agree by construction. This mechanism is defined by ADR-0065.

package actrace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// matrixPath is the committed traceability matrix --matrix regenerates.
const matrixPath = "docs/acceptance/traceability.md"

// renderComputed builds the result model once and renders the requested views:
// indented JSON to stdout (the automation interface) and / or the committed
// matrix Markdown. Neither gates — the contract gate stays in --strict.
func renderComputed(root string, index map[string]string, fe feIndex, jsonOut, matrix bool) error {
	rep, err := computeReport(root, index, fe)
	if err != nil {
		return err
	}
	if matrix {
		out := filepath.Join(root, matrixPath)
		if err := os.WriteFile(out, []byte(renderMatrix(rep.Plans)), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", matrixPath, err)
		}
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
	}
	return nil
}

// AC-coverage status words. A criterion is proven when every proof it names
// resolves, missing when at least one named test/ui proof does not, method when
// it is verified by a non-test method (none / inspection / demonstration /
// manual / scenario), and unannotated when it carries no verify: field.
const (
	acStatusProven      = "proven"
	acStatusMissing     = "missing"
	acStatusMethod      = "method"
	acStatusUnannotated = "unannotated"
)

// Proof kinds: a Go test, a front-end spec (ui:<path>), or a non-test
// verification method.
const (
	proofKindTest   = "test"
	proofKindUI     = "ui"
	proofKindMethod = "method"
)

// Report is the whole-tree traceability result: per-plan coverage plus the two
// repo-wide reverse results. It is computed fresh from source by computeReport
// and rendered as JSON (--json) and as the committed matrix (--matrix).
type Report struct {
	Plans    []PlanCoverage `json:"plans"`
	Backward BackwardResult `json:"backward"`
	Orphans  OrphanResult   `json:"orphans"`
}

// PlanCoverage is one acceptance plan's coverage: its path, lifecycle status,
// the read mode (structured / prose), every criterion's coverage, and a count
// roll-up. Stale marks a draft plan whose every verify: test already resolves.
type PlanCoverage struct {
	Path   string       `json:"path"`
	Status string       `json:"status"`
	Mode   string       `json:"mode"`
	ACs    []ACCoverage `json:"acs"`
	Counts PlanCounts   `json:"counts"`
	Stale  bool         `json:"stale"`
}

// ACCoverage is one numbered criterion's coverage: its id, the scenario heading
// it sits under (for matrix grouping), its status, the proofs it names, and —
// for a unit-only front-end proof set — the opt-out reason that waives the
// render-from-wire e2e requirement.
type ACCoverage struct {
	ID       string  `json:"id"`
	Scenario string  `json:"scenario,omitempty"`
	Status   string  `json:"status"`
	Proofs   []Proof `json:"proofs,omitempty"`
	OptOut   string  `json:"opt_out,omitempty"`
}

// Proof is one item a criterion names as its evidence. Ref is the verbatim
// citation (a test name, a ui:<basename>, or the method word). For a test/ui
// proof, Resolves says whether it resolves to exactly one source file and File
// is that path. For a method proof, Method is the method word and Reason is the
// prose after it. For a front-end (ui:*) proof, FEKind is "e2e" or "unit"
// classifying the resolved spec's directory (ui/tests/e2e/ vs ui/src/**), so
// the matrix can show the proof kind the render-from-wire gate keys on.
type Proof struct {
	Ref      string `json:"ref"`
	Kind     string `json:"kind"`
	Resolves bool   `json:"resolves,omitempty"`
	File     string `json:"file,omitempty"`
	Method   string `json:"method,omitempty"`
	Reason   string `json:"reason,omitempty"`
	FEKind   string `json:"fe_kind,omitempty"`
}

// PlanCounts is the per-plan roll-up the matrix roll-up table reads.
type PlanCounts struct {
	ACs         int `json:"acs"`
	TestProven  int `json:"test_proven"`
	ByInspect   int `json:"by_inspection"`
	Unmet       int `json:"unmet"`
	Untracked   int `json:"untracked"`
	Unannotated int `json:"unannotated"`
}

// BackwardResult is the backward gate: TestADR_NNNN_* names pinning a missing
// ADR, or a retired ADR with no resolvable supersessor. A retired ADR whose
// Status line names a supersessor ADR that exists is tolerated — the behaviour
// is re-homed, not orphaned.
type BackwardResult struct {
	Violations []string `json:"violations"`
}

// OrphanResult is the orphan gate: scenario tests no landed plan claims, split
// into excused (//actrace:untracked-ok) and unexcused (the gate failures).
type OrphanResult struct {
	Scanned   int      `json:"scanned"`
	Excused   []string `json:"excused"`
	Untracked []string `json:"untracked"`
}

// computeReport walks every plan under root and builds the whole-tree result:
// per-plan coverage (skipping README, meta, and superseded plans), the backward
// gate, and the orphan gate. It never gates — a draft plan with unbuilt tests
// renders its gaps as missing rather than failing — so it is safe to drive the
// always-clean --matrix and --json surfaces.
func computeReport(root string, index map[string]string, fe feIndex) (Report, error) {
	var rep Report
	plans, err := filepath.Glob(filepath.Join(root, "docs/acceptance/*.md"))
	if err != nil {
		return rep, fmt.Errorf("globbing plans: %w", err)
	}
	sort.Strings(plans)
	for _, p := range plans {
		base := filepath.Base(p)
		if base == "README.md" || isMetaPlan(base) {
			continue
		}
		pc, skip, err := planCoverage(p, index, fe)
		if err != nil {
			return rep, err
		}
		if skip {
			continue
		}
		rep.Plans = append(rep.Plans, pc)
	}

	statusByNum, err := loadADRStatusByNumber(root)
	if err != nil {
		return rep, err
	}
	supersessorByNum, err := loadSupersessorByNumber(root)
	if err != nil {
		return rep, err
	}
	rep.Backward.Violations = backwardViolations(adrTestNames(index), statusByNum, supersessorByNum)

	scenarios, err := collectScenarioTests(root)
	if err != nil {
		return rep, err
	}
	claimed, err := claimedTests(root)
	if err != nil {
		return rep, err
	}
	rep.Orphans.Scanned = len(scenarios)
	for _, s := range untrackedScenarioTests(scenarios, claimed) {
		if s.excused {
			rep.Orphans.Excused = append(rep.Orphans.Excused, s.name)
		} else {
			rep.Orphans.Untracked = append(rep.Orphans.Untracked, s.name)
		}
	}
	return rep, nil
}

// planCoverage reads one plan and builds its PlanCoverage. A superseded plan is
// skipped (skip=true). A draft plan whose every verify: test already resolves is
// marked stale. The mode is "structured" when any AC carries a verify: field,
// "prose" otherwise.
func planCoverage(path string, index map[string]string, fe feIndex) (cov PlanCoverage, skip bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cov, false, err
	}
	lines := strings.Split(string(b), "\n")
	status, err := parseStatus(lines)
	if err != nil {
		return cov, false, fmt.Errorf("%s: %w", strings.TrimPrefix(path, "./"), err)
	}
	if status == "superseded" {
		return cov, true, nil
	}
	acEntries := parseACs(lines)
	cov = PlanCoverage{
		Path:   strings.TrimPrefix(path, "./"),
		Status: status,
		Mode:   "prose",
		ACs:    classifyACs(lines, index, fe),
	}
	if hasAnyVerify(acEntries) {
		cov.Mode = "structured"
	}
	cov.Counts = countPlan(cov.ACs)
	if status == "draft" {
		if _, ok := draftStaleness(cov.Path, acEntries, index); ok {
			cov.Stale = true
		}
	}
	return cov, false, nil
}

// classifyAC populates one criterion's coverage from its parsed acEntry,
// resolving every named Go test against the index and every `ui:<path>` proof
// against the front-end spec set. scenario is the nearest preceding
// `### Scenario ...` heading.
func classifyAC(e acEntry, scenario string, index map[string]string, fe feIndex) ACCoverage {
	cov := ACCoverage{ID: e.id, Scenario: scenario}
	switch {
	case !e.hasVerify:
		cov.Status = acStatusUnannotated
		return cov
	case isMethod(e.verify):
		cov.Status = acStatusMethod
		cov.Proofs = []Proof{methodProof(e.verify)}
		return cov
	}
	missing := 0
	for _, name := range testCiteRe.FindAllString(e.verify, -1) {
		ok := resolve(name, index)
		if !ok {
			missing++
		}
		cov.Proofs = append(cov.Proofs, Proof{Ref: name, Kind: proofKindTest, Resolves: ok, File: index[name]})
	}
	for _, name := range uiCiteRe.FindAllString(e.verify, -1) {
		// A ui:* reference resolves only when it singles out exactly one spec.
		// Zero matches is missing; two or more is ambiguous — both unresolved.
		hits := fe.matches(name)
		ok := len(hits) == 1
		if !ok {
			missing++
		}
		file := ""
		feKind := ""
		if ok {
			file = hits[0]
			feKind = feProofKind(file)
		}
		cov.Proofs = append(cov.Proofs, Proof{Ref: name, Kind: proofKindUI, Resolves: ok, File: file, FEKind: feKind})
	}
	if missing > 0 {
		cov.Status = acStatusMissing
	} else {
		cov.Status = acStatusProven
	}
	cov.OptOut = uiUnitOkReason(e.verify)
	return cov
}

// methodProof builds the Proof for a non-test verify value, splitting the method
// word from its reason. "none — reserved with no V1 producer" becomes method
// "none", reason "reserved with no V1 producer".
func methodProof(verify string) Proof {
	fields := strings.Fields(verify)
	method := ""
	if len(fields) > 0 {
		method = strings.ToLower(strings.Trim(fields[0], "`*_"))
	}
	reason := strings.TrimSpace(verify)
	if len(fields) > 0 {
		reason = strings.TrimPrefix(reason, fields[0])
	}
	reason = strings.TrimSpace(reason)
	reason = strings.TrimLeft(reason, "—-: ")
	reason = strings.TrimSpace(reason)
	return Proof{Ref: verify, Kind: proofKindMethod, Method: method, Reason: reason}
}

// classifyACs walks a plan's lines, tracking the nearest `### Scenario ...`
// heading, and returns each AC's coverage paired with the scenario it sits
// under. It reuses parseACs's block scan so the verify: parsing is identical,
// then assigns the heading by line position.
func classifyACs(lines []string, index map[string]string, fe feIndex) []ACCoverage {
	acs := parseACs(lines)
	if len(acs) == 0 {
		return nil
	}
	scenarioByAC := map[string]string{}
	current := ""
	for _, ln := range lines {
		if h, ok := scenarioHeading(ln); ok {
			current = h
			continue
		}
		if m := acLineRe.FindStringSubmatch(ln); m != nil {
			if _, seen := scenarioByAC[m[1]]; !seen {
				scenarioByAC[m[1]] = current
			}
		}
	}
	out := make([]ACCoverage, 0, len(acs))
	for _, e := range acs {
		out = append(out, classifyAC(e, scenarioByAC[e.id], index, fe))
	}
	return out
}

// scenarioHeading returns the trimmed title of a `### Scenario ...` heading and
// whether the line is one. Only Scenario headings group the matrix; other `###`
// sections (Modules in scope, etc.) sit after the ACs and never enclose one.
func scenarioHeading(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "### ") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimPrefix(t, "### "))
	if !strings.HasPrefix(title, "Scenario") {
		return "", false
	}
	return title, true
}

// countPlan rolls up a plan's AC statuses into PlanCounts.
func countPlan(acs []ACCoverage) PlanCounts {
	c := PlanCounts{ACs: len(acs)}
	for _, ac := range acs {
		switch ac.Status {
		case acStatusProven:
			c.TestProven++
		case acStatusMethod:
			c.ByInspect++
		case acStatusMissing:
			c.Unmet++
		case acStatusUnannotated:
			c.Unannotated++
		}
	}
	return c
}
