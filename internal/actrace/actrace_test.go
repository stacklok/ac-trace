// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: LicenseRef-Stacklok-Proprietary

package actrace

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_ExactFamilyAndMiss(t *testing.T) {
	t.Parallel()
	index := map[string]string{
		"TestADR_0061_AdminWritesSkill": "a_test.go",
		"TestADR_0061_CatalogServed":    "b_test.go",
		"TestPrinciple4_TokenBoundary":  "c_test.go",
		"TestSearch_FooBar":             "d_test.go",
	}
	cases := []struct {
		name string
		cite string
		want bool
	}{
		{"exact match", "TestPrinciple4_TokenBoundary", true},
		// A bare name with no trailing "_" is fully-qualified: it must match a
		// declared test EXACTLY. "TestADR_0061" itself is not declared (only its
		// longer members are), so it is a fully-qualified miss — it must NOT
		// resolve via a longer sibling that merely shares its prefix.
		{"family stem no trailing underscore", "TestADR_0061", false},
		{"family stem trailing underscore", "TestADR_0061_", true},
		{"specific member of family", "TestADR_0061_AdminWritesSkill", true},
		{"unknown test", "TestADR_9999_Nope", false},
		// A missing specific name must not be rescued by a longer test that
		// merely shares its prefix without a "_" boundary.
		{"prefix without underscore boundary", "TestSearch_Foo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, resolve(tc.cite, index))
		})
	}
}

func TestCiteRegex_RejectsEnglishWords(t *testing.T) {
	t.Parallel()
	// "Tests" and "Testing" are prose, not citations; real test identifiers
	// have an uppercase letter or "_" after "Test".
	got := testCiteRe.FindAllString(
		"The Tests and Testing prose, plus TestADR_0019_Foo and TestPrinciple4_Bar.", -1)
	assert.Equal(t, []string{"TestADR_0019_Foo", "TestPrinciple4_Bar"}, got)
}

func TestCheckProse_CountsMissingAndExcusesSuperseded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := filepath.Join(dir, "sample.md")
	body := "" +
		"- AC1.1: behaviour holds (`TestThing_Exists`).\n" +
		"- AC1.2: the old `TestThing_Gone` is superseded; the param was dropped.\n" +
		"- AC1.3: behaviour holds (`TestThing_Missing`).\n"
	require.NoError(t, os.WriteFile(plan, []byte(body), 0o644))

	index := map[string]string{"TestThing_Exists": "x_test.go"}

	// Prose path: no verify: fields, so the legacy whole-plan scan applies.
	missing := checkProse(plan, strings.Split(body, "\n"), index)
	// Exists → found; Gone → excused (superseded); Missing → the one gap.
	assert.Equal(t, 1, missing)
}

func TestIsMethod(t *testing.T) {
	t.Parallel()
	cases := []struct {
		verify string
		want   bool
	}{
		{"none — reserved with no V1 producer", true},
		{"inspection", true},
		{"demonstration — cluster e2e", true},
		{"`TestFoo_Bar`", false},
		{"`TestFoo`, `TestBar`", false},
		{"", true}, // empty: nothing to check
	}
	for _, tc := range cases {
		t.Run(tc.verify, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isMethod(tc.verify))
		})
	}
}

func TestCheckStructured_IgnoresProseAndCountsMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plan := filepath.Join(dir, "structured.md")
	// AC1.4 cites a retired test name in PROSE; its verify: field is a method,
	// so the prose mention must NOT count as missing — the false-positive the
	// structured field exists to kill.
	body := "" +
		"- AC1.1: behaviour holds.\n" +
		"  - verify: `TestThing_Exists`\n" +
		"- AC1.2: closed by construction.\n" +
		"  - verify: none — reserved with no V1 producer\n" +
		"- AC1.3: behaviour holds.\n" +
		"  - verify: `TestThing_Missing`\n" +
		"- AC1.4: the old `TestThing_Retired` name is gone.\n" +
		"  - verify: inspection\n"
	require.NoError(t, os.WriteFile(plan, []byte(body), 0o644))

	index := map[string]string{"TestThing_Exists": "x_test.go"}

	acs := parseACs(strings.Split(body, "\n"))
	missing := checkStructured(plan, acs, index, nil)
	// Only TestThing_Missing counts; the prose `TestThing_Retired` is ignored.
	assert.Equal(t, 1, missing)
}

