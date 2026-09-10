// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package actrace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestADR_0065_OrphanGateExcusesMarkedScaffolding pins the gate-graduation rule:
// a scenario test carrying an //actrace:untracked-ok marker on the line above its
// declaration is collected as excused (reported, never a --strict failure), so
// harness scaffolding does not fail the gate; an unmarked one is not excused.
func TestADR_0065_OrphanGateExcusesMarkedScaffolding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "test", "integration", "foo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	src := "package foo\n\nimport \"testing\"\n\n" +
		"// actrace:untracked-ok — scaffolding smoke, proves no AC\n" +
		"func TestFoo_Scenario1_Smoke(t *testing.T) { _ = t }\n\n" +
		"func TestFoo_Scenario2_Real(t *testing.T) { _ = t }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o644))

	got, err := collectScenarioTests(root)
	require.NoError(t, err)
	excused := map[string]bool{}
	for _, s := range got {
		excused[s.name] = s.excused
	}
	require.Contains(t, excused, "TestFoo_Scenario1_Smoke")
	require.Contains(t, excused, "TestFoo_Scenario2_Real")
	assert.True(t, excused["TestFoo_Scenario1_Smoke"], "marker above func excuses it from the gate")
	assert.False(t, excused["TestFoo_Scenario2_Real"], "unmarked scenario test is not excused")
}

// TestADR_0065_ReverseOrphanReport pins the orphan half of the reverse checks
// from ADR-0065: a scenario test (Test<Plan>_Scenario<N>_*) claimed
// by no landed plan's verify: field is reported untracked, and a scenario test a
// landed plan claims — directly or through a family stem — is not. The check is
// scoped to scenario tests so the hundreds of TestADR_*/TestPrinciple* tests
// that trace to a decision directly are never flagged.
func TestADR_0065_ReverseOrphanReport(t *testing.T) {
	t.Parallel()

	// Sorted by name, as collectScenarioTests hands them to the check;
	// untrackedScenarioTests preserves that order.
	scenarios := []scenarioTest{
		{name: "TestCollabKnowledge_Scenario1_MultiUserProjects", file: "test/integration/collab/s1_test.go"},
		{name: "TestCredentialAccessIsolation_Scenario1_SubjectKeyedRetrievalAndStamp", file: "test/integration/cred/s1_test.go"},
		{name: "TestIntegrationMVP_Scenario1_CreateChat_HappyPath", file: "test/integration/mvp/s1_test.go"},
		{name: "TestIntegrationMVP_Scenario2_HappyPath_EmitsExpectedEventSequence", file: "test/integration/mvp/s2_test.go"},
	}

	cases := []struct {
		name    string
		claimed map[string]bool
		want    []string
	}{
		{
			name: "exact-name claim tracks one, leaves the rest untracked",
			claimed: map[string]bool{
				"TestIntegrationMVP_Scenario1_CreateChat_HappyPath": true,
			},
			want: []string{
				"TestCollabKnowledge_Scenario1_MultiUserProjects",
				"TestCredentialAccessIsolation_Scenario1_SubjectKeyedRetrievalAndStamp",
				"TestIntegrationMVP_Scenario2_HappyPath_EmitsExpectedEventSequence",
			},
		},
		{
			name: "a family-stem claim tracks every member of the family",
			claimed: map[string]bool{
				"TestIntegrationMVP_Scenario1_": true,
				"TestIntegrationMVP_Scenario2_": true,
			},
			want: []string{
				"TestCollabKnowledge_Scenario1_MultiUserProjects",
				"TestCredentialAccessIsolation_Scenario1_SubjectKeyedRetrievalAndStamp",
			},
		},
		{
			name:    "no claims means every scenario test is untracked",
			claimed: map[string]bool{},
			want: []string{
				"TestCollabKnowledge_Scenario1_MultiUserProjects",
				"TestCredentialAccessIsolation_Scenario1_SubjectKeyedRetrievalAndStamp",
				"TestIntegrationMVP_Scenario1_CreateChat_HappyPath",
				"TestIntegrationMVP_Scenario2_HappyPath_EmitsExpectedEventSequence",
			},
		},
		{
			name: "every scenario test claimed leaves none untracked",
			claimed: map[string]bool{
				"TestIntegrationMVP_Scenario1_CreateChat_HappyPath":                     true,
				"TestIntegrationMVP_Scenario2_HappyPath_EmitsExpectedEventSequence":     true,
				"TestCollabKnowledge_Scenario1_MultiUserProjects":                       true,
				"TestCredentialAccessIsolation_Scenario1_SubjectKeyedRetrievalAndStamp": true,
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := untrackedScenarioTests(scenarios, tc.claimed)
			var names []string
			for _, s := range got {
				names = append(names, s.name)
			}
			assert.Equal(t, tc.want, names, "untracked scenario test names")
		})
	}
}

