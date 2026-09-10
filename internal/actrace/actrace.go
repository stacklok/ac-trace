// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package actrace reports acceptance-criteria → test coverage for the plans
// under docs/acceptance/: every test a plan names as the proof of a criterion
// should exist in the tree. It is the report-only stage of the traceability
// work.
//
// # Two modes
//
// Structured (preferred). A plan whose acceptance criteria carry a `verify:`
// sub-line is read structurally: only the verify field is the contract.
//
//   - AC1.1: the token never enters the Agent Loop.
//   - verify: `TestPrinciple4_SaaSCredentialNeverEntersAgentLoop`
//   - AC1.4: a redirect is impossible by construction.
//   - verify: none — reserved with no V1 producer; closed by construction
//
// The verify value is either a list of proofs (each must exist) or a
// non-test method — one of none / inspection / demonstration / manual /
// scenario — for a criterion proven by review or by a higher-level
// demonstration rather than a unit test. A proof is a Go test name or a
// front-end spec reference `ui:<path>`. In structured mode prose test
// mentions elsewhere in the plan are ignored, so a retired-test name, a
// "pre-PR-NNN name" note, or a "welcome but not required" pin no longer
// reads as a missing contract.
//
// # Front-end spec references
//
// A `ui:<path>` reference names a vitest/Playwright spec under ui/ by the
// trailing part of its path (minus the .test/.spec suffix). It resolves by
// path suffix on directory boundaries: the bare basename `ui:message-bubble`
// is the shortest form, and a longer suffix `ui:chat/__tests__/message-bubble`
// qualifies it. A reference that matches exactly one spec resolves; one that
// matches none is MISSING; one that matches two or more is AMBIGUOUS — the
// author must add enough of the path to single one out. Resolution is
// file-level, not test-case-level: a `ui:<path>` maps to a whole spec file,
// not an individual `test()` / `it()` block, because vitest and Playwright
// export no per-case symbol the way Go exports a TestXxx function.
//
// Prose (legacy). A plan with no `verify:` lines falls back to scanning every
// test name it cites. A cited-but-missing test whose citing line says
// "superseded" is treated as a documented removal, not a gap. This mode is
// lossy — it cannot tell a contract citation from descriptive prose — which
// is why the structured field exists; converting a plan is how its report
// becomes trustworthy.
//
// # Plan-status lifecycle and the forward gate
//
// A plan declares its lifecycle on the `**Status:**` line, one of draft /
// in-progress / landed / superseded (per ADR-0065). With --strict the gate
// bites only a `landed` plan; draft and in-progress plans still print their
// report but never cause a non-zero exit, because a plan names its tests before
// they are built. A superseded plan is skipped entirely. An unrecognised status
// is a hard error — a typo must not silently disable the gate.
//
// On a landed plan the forward checks each cause a --strict failure: an AC with
// no verify: field; a verify: test name that does not resolve; a bare-text
// ADR-NNNN naming no docs/adr/NNNN-*.md file or a Principle N outside the range
// in docs/design/principles.md; and a plan with zero ACx.y criteria. Linked
// citations ([ADR-xxxx](../adr/...)) stay covered by `task docs-check`, so the
// bare-text grounding check is only for un-linked references.
//
// SCOPE: name-presence only, mirroring the standalone adrstatus tool. actrace
// does not check that a test's body defends the AC, nor that it passes.
package actrace

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	testDeclRe = regexp.MustCompile(`func (Test[A-Za-z0-9_]+)`)
	// A citation must look like a test identifier: "Test" followed by an
	// uppercase letter or "_", which excludes the English words "Tests" and
	// "Testing" that a bare \bTest\w+ would wrongly capture.
	testCiteRe = regexp.MustCompile(`\bTest[A-Z_][A-Za-z0-9_]*`)
	// uiCiteRe captures a front-end test reference — one of four prefixes:
	//   - `ui-e2e-realfd:<path>` — a Playwright real-stack spec under ui/tests/e2e/
	//     whose filename ends in .realfd.spec.ts; runs against a live mock-LLM
	//     cluster via the chromium-realfd project (advisory in CI). Resolves as
	//     an e2e proof for the render-from-wire gate.
	//   - `ui-e2e:<path>` — a Playwright end-to-end spec under ui/tests/e2e/
	//   - `ui-unit:<path>` — a vitest unit spec under ui/src/**
	//   - `ui:<path>`     — legacy bare form; classified by the resolved spec's
	//     directory (under ui/tests/e2e/ → e2e, otherwise unit). Kept so old
	//     plans don't silently slip through: a bare `ui:` resolving under
	//     ui/src/** is treated as a unit proof and gated accordingly.
	//
	// The path is the trailing part of the spec file's location (minus its
	// .test/.spec suffix); "/" is allowed so a colliding basename can be
	// qualified, e.g. `ui-unit:chat/__tests__/message-bubble`.
	// ui-e2e-realfd appears before ui-e2e for readability: the more-specific
	// prefix is listed first. The two prefixes don't nest (the character after
	// "ui-e2e" is ":" vs "-"), so alternation order is not load-bearing here.
	uiCiteRe = regexp.MustCompile(`\bui-e2e-realfd:[A-Za-z0-9._/-]+|\bui-(?:e2e|unit):[A-Za-z0-9._/-]+|\bui:[A-Za-z0-9._/-]+`)
	// uiUnitOkRe captures the render-from-wire opt-out token `ui-unit-ok:
	// <reason>` on a verify line. It declares that a criterion's behaviour is
	// genuinely not render-from-wire (a pure formatting helper, a URL
	// sanitizer, a type-guard) so a unit-only proof set is accepted. The
	// reason is mandatory and non-empty; the gate rejects a bare
	// `ui-unit-ok:` with no text.
	uiUnitOkRe   = regexp.MustCompile(`ui-unit-ok:\s*(\S[^\n;]*)`)
	acRe         = regexp.MustCompile(`\bAC[0-9]+\.[0-9]+\b`)
	acLineRe     = regexp.MustCompile(`^- (AC[0-9]+\.[0-9]+):`)
	verifyLineRe = regexp.MustCompile(`^\s*-\s*verify:\s*(.*)$`)
	// statusLineRE matches a `**Status:**` line (also `- **Status**:`),
	// capturing the rest of the line, mirroring the standalone adrstatus tool.
	statusLineRE = regexp.MustCompile(`(?i)^[-*\s]*\*\*status:?\*\*:?\s*(.*)$`)
	// headRE matches a `## Status` heading on its own line. The status value
	// then lives on the next non-empty line. The standalone adrstatus tool
	// supported both the `**Status:**` line and the `## Status` heading form;
	// an ADR using the heading form must classify the same way it did before the
	// extraction.
	headRE = regexp.MustCompile(`(?i)^#{1,6}\s+status\s*$`)
	// adrCiteRe captures a bare-text `ADR-NNNN` reference and the two characters
	// that follow, so a linked `[ADR-0042](../adr/...)` (followed by "](") can be
	// excluded — those stay covered by `task docs-check`.
	adrCiteRe = regexp.MustCompile(`\bADR-(\d{3,4})\b(..)?`)
	// principleCiteRe captures a bare-text `Principle N` reference.
	principleCiteRe = regexp.MustCompile(`\bPrinciple (\d+)\b`)
	// codeSpanRe matches an inline-code span. A reference inside backticks is a
	// literal example, not a citation (e.g. an AC describing the gate may say a
	// `ADR-9999` reference fails) — strip these before grounding, the same way
	// structured mode ignores prose test mentions.
	codeSpanRe = regexp.MustCompile("`[^`]*`")
	// principleItemRe matches a top-level numbered principle (`13. **...`) in
	// docs/design/principles.md, used to learn the valid 1..N range.
	principleItemRe = regexp.MustCompile(`^(\d+)\.\s+\*\*`)
	// adrTestNameRe binds a TestADR_NNNN_* declaration's name to exactly four
	// digits followed by `_`, mirroring the standalone adrstatus tool so a
	// query for 0019 never matches TestADR_0190_*.
	adrTestNameRe = regexp.MustCompile(`^TestADR_(\d{4})_`)
	// retiredRE matches the passive "Superseded ... by" / "Deprecated" a retired
	// ADR uses. It does NOT match the active "Supersedes" the *superseding*
	// ADR uses (different ending), so ADR-0013 ("Supersedes ADR-0006") stays
	// "accepted" while ADR-0006 ("Superseded ... by ADR-0013") reads "superseded".
	// Inlined from the standalone adrstatus tool.
	retiredRE   = regexp.MustCompile(`(?i)\b(superseded|deprecated)\b`)
	firstWordRE = regexp.MustCompile(`[A-Za-z]+`)
	// supersessorRe captures the supersessor ADR number named on a retired ADR's
	// Status line. It matches the first `ADR-NNNN` reference (linked
	// `[ADR-0068]` or bare `ADR-0068`) that follows a "Superseded … by" or
	// "Deprecated … by" phrase, the shape ADR-0065 mandates for a retired status.
	supersessorRe = regexp.MustCompile(`(?i)(?:supersed\w*|deprecat\w*)[^\n]*?\bADR-(\d{3,4})\b`)
)