func TestParseACs_RetainsCriterionBody(t *testing.T) {
	t.Parallel()
	// The AC line names a bare-text ADR and Principle in its prose; parseACs
	// must keep that body so grounding can check the references.
	lines := []string{
		"- AC1.1: the token never enters the loop, per ADR-0042 and Principle 4.",
		"  - verify: `TestThing_Exists`",
	}
	acs := parseACs(lines)
	require.Len(t, acs, 1)
	assert.Contains(t, acs[0].body, "ADR-0042")
	assert.Contains(t, acs[0].body, "Principle 4")
}

func TestParseStatus_ClassifiesAndRejectsTypos(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		line    string
		want    string
		wantErr bool
	}{
		{"draft", "**Status:** draft, 2026-06-17.", "draft", false},
		{"in-progress", "**Status:** in-progress, 2026-06-17.", "in-progress", false},
		{"landed", "**Status:** landed, 2026-06-17.", "landed", false},
		{"superseded", "**Status:** superseded by foo.", "superseded", false},
		{"typo errors", "**Status:** landeed, 2026-06-17.", "", true},
		{"missing status line errors", "no status here", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStatus([]string{tc.line})
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestADR_0065_LandedPlanVerifyTestMustExist pins the plan-status lifecycle and
// the forward half of the actrace gate from ADR-0065: a landed plan must carry
// at least one structured criterion, every criterion must name a resolving
// verify: test (or a non-test method with a reason), and every bare-text ADR /
// Principle it cites must ground to a real decision. draft and in-progress
// plans are reported but never gate; a superseded plan is skipped; an
// unrecognised status is a hard error so a typo cannot silently disable the
// gate.
func TestADR_0065_LandedPlanVerifyTestMustExist(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"TestThing_Exists":              "x_test.go",
		"TestADR_0061_AdminWritesSkill": "a_test.go",
	}
	// Grounding: ADR 42 exists, principles run 1..13. ADR 9999 and Principle 99
	// do not resolve.
	ground := grounding{adrNums: map[int]bool{42: true, 61: true}, principleCount: 13}

	const verifyExists = "  - verify: `TestThing_Exists`\n"

	cases := []struct {
		name string
		// body is the plan markdown after the status line.
		body string
		// status is the **Status:** word the plan declares.
		status string
		// wantFail: with --strict, does runPlan report this plan as failing?
		wantFail bool
		// wantSkip: is the plan skipped entirely (superseded)?
		wantSkip bool
		// wantStatusErr: is the status itself a hard error?
		wantStatusErr bool
	}{
		{
			name:     "draft plan with missing verify test does not fail",
			status:   "draft",
			body:     "- AC1.1: behaviour holds.\n  - verify: `TestThing_Missing`\n",
			wantFail: false,
		},
		{
			name:     "in-progress plan with missing verify test does not fail",
			status:   "in-progress",
			body:     "- AC1.1: behaviour holds.\n  - verify: `TestThing_Missing`\n",
			wantFail: false,
		},
		{
			name:     "landed plan with all checks passing does not fail",
			status:   "landed",
			body:     "- AC1.1: behaviour holds.\n" + verifyExists,
			wantFail: false,
		},
		{
			name:     "landed plan with the same missing verify test fails",
			status:   "landed",
			body:     "- AC1.1: behaviour holds.\n  - verify: `TestThing_Missing`\n",
			wantFail: true,
		},
		{
			name:     "landed plan with an AC missing its verify field fails",
			status:   "landed",
			body:     "- AC1.1: behaviour holds.\n  some prose but no verify line.\n",
			wantFail: true,
		},
		{
			name:     "landed plan with a non-test method and reason passes",
			status:   "landed",
			body:     "- AC1.1: closed by construction.\n  - verify: none — reserved with no V1 producer\n",
			wantFail: false,
		},
		{
			name:   "landed plan whose verify only a longer sibling matches fails",
			status: "landed",
			// TestADR_0061 is fully-qualified (no trailing _); only the longer
			// TestADR_0061_AdminWritesSkill is declared, so it must not resolve.
			body:     "- AC1.1: behaviour holds.\n  - verify: `TestADR_0061`\n",
			wantFail: true,
		},
		{
			name:     "landed plan citing a missing ADR in prose fails",
			status:   "landed",
			body:     "- AC1.1: behaviour holds, per ADR-9999.\n" + verifyExists,
			wantFail: true,
		},
		{
			name:     "landed plan citing an out-of-range Principle fails",
			status:   "landed",
			body:     "- AC1.1: behaviour holds, per Principle 99.\n" + verifyExists,
			wantFail: true,
		},
		{
			name:     "landed plan citing a real ADR and Principle in prose passes",
			status:   "landed",
			body:     "- AC1.1: behaviour holds, per ADR-0042 and Principle 4.\n" + verifyExists,
			wantFail: false,
		},
		{
			name:     "landed plan with zero ACs fails",
			status:   "landed",
			body:     "Some narrative with no numbered acceptance criteria at all.\n",
			wantFail: true,
		},
		{
			name:     "superseded plan is skipped entirely",
			status:   "superseded",
			body:     "- AC1.1: behaviour holds.\n  - verify: `TestThing_Missing`\n",
			wantSkip: true,
		},
		{
			name:          "unrecognised status is a hard error",
			status:        "landeed",
			body:          "- AC1.1: behaviour holds.\n" + verifyExists,
			wantStatusErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			plan := filepath.Join(dir, "plan.md")
			content := "# A plan\n\n**Status:** " + tc.status + ", 2026-06-17.\n\n" + tc.body
			require.NoError(t, os.WriteFile(plan, []byte(content), 0o644))

			fail, skip, err := runPlan(plan, index, nil, ground)
			if tc.wantStatusErr {
				require.Error(t, err, "an unrecognised status must be a hard error")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSkip, skip, "skip decision")
			// fail>0 means --strict would exit non-zero on this plan.
			assert.Equal(t, tc.wantFail, fail > 0, "strict-failure decision")
		})
	}
}

