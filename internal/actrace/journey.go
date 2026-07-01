// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: LicenseRef-Stacklok-Proprietary

package actrace

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The two **Surface:** tag values from ADR-0077. A scenario is one or the
// other; user-facing wins when a scenario is conceptually both, so the author
// tags it user-facing.
const (
	surfaceUserFacing = "user-facing"
	surfaceBackend    = "backend-foundation"
)

// surfaceLineRe matches a `**Surface:**` field line under a scenario heading,
// mirroring the shape of statusLineRE. The value is the rest of the line.
var surfaceLineRe = regexp.MustCompile(`(?i)^\s*[-*\s]*\*\*surface:?\*\*:?\s*(.*)$`)

// journeyOkRe captures the journey-proof opt-out `journey-ok: <reason>` on a
// verify line. It grandfathers a pre-existing user-facing scenario that has no
// journey proof yet — but only when the reason cites a tracked issue, so the
// debt is visible, attributed, and expiring, never a silent pass (ADR-0077).
var journeyOkRe = regexp.MustCompile(`journey-ok:\s*(\S[^\n;]*)`)

// issueRefRe matches a tracked-issue reference — a bare `#123` or an issues URL.
var issueRefRe = regexp.MustCompile(`#\d+|/issues/\d+`)

// isWeakMethod reports whether a verify method is one a user-facing AC may not
// hide behind (ADR-0077, L3): demonstration / scenario / manual. The `none` and
// `inspection` methods stay valid — a closed-by-construction or reviewed
// criterion is legitimate on any surface.
func isWeakMethod(verify string) bool {
	fields := strings.Fields(verify)
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(strings.Trim(fields[0], "`*_")) {
	case "demonstration", "scenario", "manual":
		return true
	default:
		return false
	}
}

// scenarioNum extracts the scenario number from an ACx.y id: "AC12.3" → 12.
// A malformed id yields 0, which groups it under a distinct "scenario 0" whose
// missing-tag failure surfaces the malformed numbering.
func scenarioNum(acID string) int {
	s := strings.TrimPrefix(acID, "AC")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// parseSurfaceByAC binds each ACx.y id to the `**Surface:**` value in effect
// when it appears — the field is positional, sitting under its scenario
// heading before the scenario's ACs. It also returns "orphan" messages for a
// `**Surface:**` line with no following AC before the next Surface line or
// end of file, which is a mis-placed tag (ADR-0077, AC2.8).
func parseSurfaceByAC(lines []string) (byAC map[string]string, orphans []string) {
	byAC = map[string]string{}
	current := ""
	consumed := true // did an AC consume the current surface value?
	flushOrphan := func() {
		if current != "" && !consumed {
			orphans = append(orphans, fmt.Sprintf("**Surface:** %q has no following ACx.y block", current))
		}
	}
	for _, ln := range lines {
		if m := surfaceLineRe.FindStringSubmatch(ln); m != nil {
			flushOrphan()
			current = strings.ToLower(strings.TrimSpace(m[1]))
			consumed = false
			continue
		}
		if m := acLineRe.FindStringSubmatch(ln); m != nil {
			byAC[m[1]] = current
			consumed = true
		}
	}
	flushOrphan()
	return byAC, orphans
}

// buildConstraintLine returns the `//go:build ...` constraint of a Go source
// file, or "" if it has none.
func buildConstraintLine(src string) string {
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "//go:build ") {
			return t
		}
		// The build constraint sits at the top; once real code starts, stop.
		if t != "" && !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, "package") {
			break
		}
	}
	return ""
}

// goTestIsRealCluster reports whether the Go test file at path is a real-cluster
// e2e test: it lives under test/e2e/, its build constraint includes `e2e` but
// not `synthetic`, and it does not import idpfake. A `synthetic` subprocess
// test or one that mints its own token via idpfake fakes the seam it claims to
// cross, so it is not a journey proof (ADR-0077 substance rule).
func goTestIsRealCluster(path string) bool {
	if !strings.Contains(path, "test/e2e/") {
		return false
	}
	b, err := os.ReadFile(path) //nolint:gosec // path comes from the test-declaration index, not user input
	if err != nil {
		return false
	}
	src := string(b)
	tag := buildConstraintLine(src)
	if !strings.Contains(tag, "e2e") || strings.Contains(tag, "synthetic") {
		return false
	}
	return !strings.Contains(src, "idpfake")
}

// specHasNetworkAssertion reports whether a Playwright spec asserts on a real
// server response rather than on rendered DOM alone. The signal is a
// `waitForResponse` (or `waitForResponseEvent`) call — a spec that only checks
// the DOM renders fine against a mock regardless of the backend, which is how
// #483 / #508 shipped green (ADR-0077 substance rule).
func specHasNetworkAssertion(path string) bool {
	b, err := os.ReadFile(path) //nolint:gosec // path comes from the front-end spec index, not user input
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "waitForResponse")
}

// lookupTestPath returns the file declaring a cited Go test. It handles a
// fully-qualified name and a family stem ("TestFoo_"), mirroring resolve().
func lookupTestPath(cite string, index map[string]string) (string, bool) {
	if p, ok := index[cite]; ok {
		return p, true
	}
	if strings.HasSuffix(cite, "_") || strings.HasSuffix(cite, "*") {
		base := strings.TrimRight(cite, "_*")
		for name, p := range index {
			if strings.HasPrefix(name, base+"_") {
				return p, true
			}
		}
	}
	return "", false
}

