// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package actrace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// journeyFixture writes a tree of spec / test files and returns the front-end
// spec index and the Go-test-declaration index built from it. The paths in
// both indexes are absolute (the walk root is a temp dir), and the substance
// readers os.ReadFile those paths, so the fixture exercises the real
// file-reading substance checks.
func journeyFixture(t *testing.T) (feIndex, map[string]string) {
	t.Helper()
	files := map[string]string{
		// Browser journey proofs.
		"ui/tests/e2e/chat.realfd.spec.ts":    "test('x', async ({page}) => { await page.waitForResponse('**/v1/responses'); });",
		"ui/tests/e2e/domonly.realfd.spec.ts": "test('x', async ({page}) => { await expect(page.getByText('hi')).toBeVisible(); });",
		"ui/tests/e2e/mock.spec.ts":           "test('x', async ({page}) => {});",
		"ui/src/bubble.test.ts":               "test('x', () => {});",
		// Backend journey proof: real-cluster e2e.
		"test/e2e/files_test.go": "//go:build e2e\n\npackage e2e_test\nimport \"testing\"\nfunc TestE2E_Files(t *testing.T) {}\n",
		// Faked seams under test/e2e/.
		"test/e2e/demo_test.go": "//go:build e2e && synthetic\n\npackage e2e_test\nimport \"testing\"\nfunc TestE2E_Demo(t *testing.T) {}\n",
		"test/e2e/idp_test.go":  "//go:build e2e\n\npackage e2e_test\nimport (\n\t\"testing\"\n\t_ \"x/internal/testing/idpfake\"\n)\nfunc TestE2E_Idp(t *testing.T) {}\n",
		// A Go test outside test/e2e/.
		"internal/foo/foo_test.go": "package foo\nimport \"testing\"\nfunc TestFoo_Bar(t *testing.T) {}\n",
	}
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	index, fe, err := indexTestDecls(dir)
	require.NoError(t, err)
	return fe, index
}

// TestADR_0077_JourneyProofIsLocationGated pins what counts as a journey proof:
// a substantive real-FD spec or a real-cluster test/e2e Go test — and what does
// not: a mock e2e spec, a unit spec, or a Go test outside test/e2e/.
func TestADR_0077_JourneyProofIsLocationGated(t *testing.T) {
	t.Parallel()
	fe, index := journeyFixture(t)
	tests := []struct {
		name   string
		verify string
		want   bool
	}{
		{"substantive realfd spec", "ui-e2e-realfd:chat.realfd", true},
		{"real-cluster e2e go test", "TestE2E_Files", true},
		{"mock e2e spec is not a journey", "ui-e2e:mock", false},
		{"unit spec is not a journey", "ui-unit:bubble", false},
		{"go test outside test/e2e is not a journey", "TestFoo_Bar", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isJourneyProof(acEntry{id: "AC1.1", verify: tc.verify}, fe, index)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestADR_0077_JourneyProofRejectsFakedSeam pins the substance rule: a proof in
// the right location that fakes the seam is rejected — a DOM-only realfd spec, a
// synthetic-tagged test/e2e test, and an idpfake-importing test/e2e test.
func TestADR_0077_JourneyProofRejectsFakedSeam(t *testing.T) {
	t.Parallel()
	fe, index := journeyFixture(t)
	tests := []struct {
		name   string
		verify string
	}{
		{"DOM-only realfd spec (no waitForResponse)", "ui-e2e-realfd:domonly.realfd"},
		{"synthetic-tagged test/e2e", "TestE2E_Demo"},
		{"idpfake-importing test/e2e", "TestE2E_Idp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, isJourneyProof(acEntry{id: "AC1.1", verify: tc.verify}, fe, index),
				"a faked seam must not satisfy the journey-proof requirement")
		})
	}
}

// TestADR_0077_UserFacingScenarioRequiresJourneyProof pins the scenario gate: a
// user-facing scenario needs a journey-proof AC; a backend-foundation scenario
// does not; a missing / unrecognised / ambiguous / orphan tag is a hard
// failure.
func TestADR_0077_UserFacingScenarioRequiresJourneyProof(t *testing.T) { //nolint:paralleltest // checkJourneyIntegrity prints; captureOutput swaps os.Stdout globally
	fe, index := journeyFixture(t)
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "user-facing with a journey proof passes",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e-realfd:chat.realfd\n",
			want: 0,
		},
		{
			name: "user-facing with only a mock proof fails",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e:mock\n",
			want: 1,
		},
		{
			name: "backend-foundation needs no journey proof",
			body: "**Surface:** backend-foundation\n" +
				"- AC1.1: wiring holds.\n  - verify: `TestFoo_Bar`\n",
			want: 0,
		},
		{
			name: "missing tag fails",
			body: "- AC1.1: renders.\n  - verify: ui-e2e:mock\n",
			want: 1,
		},
		{
			name: "unrecognised tag value (the unedited menu) fails",
			body: "**Surface:** user-facing | backend-foundation\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e-realfd:chat.realfd\n",
			want: 1,
		},
		{
			name: "orphan tag with no following AC fails",
			body: "**Surface:** user-facing\n**Surface:** backend-foundation\n" +
				"- AC1.1: wiring.\n  - verify: `TestFoo_Bar`\n",
			want: 1,
		},
	}
	for _, tc := range tests { //nolint:paralleltest,tparallel // captureOutput redirects os.Stdout globally
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(tc.body, "\n")
			acs := parseACs(lines)
			var got int
			out := captureOutput(func() { got = checkJourneyIntegrity(lines, acs, fe, index) })
			assert.Equal(t, tc.want, got, "output:\n%s", out)
		})
	}
}