// TestADR_0065_TestNamingSupersededAdrRejected pins the backward half of the
// actrace gate from ADR-0065: a TestADR_NNNN_* test cannot outlive the ADR it
// defends. The check is fed the adrstatus records (ADR number + classified
// status) and the set of TestADR_NNNN_* names found in the tree, so it never
// spawns a process or mutates a real ADR. It fails when a test names an ADR
// with no file or an ADR classified superseded / deprecated, and passes when
// the named ADR is accepted or proposed — a design-only, proposed ADR may
// legitimately carry a landed test.
func TestADR_0065_TestNamingSupersededAdrRejected(t *testing.T) {
	t.Parallel()

	// Status of each ADR by number, as adrstatus would classify it. ADR 99 is
	// deliberately absent — no record, no file.
	statusByNum := map[int]string{
		42: "accepted",
		65: "proposed",
		6:  "superseded",
		7:  "deprecated",
	}

	cases := []struct {
		name string
		// testNames is the set of TestADR_NNNN_* names found in the tree.
		testNames []string
		// wantViolations is the exact list of offending test names the gate
		// must report, deriving the forbidden set from the input rather than a
		// hard-coded count.
		wantViolations []string
	}{
		{
			name:           "test naming an accepted ADR passes",
			testNames:      []string{"TestADR_0042_SomethingHolds"},
			wantViolations: nil,
		},
		{
			name:           "test naming a proposed ADR passes",
			testNames:      []string{"TestADR_0065_TestNamingSupersededAdrRejected"},
			wantViolations: nil,
		},
		{
			name:           "test naming a non-existent ADR fails",
			testNames:      []string{"TestADR_0099_NoSuchAdr"},
			wantViolations: []string{"TestADR_0099_NoSuchAdr"},
		},
		{
			name:           "test naming a superseded ADR fails",
			testNames:      []string{"TestADR_0006_OldDecision"},
			wantViolations: []string{"TestADR_0006_OldDecision"},
		},
		{
			name:           "test naming a deprecated ADR fails",
			testNames:      []string{"TestADR_0007_RetiredDecision"},
			wantViolations: []string{"TestADR_0007_RetiredDecision"},
		},
		{
			name: "a clean set with several accepted and proposed pins passes",
			testNames: []string{
				"TestADR_0042_SomethingHolds",
				"TestADR_0065_TestNamingSupersededAdrRejected",
			},
			wantViolations: nil,
		},
		{
			name: "one bad pin among good ones is reported, the good ones are not",
			testNames: []string{
				"TestADR_0042_SomethingHolds",
				"TestADR_0006_OldDecision",
			},
			wantViolations: []string{"TestADR_0006_OldDecision"},
		},
	}

	// No retired ADR here names a supersessor, so every retired pin is still a
	// violation — this test pins the no-supersessor case unchanged.
	supersessorByNum := map[int]int{}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := backwardViolations(tc.testNames, statusByNum, supersessorByNum)
			assert.Equal(t, tc.wantViolations, got, "offending TestADR_* names")
		})
	}
}

