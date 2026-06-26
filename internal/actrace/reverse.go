// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: LicenseRef-Stacklok-Proprietary

// This file adds the two report-only reverse checks tracked in issue #443.
// The forward gate asks "does every test a landed plan names exist?"; the
// backward gate asks "does every TestADR_NNNN_* name a live ADR?". Neither can
// answer two reverse-direction questions:
//
//   - Orphan check (test → tracked). Did a scenario test land that no landed
//     plan tracks through its verify: field? Most TestADR_NNNN_* and
//     TestPrincipleN_* tests trace to a decision DIRECTLY (the backward gate and
//     adrstatus already cover those), so they are NOT orphans. The orphan check
//     is scoped to the scenario tests under test/integration, test/scenarios,
//     and test/e2e — the L4/L5 tests whose home is a plan, not an ADR — and
//     reports the ones no landed plan claims.
//   - Staleness check (draft → should-be-landed). A draft plan whose every
//     verify: test already resolves is a candidate to flip to landed. A draft
//     plan with at least one still-missing verify: test is not flagged — its
//     work has not landed yet.
//
// Both are report-only. They never feed the --strict exit code, mirroring the
// staged rollout the gate itself used: report first, gate later once the signal
// is triaged.

package actrace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// scenarioTestNameRe matches a scenario / acceptance test name —
// Test<Plan>_Scenario<N>_*. These are the L4/L5 tests whose home is an
// acceptance plan rather than an ADR or principle, so they are the set the
// orphan check reasons about.
var scenarioTestNameRe = regexp.MustCompile(`^Test[A-Za-z0-9]+_Scenario[0-9]+`)

// scenarioTest is one scenario test the orphan check considers: its name, the
// file that declares it, and whether it carries an `//actrace:untracked-ok`
// marker excusing it from the gate (for harness scaffolding that proves no AC).
type scenarioTest struct {
	name    string
	file    string
	excused bool
}

// untrackedOKMarker on the line above a scenario test func excuses it from the
// orphan gate — for a harness/composition smoke that is infrastructure, not an
// acceptance-criterion proof, so no plan should be expected to claim it.
const untrackedOKMarker = "actrace:untracked-ok"