// isValidStatus reports whether w is one of the ADR-0065 lifecycle words.
func isValidStatus(w string) bool {
	switch w {
	case "draft", "in-progress", "landed", "superseded":
		return true
	default:
		return false
	}
}

// isMetaPlan reports whether a plan documents the test-naming convention
// itself — its prose carries TestADR_NNNN_, TestPrincipleN_, TestScenarioN
// placeholders, so its "citations" are illustrative, not an acceptance
// contract. The generated traceability matrix is also skipped: it is an actrace
// artifact, not an acceptance plan, and re-reading it as one would fail on its
// missing Status line. Those files are skipped.
func isMetaPlan(base string) bool {
	switch base {
	case "test-suite-legibility.md", "testing-completeness.md", "traceability.md":
		return true
	default:
		return false
	}
}

// isMethod reports whether a verify value names a non-test verification method
// (proven by review or a higher-level demonstration) rather than listing tests.
func isMethod(verify string) bool {
	fields := strings.Fields(verify)
	if len(fields) == 0 {
		return true // empty verify: nothing to check
	}
	switch strings.ToLower(strings.Trim(fields[0], "`*_")) {
	case "none", "inspection", "demonstration", "manual", "scenario":
		return true
	default:
		return false
	}
}

// citation is one test name a plan names as part of its acceptance contract.
type citation struct {
	test       string
	ac         string // nearest AC id on the citing line, "" if none
	superseded bool   // citing line documents the test as removed
}

// acEntry is one numbered acceptance criterion and its optional verify field.
// body holds the criterion's prose (the AC line and any continuation lines, but
// not the verify: sub-line) so a bare-text ADR / Principle cited in the prose
// can be grounded.
type acEntry struct {
	id        string
	verify    string
	hasVerify bool
	body      string
}