// TestBackwardGate_ToleratesSupersededWithResolvableSupersessor pins the
// refinement the agent-loop-engine-import plan needs: a TestADR_NNNN_* that
// names a superseded ADR is NOT a violation when that ADR names a supersessor
// ADR which itself exists — the behaviour is re-homed, not orphaned. A
// superseded ADR that names no supersessor (or one that does not exist) is
// still a violation, and a missing ADR is still a violation.
func TestBackwardGate_ToleratesSupersededWithResolvableSupersessor(t *testing.T) {
	t.Parallel()

	// ADR 31 is superseded by 68 (which exists); ADR 35 is superseded by 68
	// too. ADR 6 is superseded but names a supersessor (88) that does not
	// exist. ADR 7 is superseded and names no supersessor at all. ADR 99 is
	// missing entirely. ADR 42 is accepted.
	statusByNum := map[int]string{
		31: "superseded",
		35: "superseded",
		68: "accepted",
		6:  "superseded",
		7:  "superseded",
		42: "accepted",
	}
	supersessorByNum := map[int]int{
		31: 68,
		35: 68,
		6:  88, // supersessor does not exist in statusByNum
		// 7 deliberately absent: no supersessor parsed.
	}

	cases := []struct {
		name           string
		testNames      []string
		wantViolations []string
	}{
		{
			name:           "superseded by an ADR that exists is tolerated",
			testNames:      []string{"TestADR_0031_FunctionCalling"},
			wantViolations: nil,
		},
		{
			name: "two pins on ADRs superseded by the same existing ADR are tolerated",
			testNames: []string{
				"TestADR_0031_FunctionCalling",
				"TestADR_0035_DenyPolicy",
			},
			wantViolations: nil,
		},
		{
			name:           "superseded but supersessor does not exist is a violation",
			testNames:      []string{"TestADR_0006_OldDecision"},
			wantViolations: []string{"TestADR_0006_OldDecision"},
		},
		{
			name:           "superseded but names no supersessor is a violation",
			testNames:      []string{"TestADR_0007_NoSupersessor"},
			wantViolations: []string{"TestADR_0007_NoSupersessor"},
		},
		{
			name:           "missing ADR is a violation",
			testNames:      []string{"TestADR_0099_NoSuchAdr"},
			wantViolations: []string{"TestADR_0099_NoSuchAdr"},
		},
		{
			name:           "accepted ADR is not a violation",
			testNames:      []string{"TestADR_0042_SomethingHolds"},
			wantViolations: nil,
		},
		{
			name: "tolerated and violating pins are separated correctly",
			testNames: []string{
				"TestADR_0031_FunctionCalling",
				"TestADR_0006_OldDecision",
				"TestADR_0007_NoSupersessor",
				"TestADR_0099_NoSuchAdr",
			},
			wantViolations: []string{
				"TestADR_0006_OldDecision",
				"TestADR_0007_NoSupersessor",
				"TestADR_0099_NoSuchAdr",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := backwardViolations(tc.testNames, statusByNum, supersessorByNum)
			assert.Equal(t, tc.wantViolations, got, "offending TestADR_* names")
		})
	}
}