// TestADR_0065_ReverseOrphanScopesToScenarioTests pins the scope rule that keeps
// the orphan check low-noise: an ADR- or principle-tracing test is NOT a
// scenario test, so it is never collected as an orphan candidate. Only
// Test<Plan>_Scenario<N>_* names match.
func TestADR_0065_ReverseOrphanScopesToScenarioTests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"TestIntegrationMVP_Scenario1_CreateChat_HappyPath", true},
		{"TestCollabKnowledge_Scenario3_CrossChatSearch", true},
		// These trace to a decision directly; they must not read as scenario
		// orphans, or the check would flag hundreds of legitimate tests.
		{"TestADR_0019_CredentialRetrievableOnlyByStoringIdentity", false},
		{"TestPrinciple4_TokenBoundary", false},
		{"TestInvariant_LiveEqReplay", false},
		{"TestSearch_FooBar", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, scenarioTestNameRe.MatchString(tc.name))
		})
	}
}

// TestADR_0065_DraftStalenessReport pins the staleness half of the reverse
// checks from ADR-0065: a draft plan whose every verify: test
// already resolves is flagged as a candidate to flip to landed, and a draft plan
// with at least one still-missing verify: test is not. A draft plan with no
// test-verified criterion is not flagged either, since there is no landed test
// to infer staleness from.
func TestADR_0065_DraftStalenessReport(t *testing.T) {
	t.Parallel()

	index := map[string]string{
		"TestThing_Exists":   "x_test.go",
		"TestThing_AlsoHere": "y_test.go",
	}

	cases := []struct {
		name      string
		acs       []acEntry
		wantStale bool
		wantTACs  int
	}{
		{
			name: "all verify tests resolve — flagged stale",
			acs: []acEntry{
				{id: "AC1.1", verify: "`TestThing_Exists`", hasVerify: true, body: "AC1.1"},
				{id: "AC1.2", verify: "`TestThing_AlsoHere`", hasVerify: true, body: "AC1.2"},
			},
			wantStale: true,
			wantTACs:  2,
		},
		{
			name: "one verify test still missing — not flagged",
			acs: []acEntry{
				{id: "AC1.1", verify: "`TestThing_Exists`", hasVerify: true, body: "AC1.1"},
				{id: "AC1.2", verify: "`TestThing_Missing`", hasVerify: true, body: "AC1.2"},
			},
			wantStale: false,
		},
		{
			name: "method-only criteria — no landed test to infer from, not flagged",
			acs: []acEntry{
				{id: "AC1.1", verify: "none — reserved", hasVerify: true, body: "AC1.1"},
				{id: "AC1.2", verify: "inspection — by construction", hasVerify: true, body: "AC1.2"},
			},
			wantStale: false,
		},
		{
			name: "a method AC alongside a resolved test AC is still flagged",
			acs: []acEntry{
				{id: "AC1.1", verify: "`TestThing_Exists`", hasVerify: true, body: "AC1.1"},
				{id: "AC1.2", verify: "none — reserved", hasVerify: true, body: "AC1.2"},
			},
			wantStale: true,
			wantTACs:  1,
		},
		{
			name: "an AC with no verify field — not flagged",
			acs: []acEntry{
				{id: "AC1.1", verify: "", hasVerify: false, body: "AC1.1"},
			},
			wantStale: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sp, ok := draftStaleness("plan.md", tc.acs, index)
			assert.Equal(t, tc.wantStale, ok, "staleness decision")
			if tc.wantStale {
				assert.Equal(t, tc.wantTACs, sp.testACs, "test-verified AC count")
			}
		})
	}
}