// grounding records which ADRs and principles exist, so a bare-text citation
// can be checked against the real set.
type grounding struct {
	adrNums        map[int]bool
	principleCount int
}

// Run is the CLI entrypoint. It parses args, runs the requested mode, and
// returns the process exit code (0 success, 1 gate failure under --strict,
// 2 usage/internal error). The binary in cmd/actrace calls os.Exit(Run(os.Args[1:])).
func Run(args []string) int {
	fs := flag.NewFlagSet("actrace", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	plan := fs.String("plan", "", "check a single plan (path or basename, with or without .md); default: all plans")
	strict := fs.Bool("strict", false, "exit non-zero when a landed plan fails a forward check")
	reverse := fs.Bool("reverse", false, "also print the report-only reverse checks (untracked scenario tests, stale draft plans)")
	jsonOut := fs.Bool("json", false, "print the whole-tree coverage as indented JSON to stdout (computed fresh, not committed)")
	matrix := fs.Bool("matrix", false, "regenerate the committed docs/acceptance/traceability.md matrix (never gates)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	index, fe, err := indexTestDecls(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "indexing test declarations:", err)
		return 2
	}

	// --json and --matrix render the freshly-computed result model and never
	// gate, mirroring adrstatus: the JSON is the automation interface, the
	// matrix is committed and gated for freshness by verify-gen.
	if *jsonOut || *matrix {
		if err := renderComputed(".", index, fe, *jsonOut, *matrix); err != nil {
			fmt.Fprintln(os.Stderr, "actrace:", err)
			return 2
		}
		return 0
	}

	ground, err := loadGrounding(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading grounding:", err)
		return 2
	}

	cfg, err := loadConfig(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "loading .actrace.yml:", err)
		return 2
	}

	plans, err := resolvePlans(*plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "globbing plans:", err)
		return 2
	}

	totalFailures := runPlans(plans, index, fe, ground, cfg)

	// The backward gate runs once over the whole tree, not per plan: every
	// landed TestADR_NNNN_* must name an ADR that exists and is not retired.
	// It is skipped when --plan narrows the run to one plan, since it is a
	// repo-wide check unrelated to a single plan.
	if *plan == "" {
		fmt.Println()
		backwardFailures, err := runBackwardGate(".", index)
		if err != nil {
			fmt.Fprintln(os.Stderr, "running backward gate:", err)
			return 2
		}
		totalFailures += backwardFailures

		// The orphan gate runs alongside the backward gate, over the whole tree:
		// a scenario test that no landed plan claims (and is not marked
		// //actrace:untracked-ok) is a --strict failure — the reverse of the
		// forward gate. It answers "did a scenario test land that no plan tracks?".
		fmt.Println()
		orphanFailures, err := runOrphanGate(".")
		if err != nil {
			fmt.Fprintln(os.Stderr, "running orphan gate:", err)
			return 2
		}
		totalFailures += orphanFailures
	}

	// The staleness check is report-only: a draft plan whose verify: tests have
	// all landed is a flip-to-landed nudge, not an error, so it never adds to
	// totalFailures. It runs only under --reverse, over the whole tree.
	if *reverse && *plan == "" {
		fmt.Println()
		if err := runStalenessReport(".", index); err != nil {
			fmt.Fprintln(os.Stderr, "running staleness report:", err)
			return 2
		}
	}

	fmt.Println()
	if totalFailures > 0 {
		fmt.Printf("%d traceability-gate failure(s)\n", totalFailures)
		if *strict {
			return 1
		}
		fmt.Println("(report-only; pass --strict to gate)")
		return 0
	}
	fmt.Println("every landed plan passes its forward checks; every TestADR_* pins a live ADR")
	return 0
}

// resolvePlans returns the plan files to check. An empty plan flag means every
// plan under docs/acceptance; otherwise it normalises the one named plan
// (adding .md and the docs/acceptance/ prefix when omitted).
func resolvePlans(plan string) ([]string, error) {
	if plan == "" {
		return filepath.Glob("docs/acceptance/*.md")
	}
	p := plan
	if !strings.HasSuffix(p, ".md") {
		p += ".md"
	}
	if !strings.Contains(p, "/") {
		p = filepath.Join("docs/acceptance", p)
	}
	return []string{p}, nil
}

// runPlans checks each plan and returns the total forward-check failures. The
// README and meta plans are skipped; an unreadable or mis-statused plan is a
// hard error that exits the process.
func runPlans(plans []string, index map[string]string, fe feIndex, ground grounding, cfg Config) int {
	totalFailures := 0
	for _, p := range plans {
		base := filepath.Base(p)
		if base == "README.md" || isMetaPlan(base) {
			continue
		}
		failures, skipped, err := runPlan(p, index, fe, ground, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "checking", p, ":", err)
			os.Exit(2)
		}
		if skipped {
			continue
		}
		totalFailures += failures
	}
	return totalFailures
}

// indexTestDecls maps every `func Test*` name to the file that declares it, and
// collects every front-end spec stem a `ui:<path>` reference can resolve
// against.
func indexTestDecls(root string) (map[string]string, feIndex, error) {
	index := map[string]string{}
	var fe feIndex
	skip := map[string]bool{".git": true, "node_modules": true, ".pnpm-store": true, "vendor": true}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skip[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// Front-end spec files are collected so a `ui:<path>` reference resolves
		// by path suffix against the real spec.
		if _, ok := feSpecStem(path); ok {
			fe = append(fe, strings.TrimPrefix(path, "./"))
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range testDeclRe.FindAllStringSubmatch(string(b), -1) {
			if _, ok := index[m[1]]; !ok {
				index[m[1]] = strings.TrimPrefix(path, "./")
			}
		}
		return nil
	})
	return index, fe, err
}