// isJourneyProof reports whether an AC's verify cites at least one journey
// proof. Browser surface: a `ui-e2e-realfd:` token resolving to a
// .realfd.spec.ts spec that asserts on a real server response. Backend surface:
// a Go test under test/e2e/ that is a real-cluster run. Both are gated on
// substance, not location alone (ADR-0077).
func isJourneyProof(e acEntry, fe feIndex, index map[string]string) bool {
	for _, tok := range uiCiteRe.FindAllString(e.verify, -1) {
		if prefix, _ := parseFECitation(tok); prefix != fePrefixRealfd {
			continue
		}
		hits := fe.matches(tok)
		if len(hits) == 1 && strings.HasSuffix(hits[0], ".realfd.spec.ts") && specHasNetworkAssertion(hits[0]) {
			return true
		}
	}
	for _, name := range testCiteRe.FindAllString(e.verify, -1) {
		if path, ok := lookupTestPath(name, index); ok && goTestIsRealCluster(path) {
			return true
		}
	}
	return false
}

// scenarioGroup accumulates one scenario's ACs while the plan is scanned.
type scenarioGroup struct {
	surfaces    map[string]bool
	hasJourney  bool
	sawOptOut   bool // a journey-ok: token appeared on some AC in the scenario
	optOutValid bool // and that opt-out cited a tracked issue
}

// checkJourneyIntegrity is the ADR-0077 gate, run on a landed plan only when
// `.actrace.yml` sets journey_integrity. It binds each scenario's
// `**Surface:**` tag, then requires every user-facing scenario to carry at
// least one AC citing a journey proof. It hard-fails a missing tag, an
// unrecognised tag value, an orphan tag line, and a scenario whose ACs carry
// conflicting tags (ambiguous grouping). Returns the failure count.
func checkJourneyIntegrity(lines []string, acs []acEntry, fe feIndex, index map[string]string) int {
	surfByAC, orphans := parseSurfaceByAC(lines)
	failures := 0
	for _, msg := range orphans {
		failures++
		fmt.Printf("  ✗ %s\n", msg)
	}
	failures += checkWeakMethods(acs, surfByAC)
	groups, order := groupScenarios(acs, surfByAC, fe, index)
	for _, n := range order {
		failures += evaluateScenario(n, groups[n])
	}
	return failures
}

// checkWeakMethods reports every user-facing AC proven by a weak method
// (demonstration / scenario / manual), which ADR-0077 forbids on a user-facing
// surface. Returns the failure count.
func checkWeakMethods(acs []acEntry, surfByAC map[string]string) int {
	failures := 0
	for _, e := range acs {
		if surfByAC[e.id] == surfaceUserFacing && isWeakMethod(e.verify) {
			failures++
			fmt.Printf("  ✗ %s  user-facing AC cannot be proven by a demonstration/scenario/manual method\n", e.id)
		}
	}
	return failures
}

// groupScenarios buckets ACs by scenario number, recording each scenario's
// tag(s), whether any AC cites a journey proof, and whether a journey-ok:
// opt-out (and a valid, issue-citing one) appeared.
func groupScenarios(
	acs []acEntry, surfByAC map[string]string, fe feIndex, index map[string]string,
) (map[int]*scenarioGroup, []int) {
	groups := map[int]*scenarioGroup{}
	var order []int
	for _, e := range acs {
		n := scenarioNum(e.id)
		g := groups[n]
		if g == nil {
			g = &scenarioGroup{surfaces: map[string]bool{}}
			groups[n] = g
			order = append(order, n)
		}
		g.surfaces[surfByAC[e.id]] = true
		if isJourneyProof(e, fe, index) {
			g.hasJourney = true
		}
		if m := journeyOkRe.FindStringSubmatch(e.verify); m != nil {
			g.sawOptOut = true
			if issueRefRe.MatchString(m[1]) {
				g.optOutValid = true
			}
		}
	}
	return groups, order
}

// evaluateScenario returns the failures for one scenario's tag: a conflicting,
// missing, or unrecognised **Surface:** value, or (for a user-facing scenario)
// a missing journey proof without a valid opt-out.
func evaluateScenario(n int, g *scenarioGroup) int {
	if len(g.surfaces) > 1 {
		fmt.Printf("  ✗ scenario %d: ACs carry conflicting **Surface:** values — grouping is ambiguous\n", n)
		return 1
	}
	var surf string
	for s := range g.surfaces {
		surf = s
	}
	switch surf {
	case "":
		fmt.Printf("  ✗ scenario %d: no **Surface:** tag (add user-facing or backend-foundation)\n", n)
		return 1
	case surfaceUserFacing:
		return evaluateUserFacing(n, g)
	case surfaceBackend:
		return 0 // no journey proof required
	default:
		fmt.Printf("  ✗ scenario %d: unrecognised **Surface:** value %q (want user-facing or backend-foundation)\n", n, surf)
		return 1
	}
}

// evaluateUserFacing returns the failures for a user-facing scenario: a
// malformed opt-out, or a missing journey proof with no valid opt-out. A valid
// issue-citing opt-out grandfathers the scenario (deferred, tracked debt).
func evaluateUserFacing(n int, g *scenarioGroup) int {
	switch {
	case g.sawOptOut && !g.optOutValid:
		fmt.Printf("  ✗ scenario %d: journey-ok: opt-out must cite a tracked issue (e.g. #692)\n", n)
		return 1
	case g.optOutValid:
		return 0
	case !g.hasJourney:
		fmt.Printf("  ✗ scenario %d: user-facing but no AC cites a journey proof "+
			"(a ui-e2e-realfd: spec with a real-response assertion, or a test/e2e/ real-cluster Go test)\n", n)
		return 1
	default:
		return 0
	}
}