func TestLoadSupersessorByNumber_ParsesRetiredStatusLines(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	adrDir := filepath.Join(root, "docs", "adr")
	require.NoError(t, os.MkdirAll(adrDir, 0o755))

	// A linked "Superseded by [ADR-0068](...)" line — the common shape.
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(adrDir, name), []byte(body), 0o644))
	}
	write("0031-in-house-loop.md",
		"# ADR-0031\n\n**Status:** ~~accepted~~. **Superseded by [ADR-0068](./0068-x.md), 2026-06-19.**\n")
	write("0035-harvest.md",
		"# ADR-0035\n\n**Status:** ~~accepted~~. **Superseded by [ADR-0068](./0068-x.md).**\n")
	// A bare-text "Deprecated in favour of ADR-0042" line — no markdown link.
	write("0010-old.md",
		"# ADR-0010\n\n**Status:** Deprecated by ADR-0042.\n")
	// An accepted ADR names no supersessor.
	write("0042-current.md",
		"# ADR-0042\n\n**Status:** accepted, 2026-01-01.\n")
	// A superseded ADR whose status line names no supersessor at all.
	write("0007-orphaned.md",
		"# ADR-0007\n\n**Status:** ~~accepted~~. Superseded.\n")

	got, err := loadSupersessorByNumber(root)
	require.NoError(t, err)

	want := map[int]int{
		31: 68,
		35: 68,
		10: 42,
	}
	assert.Equal(t, want, got, "retired ADR number → supersessor number")
}

func TestParseACs_FindsVerifyAfterWrappedCriterion(t *testing.T) {
	t.Parallel()
	// The criterion text wraps across lines, so the verify: sub-line is not
	// immediately after the AC line — parseACs must scan the whole block.
	lines := []string{
		"- AC1.1: a long criterion that wraps",
		"  onto a second and third",
		"  continuation line.",
		"  - verify: `TestThing_Exists`",
		"- AC1.2: next.",
		"  - verify: none — by construction",
	}
	acs := parseACs(lines)
	require.Len(t, acs, 2)
	assert.True(t, acs[0].hasVerify, "verify after wrapped criterion must be found")
	assert.Equal(t, "`TestThing_Exists`", acs[0].verify)
	assert.True(t, acs[1].hasVerify)
}

func TestCheckGrounding_IgnoresCodeSpans(t *testing.T) {
	t.Parallel()
	ground := grounding{adrNums: map[int]bool{42: true}, principleCount: 13}
	// Backtick-wrapped references are literal examples (an AC describing the
	// gate may say a `ADR-9999` / `Principle 99` reference fails) — not citations.
	example := acEntry{id: "AC1.1", body: "a bare-text `ADR-9999` or `Principle 99` that resolves to nothing fails the gate"}
	assert.Equal(t, 0, checkGrounding(example, ground))
	// A bare (non-code-span) dangling reference must still fail — both of them.
	bare := acEntry{id: "AC1.2", body: "grounded in ADR-9999 and Principle 99"}
	assert.Equal(t, 2, checkGrounding(bare, ground))
	// Real bare references pass.
	realRef := acEntry{id: "AC1.3", body: "per ADR-0042 and Principle 4"}
	assert.Equal(t, 0, checkGrounding(realRef, ground))
}