// feSpecStem returns a front-end spec file's repo-relative path minus its
// .test/.spec.{ts,tsx} suffix, and whether path is such a file. The stem is
// what a `ui:<path>` reference resolves against — e.g.
// "ui/src/components/chat/__tests__/message-bubble".
func feSpecStem(path string) (string, bool) {
	p := strings.TrimPrefix(path, "./")
	for _, suf := range []string{".test.tsx", ".test.ts", ".spec.tsx", ".spec.ts"} {
		if strings.HasSuffix(p, suf) {
			return strings.TrimSuffix(p, suf), true
		}
	}
	return "", false
}

// feIndex is the set of front-end spec files a `ui:<path>` reference resolves
// against, each a repo-relative path. A reference matches by path suffix on
// directory boundaries (against the path minus its .test/.spec suffix), so the
// bare basename is the shortest form and a longer path qualifies a basename two
// specs share.
type feIndex []string

// fePrefix is the declared kind a citation claims for its front-end proof.
type fePrefix int

const (
	fePrefixLegacy fePrefix = iota // bare `ui:` — classified by resolved path
	fePrefixE2E                    // `ui-e2e:` — must resolve under ui/tests/e2e/
	fePrefixUnit                   // `ui-unit:` — must resolve under ui/src/**
	fePrefixRealfd                 // `ui-e2e-realfd:` — must resolve under ui/tests/e2e/ and end in .realfd.spec.ts
)

// parseFECitation splits a `ui:*` citation token into its declared prefix and
// the path part the spec index resolves against. The path is the same for all
// prefixes; the prefix is what the author declared.
// ui-e2e-realfd is checked before ui-e2e for readability. The two prefixes
// don't nest ("ui-e2e" is followed by ":" vs "-"), so switch-case order is
// not load-bearing.
func parseFECitation(cite string) (fePrefix, string) {
	switch {
	case strings.HasPrefix(cite, "ui-e2e-realfd:"):
		return fePrefixRealfd, strings.TrimPrefix(cite, "ui-e2e-realfd:")
	case strings.HasPrefix(cite, "ui-e2e:"):
		return fePrefixE2E, strings.TrimPrefix(cite, "ui-e2e:")
	case strings.HasPrefix(cite, "ui-unit:"):
		return fePrefixUnit, strings.TrimPrefix(cite, "ui-unit:")
	default:
		return fePrefixLegacy, strings.TrimPrefix(cite, "ui:")
	}
}

// feKindE2E is the proof kind for Playwright specs under ui/tests/e2e/.
const feKindE2E = "e2e"

// feProofKind classifies a resolved front-end spec path: feKindE2E if it lives
// under ui/tests/e2e/ (Playwright, per ui/playwright.config.ts testDir), else
// "unit" (vitest under ui/src/**). Classification is by resolved path, not by
// the citation's claimed prefix — a `ui-e2e:` citation that resolves under
// ui/src/** is a mislabel the gate rejects.
func feProofKind(resolvedPath string) string {
	if strings.HasPrefix(resolvedPath, "ui/tests/e2e/") {
		return feKindE2E
	}
	return "unit"
}

// matches returns the spec files the citation resolves to, sorted. The citation
// is the verify token including its `ui:`/`ui-e2e:`/`ui-unit:` prefix; the path
// part is compared against each spec's stem (the path minus its .test/.spec
// suffix). Zero matches means no such spec; one means a unique resolution; more
// than one means the reference is ambiguous and must be qualified with more of
// the path.
func (fe feIndex) matches(cite string) []string {
	_, want := parseFECitation(cite)
	var hits []string
	for _, path := range fe {
		stem, ok := feSpecStem(path)
		if !ok {
			continue
		}
		if stem == want || strings.HasSuffix(stem, "/"+want) {
			hits = append(hits, path)
		}
	}
	sort.Strings(hits)
	return hits
}

// resolve reports whether a citation matches a real test. A citation ending in
// "_" (or "*") is a family stem and matches any declared member: e.g.
// "TestADR_0061_" matches "TestADR_0061_AdminWritesSkill". A fully-qualified
// name (no trailing "_") must match a declared test EXACTLY — it does NOT
// resolve via a longer sibling that merely shares its prefix, so a bare
// "TestADR_0061" is a miss unless a test named exactly "TestADR_0061" exists.
func resolve(cite string, index map[string]string) bool {
	if strings.HasSuffix(cite, "_") || strings.HasSuffix(cite, "*") {
		base := strings.TrimRight(cite, "_*")
		if _, ok := index[base]; ok {
			return true
		}
		for name := range index {
			if strings.HasPrefix(name, base+"_") {
				return true
			}
		}
		return false
	}
	_, ok := index[cite]
	return ok
}

// parseACs extracts each numbered AC and the verify: sub-line in its block.
// An AC's block runs from its line to the next AC, heading, or blank line, so
// the verify: line is found even when the criterion text wraps across lines.
func parseACs(lines []string) []acEntry {
	var acs []acEntry
	for i, line := range lines {
		m := acLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		e := acEntry{id: m[1], body: line}
		for j := i + 1; j < len(lines); j++ {
			nxt := lines[j]
			if strings.TrimSpace(nxt) == "" || strings.HasPrefix(nxt, "- ") || strings.HasPrefix(nxt, "#") {
				break
			}
			if vm := verifyLineRe.FindStringSubmatch(nxt); vm != nil {
				e.verify = strings.TrimSpace(vm[1])
				e.hasVerify = true
				break
			}
			e.body += "\n" + nxt
		}
		acs = append(acs, e)
	}
	return acs
}