// collectScenarioTests walks the scenario/e2e trees under root and returns every
// Test<Plan>_Scenario<N>_* declaration it finds, sorted by name. A name that
// appears in two files keeps its first-seen file, mirroring indexTestDecls.
func collectScenarioTests(root string) ([]scenarioTest, error) {
	// The trees the orphan check scans. Scenario tests live here; scoping to
	// them keeps the check from flagging the hundreds of ADR/principle-tracing
	// unit tests that trace to a decision directly.
	scenarioDirs := []string{"test/integration", "test/scenarios", "test/e2e"}
	seen := map[string]string{}
	excused := map[string]bool{}
	for _, dir := range scenarioDirs {
		base := filepath.Join(root, dir)
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			lines := strings.Split(string(b), "\n")
			for i, line := range lines {
				m := testDeclRe.FindStringSubmatch(line)
				if m == nil || !scenarioTestNameRe.MatchString(m[1]) {
					continue
				}
				name := m[1]
				if _, ok := seen[name]; ok {
					continue
				}
				seen[name] = strings.TrimPrefix(path, "./")
				// An `//actrace:untracked-ok` comment on the line above the func
				// excuses the test from the orphan gate.
				if i > 0 && strings.Contains(lines[i-1], untrackedOKMarker) {
					excused[name] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", dir, err)
		}
	}
	out := make([]scenarioTest, 0, len(seen))
	for name, file := range seen {
		out = append(out, scenarioTest{name: name, file: file, excused: excused[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// claimedTests returns every test citation named in a landed plan's verify:
// fields. A citation may be a fully-qualified name or a family stem (trailing
// "_" / "*"); it is returned verbatim so a scenario test can be matched against
// it with the same resolve() rule the forward gate uses. Only landed plans
// count: a draft plan names tests before they are built, so its verify: names
// are not yet a tracking claim. The README and meta plans are skipped.
func claimedTests(root string) (map[string]bool, error) {
	claimed := map[string]bool{}
	plans, err := filepath.Glob(filepath.Join(root, "docs/acceptance/*.md"))
	if err != nil {
		return nil, fmt.Errorf("globbing plans: %w", err)
	}
	for _, p := range plans {
		base := filepath.Base(p)
		if base == "README.md" || isMetaPlan(base) {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		lines := strings.Split(string(b), "\n")
		status, err := parseStatus(lines)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", strings.TrimPrefix(p, "./"), err)
		}
		if status != "landed" {
			continue
		}
		for _, e := range parseACs(lines) {
			if !e.hasVerify || isMethod(e.verify) {
				continue
			}
			for _, name := range testCiteRe.FindAllString(e.verify, -1) {
				claimed[name] = true
			}
		}
	}
	return claimed, nil
}

// untrackedScenarioTests returns the scenario tests no landed plan claims. A
// test is tracked when some claimed citation resolves to it — resolve() handles
// the family-stem case, so a plan that claims "TestFoo_Scenario1_" tracks every
// member of that family. The returned list is sorted by test name.
func untrackedScenarioTests(scenarios []scenarioTest, claimed map[string]bool) []scenarioTest {
	var untracked []scenarioTest
	for _, s := range scenarios {
		if !scenarioTracked(s.name, claimed) {
			untracked = append(untracked, s)
		}
	}
	return untracked
}

// scenarioTracked reports whether any claimed citation resolves to the test
// name, reusing the forward gate's resolve() so a family stem matches its
// members.
func scenarioTracked(name string, claimed map[string]bool) bool {
	single := map[string]string{name: ""}
	for cite := range claimed {
		if resolve(cite, single) {
			return true
		}
	}
	return false
}

// stalePlan names a draft plan whose verify: tests have all landed, with the
// count of test-verified criteria so a reader sees how much evidence the flag
// rests on.
type stalePlan struct {
	path      string
	testACs   int
	resolvedT int
}

// staleDraftPlans returns the draft plans whose every verify: test already
// resolves in the index — candidates to flip from draft to landed. A draft plan
// with at least one missing verify: test is not flagged; its work has not
// landed. A draft plan with no test-verified criteria at all (only method ACs,
// or no verify: fields) is not flagged either, since there is no landed test to
// infer from. The README and meta plans are skipped.
func staleDraftPlans(root string, index map[string]string) ([]stalePlan, error) {
	plans, err := filepath.Glob(filepath.Join(root, "docs/acceptance/*.md"))
	if err != nil {
		return nil, fmt.Errorf("globbing plans: %w", err)
	}
	var stale []stalePlan
	for _, p := range plans {
		base := filepath.Base(p)
		if base == "README.md" || isMetaPlan(base) {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		lines := strings.Split(string(b), "\n")
		status, err := parseStatus(lines)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", strings.TrimPrefix(p, "./"), err)
		}
		if status != "draft" {
			continue
		}
		if sp, ok := draftStaleness(strings.TrimPrefix(p, "./"), parseACs(lines), index); ok {
			stale = append(stale, sp)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].path < stale[j].path })
	return stale, nil
}

// draftStaleness inspects one draft plan's ACs. It returns a stalePlan and true
// when the plan has at least one test-verified criterion and every named test
// resolves; otherwise it returns false.
func draftStaleness(path string, acs []acEntry, index map[string]string) (stalePlan, bool) {
	testACs, resolved, missing := 0, 0, 0
	for _, e := range acs {
		if !e.hasVerify || isMethod(e.verify) {
			continue
		}
		names := testCiteRe.FindAllString(e.verify, -1)
		if len(names) == 0 {
			continue
		}
		testACs++
		acResolved := true
		for _, name := range names {
			if !resolve(name, index) {
				missing++
				acResolved = false
			}
		}
		if acResolved {
			resolved++
		}
	}
	if testACs == 0 || missing > 0 {
		return stalePlan{}, false
	}
	return stalePlan{path: path, testACs: testACs, resolvedT: resolved}, true
}

// runOrphanGate reports scenario tests no landed plan tracks and returns the
// count of unexcused ones — the gate failures. A test carrying an
// `//actrace:untracked-ok` marker is reported but never counted, so harness
// scaffolding does not fail the gate. Runs over the whole tree like the
// backward gate; --strict turns a non-zero count into a non-zero exit.
func runOrphanGate(root string) (int, error) {
	scenarios, err := collectScenarioTests(root)
	if err != nil {
		return 0, err
	}
	claimed, err := claimedTests(root)
	if err != nil {
		return 0, err
	}
	untracked := untrackedScenarioTests(scenarios, claimed)

	failures, excused := 0, 0
	fmt.Println("== reverse: scenario tests no landed plan tracks ==")
	for _, s := range untracked {
		if s.excused {
			excused++
			fmt.Printf("  ~ %s  (%s) — untracked, excused by //actrace:untracked-ok\n", s.name, s.file)
			continue
		}
		failures++
		fmt.Printf("  ✗ %s  (%s) — no plan's verify: claims it; cite it in a plan or mark //actrace:untracked-ok\n", s.name, s.file)
	}
	fmt.Printf("  %d scenario test(s) scanned · %d excused · %d untracked\n",
		len(scenarios), excused, failures)
	return failures, nil
}

// runStalenessReport prints the report-only staleness check: a draft plan whose
// every verify: test already resolves is a flip-to-landed candidate. A stale
// draft is a nudge, not an error, so this never gates.
func runStalenessReport(root string, index map[string]string) error {
	stale, err := staleDraftPlans(root, index)
	if err != nil {
		return err
	}
	fmt.Println("== reverse: draft plans whose verify: tests have all landed ==")
	for _, sp := range stale {
		fmt.Printf("  ? %s — %d test-verified AC(s) all resolve; consider flipping to landed\n",
			sp.path, sp.testACs)
	}
	fmt.Printf("  %d draft plan(s) look stale (report-only)\n", len(stale))
	return nil
}