func TestFESpecStem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"ui/src/lib/atrium/__tests__/skill-loaded-stream.test.ts", "ui/src/lib/atrium/__tests__/skill-loaded-stream", true},
		{"ui/src/components/chat/__tests__/chip-unions-from-live-frame.test.tsx", "ui/src/components/chat/__tests__/chip-unions-from-live-frame", true},
		{"ui/tests/e2e/auth.spec.ts", "ui/tests/e2e/auth", true},
		// A leading "./" is trimmed so the stem is a stable repo-relative path.
		{"./ui/tests/e2e/chat.spec.tsx", "ui/tests/e2e/chat", true},
		{"internal/tools/actrace/main.go", "", false},
		{"foo_test.go", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got, ok := feSpecStem(tc.path)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFEIndex_Matches(t *testing.T) {
	t.Parallel()
	// Two specs share the basename "message-bubble"; "skill-loaded-stream" is
	// unique. The suffix rule on directory boundaries (against each path minus
	// its .test/.spec suffix) decides each lookup, and matches returns the real
	// spec file paths.
	fe := feIndex{
		"ui/src/components/chat/__tests__/message-bubble.test.tsx",
		"ui/src/components/skills/__tests__/message-bubble.test.tsx",
		"ui/src/lib/atrium/__tests__/skill-loaded-stream.test.ts",
	}
	cases := []struct {
		name string
		cite string
		want []string
	}{
		{
			name: "unique basename resolves to one spec",
			cite: "ui:skill-loaded-stream",
			want: []string{"ui/src/lib/atrium/__tests__/skill-loaded-stream.test.ts"},
		},
		{
			name: "colliding basename matches both specs",
			cite: "ui:message-bubble",
			want: []string{
				"ui/src/components/chat/__tests__/message-bubble.test.tsx",
				"ui/src/components/skills/__tests__/message-bubble.test.tsx",
			},
		},
		{
			name: "longer suffix qualifies the collision to one spec",
			cite: "ui:chat/__tests__/message-bubble",
			want: []string{"ui/src/components/chat/__tests__/message-bubble.test.tsx"},
		},
		{
			name: "no such spec matches nothing",
			cite: "ui:no-such-spec",
			want: nil,
		},
		{
			// A suffix must fall on a directory boundary: "bubble" is not a
			// suffix of ".../message-bubble" because it does not follow a "/".
			name: "partial path segment does not match",
			cite: "ui:bubble",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fe.matches(tc.cite))
		})
	}
}

func TestCheckLanded_ResolvesUITestReference(t *testing.T) {
	t.Parallel()
	fe := feIndex{
		"ui/src/lib/atrium/__tests__/skill-loaded-stream.test.ts",
		"ui/src/components/chat/__tests__/message-bubble.test.tsx",
		"ui/src/components/skills/__tests__/message-bubble.test.tsx",
	}
	ground := grounding{adrNums: map[int]bool{}, principleCount: 13}
	acs := []acEntry{
		// Unique basename resolves.
		{id: "AC1.1", verify: "ui:skill-loaded-stream", hasVerify: true, body: "AC1.1: a FE behaviour"},
		// No such spec — MISSING.
		{id: "AC1.2", verify: "ui:no-such-spec", hasVerify: true, body: "AC1.2: a missing FE behaviour"},
		// Shared basename — AMBIGUOUS.
		{id: "AC1.3", verify: "ui:message-bubble", hasVerify: true, body: "AC1.3: an ambiguous FE behaviour"},
		// Qualified with the parent dir — resolves to one spec.
		{id: "AC1.4", verify: "ui:chat/__tests__/message-bubble", hasVerify: true, body: "AC1.4: a qualified FE behaviour"},
	}
	// AC1.2 (missing) and AC1.3 (ambiguous) are the two failures; AC1.1 and
	// AC1.4 each single out exactly one spec. Note: AC1.1 and AC1.4 are
	// unit-only with no opt-out, but checkLanded's render-from-wire gate
	// fires on each — so each unit-only AC adds one gate failure on top of
	// the resolution failures. (AC1.2 has no resolvable FE proof so the gate
	// does not fire for it; AC1.3 is ambiguous so the gate also does not
	// fire — checkRenderFromWireGate skips cites that don't resolve to one.)
	assert.Equal(t, 4, checkLanded("fe.md", acs, map[string]string{}, fe, ground))
}