// parseStatus reads a plan's `**Status:**` line and reduces it to one lifecycle
// word. A missing status line or an unrecognised value is a hard error — a typo
// must not silently disable the gate for a plan.
func parseStatus(lines []string) (string, error) {
	for _, ln := range lines {
		m := statusLineRE.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		w := firstStatusWord(m[1])
		if !isValidStatus(w) {
			return "", fmt.Errorf("unrecognised status %q (want draft / in-progress / landed / superseded)", w)
		}
		return w, nil
	}
	return "", fmt.Errorf("no **Status:** line found")
}

// firstStatusWord pulls the lifecycle token off a status line. The token may be
// hyphenated ("in-progress"), so it runs to the first space, comma, or period.
func firstStatusWord(s string) string {
	s = strings.TrimSpace(s)
	i := strings.IndexAny(s, " ,.\t")
	if i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// loadGrounding learns which ADRs exist (from docs/adr/NNNN-*.md filenames) and
// how many platform principles are declared in docs/design/principles.md, so a
// bare-text citation can be checked against the real set.
func loadGrounding(root string) (grounding, error) {
	g := grounding{adrNums: map[int]bool{}}
	matches, err := filepath.Glob(filepath.Join(root, "docs/adr/[0-9][0-9][0-9][0-9]-*.md"))
	if err != nil {
		return g, fmt.Errorf("globbing ADR files: %w", err)
	}
	for _, p := range matches {
		base := filepath.Base(p)
		if n, err := strconv.Atoi(base[:4]); err == nil {
			g.adrNums[n] = true
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "docs/design/principles.md"))
	if err != nil {
		return g, fmt.Errorf("reading principles.md: %w", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if m := principleItemRe.FindStringSubmatch(ln); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > g.principleCount {
				g.principleCount = n
			}
		}
	}
	return g, nil
}

// runPlan classifies a plan by status and decides whether --strict would gate
// it. A landed plan is run through the forward checks; the returned count is the
// number of failures. draft and in-progress plans are reported but never gate,
// so they return zero failures. A superseded plan is skipped (skipped=true). An
// unrecognised status is a hard error.
func runPlan(
	path string, index map[string]string, fe feIndex, ground grounding, cfg Config,
) (failures int, skipped bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	lines := strings.Split(string(b), "\n")
	status, err := parseStatus(lines)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", strings.TrimPrefix(path, "./"), err)
	}
	if status == "superseded" {
		return 0, true, nil
	}
	acs := parseACs(lines)
	if status == "landed" {
		return checkLanded(path, lines, acs, index, fe, ground, cfg), false, nil
	}
	// draft / in-progress: report only, never gate.
	reportPlan(path, status, acs, lines, index, fe, cfg)
	return 0, false, nil
}

// reportPlan prints a draft / in-progress plan's coverage without gating.
func reportPlan(path, status string, acs []acEntry, lines []string, index map[string]string, fe feIndex, cfg Config) {
	fmt.Printf("(%s, report-only) ", status)
	if hasAnyVerify(acs) {
		checkStructured(path, acs, index, fe, cfg)
		return
	}
	checkProse(path, lines, index)
}

// checkLanded runs the forward gate on a landed plan and returns the number of
// failures: zero structured ACs; an AC with no verify: field; a verify: test
// that does not resolve; or a bare-text ADR / Principle that grounds to nothing.
func checkLanded(
	path string, lines []string, acs []acEntry, index map[string]string, fe feIndex, ground grounding, cfg Config,
) int {
	fmt.Printf("== %s ==  [landed]\n", strings.TrimPrefix(path, "./"))
	if len(acs) == 0 {
		fmt.Println("  ✗ landed plan has zero ACx.y criteria — must assert at least one")
		return 1
	}
	failures := 0
	for _, e := range acs {
		switch {
		case !e.hasVerify:
			failures++
			fmt.Printf("  ✗ %s  no verify: field\n", e.id)
		case isMethod(e.verify):
			// non-test method with a reason — accepted.
		default:
			for _, name := range testCiteRe.FindAllString(e.verify, -1) {
				if !resolve(name, index) {
					failures++
					fmt.Printf("  ✗ %s  verify names %s — MISSING\n", e.id, name)
				}
			}
			failures += checkUIRefs(e, fe)
			failures += checkRenderFromWireGate(e, fe)
			failures += checkResolverRefs(e, cfg)
		}
		failures += checkGrounding(e, ground)
	}
	if cfg.JourneyIntegrity {
		failures += checkJourneyIntegrity(lines, acs, fe, index)
	}
	fmt.Printf("  %d ACs · %d failure(s)\n", len(acs), failures)
	return failures
}

// checkUIRefs resolves every `ui:*` reference in an AC's verify field and
// reports the ones that do not single out exactly one spec: zero matches is
// MISSING, two or more is AMBIGUOUS (the basename is shared, so the reference
// must be qualified with more of the path). It also rejects a mislabeled
// citation: a `ui-e2e:` token resolving outside ui/tests/e2e/ (or a `ui-unit:`
// token resolving under it) is a hard error — the author declared the wrong
// kind. A `ui-e2e-realfd:` token must resolve to a .realfd.spec.ts file under
// ui/tests/e2e/. It returns the count of unresolved / mislabeled references.
func checkUIRefs(e acEntry, fe feIndex) int {
	failures := 0
	for _, name := range uiCiteRe.FindAllString(e.verify, -1) {
		prefix, _ := parseFECitation(name)
		hits := fe.matches(name)
		switch len(hits) {
		case 1:
			// unique resolution — check the declared prefix matches the
			// resolved path's kind. A mislabel is a hard error: the gate must
			// not accept a `ui-e2e:` citation that resolves to a unit spec
			// (or vice versa), since that hides a unit-only proof behind an
			// e2e claim. A `ui-e2e-realfd:` citation must resolve to a
			// .realfd.spec.ts file under ui/tests/e2e/.
			kind := feProofKind(hits[0])
			switch prefix {
			case fePrefixE2E:
				if kind != feKindE2E {
					failures++
					fmt.Printf("  ✗ %s  verify names %s — MISLABELED: declared ui-e2e: but resolves to unit spec %s\n",
						e.id, name, hits[0])
				}
			case fePrefixUnit:
				if kind != "unit" {
					failures++
					fmt.Printf("  ✗ %s  verify names %s — MISLABELED: declared ui-unit: but resolves to e2e spec %s\n",
						e.id, name, hits[0])
				}
			case fePrefixRealfd:
				if kind != feKindE2E {
					failures++
					fmt.Printf("  ✗ %s  verify names %s — MISLABELED: declared ui-e2e-realfd: but resolves to unit spec %s\n",
						e.id, name, hits[0])
				} else if !strings.HasSuffix(hits[0], ".realfd.spec.ts") {
					failures++
					fmt.Printf(
						"  ✗ %s  verify names %s — MISLABELED: declared ui-e2e-realfd: but resolved spec does not end in .realfd.spec.ts: %s\n",
						e.id, name, hits[0])
				}
			case fePrefixLegacy:
				// classified by resolved path, not a mislabel
			default:
				// exhaustiveness check: all fePrefix values handled above
			}
		case 0:
			failures++
			fmt.Printf("  ✗ %s  verify names %s — MISSING (no FE spec under ui/)\n", e.id, name)
		default:
			failures++
			fmt.Printf("  ✗ %s  verify names %s — AMBIGUOUS across %d FE specs (%s); qualify with more of the path\n",
				e.id, name, len(hits), strings.Join(hits, ", "))
		}
	}
	return failures
}

// checkRenderFromWireGate enforces the default-deny rule from
// .claude/rules/bff-route-handlers.md § "Proving FD-wire translation": a
// landed AC whose proof set contains one or more front-end specs must include
// at least one Playwright e2e proof (a spec under ui/tests/e2e/), unless the
// verify line carries `ui-unit-ok: <reason>` declaring the behaviour is
// genuinely not render-from-wire. A unit-only proof set with no opt-out is the
// exact shape that shipped two render bugs behind green unit fakes, so it is a
// failure. The opt-out reason must be non-empty.
//
// The check classifies each FE citation by its RESOLVED path (not by the
// declared prefix — checkUIRefs already rejected mislabeled citations). A bare
// legacy `ui:` token resolves to e2e or unit by where its spec lives, so old
// plans cannot slip through the alias.
func checkRenderFromWireGate(e acEntry, fe feIndex) int {
	feCites := uiCiteRe.FindAllString(e.verify, -1)
	if len(feCites) == 0 {
		return 0
	}
	hasE2E := false
	hasUnit := false
	for _, cite := range feCites {
		hits := fe.matches(cite)
		if len(hits) != 1 {
			continue // checkUIRefs already reported the miss/ambiguity.
		}
		if feProofKind(hits[0]) == feKindE2E {
			hasE2E = true
		} else {
			hasUnit = true
		}
	}
	if hasE2E {
		return 0
	}
	if !hasUnit {
		return 0 // no resolvable FE proof at all; checkUIRefs reported it.
	}
	// Unit-only proof set. Accept only with a non-empty `ui-unit-ok:` reason.
	if reason := uiUnitOkReason(e.verify); reason != "" {
		return 0
	}
	fmt.Printf("  ✗ %s  render-from-wire AC has only unit FE proofs; add a ui-e2e: proof or declare ui-unit-ok: <reason>\n", e.id)
	return 1
}

// uiUnitOkReason returns the opt-out reason text on a verify line, or "" when
// the line carries no `ui-unit-ok:` token or the reason is empty. The reason
// is mandatory: a bare `ui-unit-ok:` with nothing after it does not opt out.
func uiUnitOkReason(verify string) string {
	m := uiUnitOkRe.FindStringSubmatch(verify)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// checkGrounding verifies every bare-text ADR / Principle cited in an AC's prose
// resolves to a real decision. Linked citations (`[ADR-xxxx](...)`) are excluded
// — `task docs-check` already covers those. Returns the count of dangling
// references.
func checkGrounding(e acEntry, ground grounding) int {
	failures := 0
	// Strip inline-code spans so a backtick-wrapped reference used as a literal
	// example is not mistaken for a citation that must resolve.
	body := codeSpanRe.ReplaceAllString(e.body, " ")
	for _, m := range adrCiteRe.FindAllStringSubmatch(body, -1) {
		if strings.HasPrefix(m[2], "](") {
			continue // linked reference, covered by docs-check
		}
		n, _ := strconv.Atoi(m[1])
		if !ground.adrNums[n] {
			failures++
			fmt.Printf("  ✗ %s  cites ADR-%s — no such ADR\n", e.id, m[1])
		}
	}
	for _, m := range principleCiteRe.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > ground.principleCount {
			failures++
			fmt.Printf("  ✗ %s  cites Principle %s — out of range 1..%d\n", e.id, m[1], ground.principleCount)
		}
	}
	return failures
}