// TestADR_0065_RenderFromWireGateDefaultDeny pins the frontend half of the
// render-from-wire rule from .claude/rules/bff-route-handlers.md § "Proving
// FD-wire translation", enforced on landed plans by actrace: a criterion whose
// proof set contains front-end specs must include at least one Playwright e2e
// proof (a spec under ui/tests/e2e/), unless the verify line carries
// `ui-unit-ok: <reason>` declaring the behaviour is genuinely not
// render-from-wire. A bare legacy `ui:` token is classified by its resolved
// path, so old plans cannot slip through the alias. A `ui-e2e:` citation that
// resolves to a unit spec (or `ui-unit:` to an e2e spec) is a hard mislabel
// error.
func TestADR_0065_RenderFromWireGateDefaultDeny(t *testing.T) {
	t.Parallel()
	// One e2e spec under ui/tests/e2e/, two unit specs under ui/src/**.
	fe := feIndex{
		"ui/tests/e2e/chat-rich.spec.ts",
		"ui/src/lib/atrium/__tests__/safe-url.test.ts",
		"ui/src/components/chat/__tests__/message-bubble.test.tsx",
	}
	ground := grounding{adrNums: map[int]bool{}, principleCount: 13}

	cases := []struct {
		name     string
		verify   string
		wantFail bool
	}{
		{
			// (a) FE-only unit proof, no opt-out → FAIL (the bug class).
			name:     "unit-only no opt-out fails",
			verify:   "ui-unit:safe-url",
			wantFail: true,
		},
		{
			// (b) same unit proof with a ui-unit-ok: reason → PASS.
			name:     "unit-only with opt-out passes",
			verify:   "ui-unit:safe-url; ui-unit-ok: pure URL sanitizer, no wire render",
			wantFail: false,
		},
		{
			// opt-out with no reason text is not an opt-out → FAIL.
			name:     "opt-out with empty reason fails",
			verify:   "ui-unit:safe-url; ui-unit-ok:",
			wantFail: true,
		},
		{
			// (c) at least one ui-e2e: → PASS.
			name:     "at least one e2e passes",
			verify:   "ui-e2e:chat-rich, ui-unit:safe-url",
			wantFail: false,
		},
		{
			// (e) legacy bare ui: resolving under ui/src/** with no opt-out → FAIL.
			name:     "legacy ui under ui/src with no opt-out fails",
			verify:   "ui:message-bubble",
			wantFail: true,
		},
		{
			// Legacy bare ui: that resolves under ui/tests/e2e/ → PASS (e2e by path).
			name:     "legacy ui under ui/tests/e2e passes",
			verify:   "ui:chat-rich",
			wantFail: false,
		},
		{
			// A Go-test-only AC (no FE proofs) is untouched by the gate.
			name:     "go-test-only AC is untouched",
			verify:   "`TestThing_Exists`",
			wantFail: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			index := map[string]string{"TestThing_Exists": "x_test.go"}
			acs := []acEntry{{id: "AC1.1", verify: tc.verify, hasVerify: true, body: "AC1.1: a behaviour"}}
			got := checkLanded("p.md", acs, index, fe, ground)
			assert.Equal(t, tc.wantFail, got > 0, "strict-failure decision")
		})
	}
}

// TestADR_0065_RenderFromWireGateMislabel pins the mislabel check: a citation that
// declares `ui-e2e:` but resolves to a unit spec (or `ui-unit:` to an e2e
// spec) is a hard error, since classifying by the citation string would hide a
// unit-only proof behind an e2e claim.
func TestADR_0065_RenderFromWireGateMislabel(t *testing.T) {
	t.Parallel()
	fe := feIndex{
		"ui/tests/e2e/chat-rich.spec.ts",
		"ui/src/lib/atrium/__tests__/safe-url.test.ts",
	}
	ground := grounding{adrNums: map[int]bool{}, principleCount: 13}

	cases := []struct {
		name     string
		verify   string
		wantFail bool
	}{
		{
			name:     "ui-e2e resolving to a unit spec is a mislabel",
			verify:   "ui-e2e:safe-url",
			wantFail: true,
		},
		{
			name:     "ui-unit resolving to an e2e spec is a mislabel",
			verify:   "ui-unit:chat-rich",
			wantFail: true,
		},
		{
			name:     "correctly-labeled e2e passes",
			verify:   "ui-e2e:chat-rich",
			wantFail: false,
		},
		{
			name:     "correctly-labeled unit with opt-out passes",
			verify:   "ui-unit:safe-url; ui-unit-ok: pure helper",
			wantFail: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			acs := []acEntry{{id: "AC1.1", verify: tc.verify, hasVerify: true, body: "AC1.1: a behaviour"}}
			got := checkLanded("p.md", acs, map[string]string{}, fe, ground)
			assert.Equal(t, tc.wantFail, got > 0, "strict-failure decision")
		})
	}
}

// captureOutput redirects os.Stdout to a pipe for the duration of fn, then
// restores it and returns everything that was written. Used to assert the exact
// diagnostic message a gate failure prints, not merely that a failure occurred.
func captureOutput(fn func()) string {
	r, w, err := os.Pipe()
	if err != nil {
		panic("captureOutput: os.Pipe: " + err.Error())
	}
	old := os.Stdout
	os.Stdout = w
	defer func() {
		w.Close() // no-op on normal path; ensures close on panic
		os.Stdout = old
	}()
	fn()
	w.Close() // signal EOF so ReadAll does not block
	b, _ := io.ReadAll(r)
	return string(b)
}

// TestADR_0065_RealfdProofType pins the ui-e2e-realfd: proof type: it resolves
// like ui-e2e: (satisfying the render-from-wire gate) but additionally requires
// the resolved spec filename to end in .realfd.spec.ts. Each mislabel case
// asserts the SPECIFIC diagnostic message that fired, not merely that some
// failure occurred — so a refactor collapsing the two mislabel branches would
// still be caught (the wrong message would appear instead of the right one).
func TestADR_0065_RealfdProofType(t *testing.T) { //nolint:paralleltest // captureOutput swaps os.Stdout globally; running in parallel races with other tests that print
	fe := feIndex{
		"ui/tests/e2e/skill-loaded.realfd.spec.ts",
		"ui/tests/e2e/chat-rich.spec.ts",
		"ui/src/lib/atrium/__tests__/safe-url.test.ts",
	}
	ground := grounding{adrNums: map[int]bool{}, principleCount: 13}

	cases := []struct {
		name        string
		verify      string
		wantFail    bool
		wantMessage string // non-empty: the specific diagnostic substring that must appear
	}{
		{
			// A correctly-labeled realfd citation resolves and satisfies the
			// render-from-wire gate (it is classified as e2e by its path).
			name:     "correctly-labeled realfd citation passes",
			verify:   "ui-e2e-realfd:skill-loaded.realfd",
			wantFail: false,
		},
		{
			// A realfd citation pointing at a plain .spec.ts (not .realfd.spec.ts)
			// must fire the "does not end in .realfd.spec.ts" diagnostic, not the
			// unit-resolution diagnostic — so that collapsing the two branches into
			// one generic "MISLABELED" message would break this assertion.
			name:        "realfd citation pointing at plain spec fires suffix diagnostic",
			verify:      "ui-e2e-realfd:chat-rich",
			wantFail:    true,
			wantMessage: "does not end in .realfd.spec.ts",
		},
		{
			// A realfd citation pointing at a unit spec (wrong directory) must fire
			// the unit-resolution diagnostic, not the suffix diagnostic.
			name:        "realfd citation pointing at unit spec fires unit-resolution diagnostic",
			verify:      "ui-e2e-realfd:safe-url",
			wantFail:    true,
			wantMessage: "declared ui-e2e-realfd: but resolves to unit spec",
		},
		{
			// A realfd proof combined with a plain e2e proof satisfies the gate.
			name:     "realfd plus e2e both pass",
			verify:   "ui-e2e:chat-rich, ui-e2e-realfd:skill-loaded.realfd",
			wantFail: false,
		},
	}

	// Note: subtests are not parallel here — captureOutput redirects os.Stdout
	// globally, so parallel subtests would race on the pipe. Each case runs
	// sequentially within the (parallel) parent.
	for _, tc := range cases { //nolint:paralleltest,tparallel // captureOutput redirects os.Stdout globally; parallel subtests would race on the pipe
		t.Run(tc.name, func(t *testing.T) {
			acs := []acEntry{{id: "AC1.1", verify: tc.verify, hasVerify: true, body: "AC1.1: a behaviour"}}
			var failures int
			out := captureOutput(func() {
				failures = checkLanded("p.md", acs, map[string]string{}, fe, ground)
			})
			assert.Equal(t, tc.wantFail, failures > 0, "strict-failure decision")
			if tc.wantMessage != "" {
				assert.Contains(t, out, tc.wantMessage, "expected specific diagnostic message in output")
			}
		})
	}
}