// hasAnyVerify reports whether any AC carries a verify: field (structured mode).
func hasAnyVerify(acs []acEntry) bool {
	for _, e := range acs {
		if e.hasVerify {
			return true
		}
	}
	return false
}

// checkStructured evaluates a plan's verify: fields. Test-method criteria have
// every named Go test and `ui:<path>` reference checked; non-test methods are
// accepted.
func checkStructured(path string, acs []acEntry, index map[string]string, fe feIndex, cfg Config) int {
	fmt.Printf("== %s ==  [structured]\n", strings.TrimPrefix(path, "./"))
	missing, unannotated, testACs, methodACs := 0, 0, 0, 0
	for _, e := range acs {
		switch {
		case !e.hasVerify:
			unannotated++
			fmt.Printf("  ? %s  (no verify: field)\n", e.id)
		case isMethod(e.verify):
			methodACs++
		default:
			testACs++
			for _, name := range testCiteRe.FindAllString(e.verify, -1) {
				if !resolve(name, index) {
					missing++
					fmt.Printf("  ✗ %s  verify names %s — MISSING\n", e.id, name)
				}
			}
			missing += checkUIRefs(e, fe)
			missing += checkResolverRefs(e, cfg)
		}
	}
	fmt.Printf("  %d ACs · %d test-verified · %d method · %d unannotated · %d missing\n",
		len(acs), testACs, methodACs, unannotated, missing)
	return missing
}

// checkProse is the legacy scan: every test name the plan cites must exist,
// excusing a missing test whose citing line documents it as superseded.
func checkProse(path string, lines []string, index map[string]string) int {
	seen := map[string]citation{}
	for _, line := range lines {
		names := testCiteRe.FindAllString(line, -1)
		if len(names) == 0 {
			continue
		}
		ac := acRe.FindString(line)
		sup := strings.Contains(strings.ToLower(line), "supersed")
		for _, name := range names {
			c, ok := seen[name]
			if !ok {
				c = citation{test: name, ac: ac}
			}
			if sup {
				c.superseded = true // sticky: any citing line marking it removed wins
			}
			seen[name] = c
		}
	}

	cites := make([]citation, 0, len(seen))
	for _, c := range seen {
		cites = append(cites, c)
	}
	sort.Slice(cites, func(i, j int) bool { return cites[i].test < cites[j].test })

	fmt.Printf("== %s ==  [prose]\n", strings.TrimPrefix(path, "./"))
	var missing []citation
	found, superseded := 0, 0
	for _, c := range cites {
		switch {
		case resolve(c.test, index):
			found++
		case c.superseded:
			superseded++
			fmt.Printf("  ~ %s  (cited %s, documented superseded — OK)\n", c.test, acOrDash(c.ac))
		default:
			missing = append(missing, c)
		}
	}
	for _, c := range missing {
		fmt.Printf("  ✗ %s  (cited %s) MISSING\n", c.test, acOrDash(c.ac))
	}
	fmt.Printf("  %d cited, %d found, %d superseded, %d missing\n",
		len(cites), found, superseded, len(missing))
	return len(missing)
}

func acOrDash(ac string) string {
	if ac == "" {
		return "no-AC"
	}
	return ac
}

// backwardViolations reports, in sorted order, every TestADR_NNNN_* name that
// orphans the ADR it pins. A pin is a violation when the ADR is missing (no
// docs/adr/NNNN-*.md file, so no entry in statusByNum), OR the ADR is retired
// (adrstatus classifies it "superseded" or "deprecated") AND has no resolvable
// supersessor. A retired ADR has a resolvable supersessor when its Status line
// names a supersessor ADR (supersessorByNum) that itself exists in statusByNum
// — the behaviour has been re-homed under the supersessor, so the pin is not
// orphaned and is tolerated. A test naming an accepted or proposed ADR passes —
// a design-only, proposed ADR may legitimately carry a landed test.
func backwardViolations(testNames []string, statusByNum map[int]string, supersessorByNum map[int]int) []string {
	var bad []string
	for _, name := range testNames {
		m := adrTestNameRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1]) // m[1] is exactly four digits.
		status, ok := statusByNum[n]
		switch {
		case !ok:
			bad = append(bad, name) // names an ADR that does not exist
		case isRetiredStatus(status) && !hasResolvableSupersessor(n, statusByNum, supersessorByNum):
			bad = append(bad, name) // retired with nowhere to re-home the behaviour
		}
	}
	sort.Strings(bad)
	return bad
}

// hasResolvableSupersessor reports whether retired ADR n names a supersessor
// ADR that itself exists in the ADR set. A retired ADR with a resolvable
// supersessor has had its behaviour re-homed, so a TestADR_* still pinning it
// is not orphaned.
func hasResolvableSupersessor(n int, statusByNum map[int]string, supersessorByNum map[int]int) bool {
	sup, ok := supersessorByNum[n]
	if !ok {
		return false
	}
	_, exists := statusByNum[sup]
	return exists
}

// isRetiredStatus reports whether an adrstatus classification names a retired
// ADR — "superseded" or "deprecated". A TestADR_* pinning a retired ADR is a
// backward-gate violation unless the ADR names a resolvable supersessor (the
// behaviour is re-homed, not orphaned); see backwardViolations.
func isRetiredStatus(status string) bool {
	switch strings.ToLower(status) {
	case "superseded", "deprecated":
		return true
	default:
		return false
	}
}

// adrTestNames returns every TestADR_NNNN_* name in the test-declaration index,
// the set whose pinned ADR the backward gate checks.
func adrTestNames(index map[string]string) []string {
	var names []string
	for name := range index {
		if adrTestNameRe.MatchString(name) {
			names = append(names, name)
		}
	}
	return names
}

// loadADRStatusByNumber classifies every ADR under docs/adr/ into a
// status-by-number map. It mirrors the original adrstatus classification so
// both status forms behave identically: a struck-through status line reads as
// superseded/deprecated, otherwise the first word wins. Inlining it here
// removes the external process dependency the extracted package would
// otherwise carry.
func loadADRStatusByNumber(root string) (map[int]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, "docs/adr/[0-9][0-9][0-9][0-9]-*.md"))
	if err != nil {
		return nil, fmt.Errorf("globbing ADR files: %w", err)
	}
	byNum := make(map[int]string, len(matches))
	for _, p := range matches {
		base := filepath.Base(p)
		n, convErr := strconv.Atoi(base[:4])
		if convErr != nil {
			continue
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, fmt.Errorf("reading %s: %w", base, readErr)
		}
		// A present ADR file always gets an entry. The original adrstatus.parseADR
		// defaulted an unrecognized status to "unknown" rather than omitting the
		// ADR; the backward gate treats a missing entry as a missing ADR (a
		// violation), so omitting a present-but-statusless ADR would wrongly flag
		// its TestADR_* pins. "unknown" is not retired, so it is not a violation.
		byNum[n] = "unknown"
		lines := strings.Split(string(b), "\n")
		for i, ln := range lines {
			if m := statusLineRE.FindStringSubmatch(ln); m != nil {
				byNum[n] = classifyStatus(m[1])
				break
			}
			// `## Status` heading: classify the next non-empty line.
			if headRE.MatchString(ln) {
				for _, next := range lines[i+1:] {
					if w := strings.TrimSpace(next); w != "" {
						byNum[n] = classifyStatus(w)
						break
					}
				}
				break
			}
		}
	}
	return byNum, nil
}

// classifyStatus reduces a status line's prose to one lifecycle word. The
// signal for a retired ADR is the STRIKETHROUGH of its old status
// (`~~accepted~~. **Superseded ... by**`), not the mere presence of the word
// "superseded" — a line may mention another ADR being superseded ("ADR-0006
// is now superseded") or use a compound like "non-deprecated", and those must
// stay "accepted". When the leading status is struck through, the retired
// keyword on the line names the kind; otherwise the first word wins. This is
// the classifyStatus function inlined from the standalone adrstatus tool.
func classifyStatus(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "~~") {
		if m := retiredRE.FindString(s); m != "" {
			return strings.ToLower(m)
		}
		return "superseded" // struck through but unlabeled — still retired
	}
	if w := firstWordRE.FindString(s); w != "" {
		return strings.ToLower(w)
	}
	return "unknown"
}

// loadSupersessorByNumber parses every retired ADR's Status line for the
// supersessor ADR it names, building a map from retired ADR number to
// supersessor number. It reuses the docs/adr/NNNN-*.md glob and four-digit
// parsing the rest of the file uses. An ADR with no "Superseded … by" /
// "Deprecated … by" reference contributes no entry, so a retired-but-orphaned
// ADR is left out of the map and stays a backward-gate violation.
func loadSupersessorByNumber(root string) (map[int]int, error) {
	byNum := map[int]int{}
	matches, err := filepath.Glob(filepath.Join(root, "docs/adr/[0-9][0-9][0-9][0-9]-*.md"))
	if err != nil {
		return nil, fmt.Errorf("globbing ADR files: %w", err)
	}
	for _, p := range matches {
		base := filepath.Base(p)
		n, convErr := strconv.Atoi(base[:4])
		if convErr != nil {
			continue
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, fmt.Errorf("reading %s: %w", base, readErr)
		}
		for _, ln := range strings.Split(string(b), "\n") {
			if statusLineRE.FindStringSubmatch(ln) == nil {
				continue
			}
			if m := supersessorRe.FindStringSubmatch(ln); m != nil {
				if sup, supErr := strconv.Atoi(m[1]); supErr == nil {
					byNum[n] = sup
				}
			}
			break // one Status line per ADR
		}
	}
	return byNum, nil
}

// runBackwardGate reports the backward half of the gate: a TestADR_NNNN_* test
// must name an ADR that exists and is either not retired or retired with a
// resolvable supersessor (the behaviour re-homed). It returns the number of
// offending tests.
func runBackwardGate(root string, index map[string]string) (int, error) {
	statusByNum, err := loadADRStatusByNumber(root)
	if err != nil {
		return 0, err
	}
	supersessorByNum, err := loadSupersessorByNumber(root)
	if err != nil {
		return 0, err
	}
	bad := backwardViolations(adrTestNames(index), statusByNum, supersessorByNum)
	fmt.Printf("== backward gate: TestADR_NNNN_* pins ==\n")
	for _, name := range bad {
		fmt.Printf("  ✗ %s  names an ADR that is missing, or retired with no resolvable supersessor\n", name)
	}
	fmt.Printf("  %d TestADR_* pin(s) over a missing or orphaned-retired ADR\n", len(bad))
	return len(bad), nil
}
