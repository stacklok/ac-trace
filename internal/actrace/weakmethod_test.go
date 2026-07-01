// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: LicenseRef-Stacklok-Proprietary

package actrace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestADR_0077_UserFacingRejectsWeakMethods pins L3: a user-facing AC may not
// be proven by demonstration / scenario / manual. The same method stays valid
// on a backend-foundation AC, and none / inspection stay valid everywhere.
func TestADR_0077_UserFacingRejectsWeakMethods(t *testing.T) { //nolint:paralleltest // captureOutput swaps os.Stdout globally
	fe, index := journeyFixture(t)
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "user-facing AC proven by demonstration fails",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e-realfd:chat.realfd\n" +
				"- AC1.2: also holds.\n  - verify: demonstration — hand-checked in staging\n",
			want: 1,
		},
		{
			name: "backend-foundation AC proven by demonstration passes",
			body: "**Surface:** backend-foundation\n" +
				"- AC1.1: wiring holds.\n  - verify: demonstration — the gate is the worked example\n",
			want: 0,
		},
		{
			name: "user-facing AC proven by inspection is fine",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e-realfd:chat.realfd\n" +
				"- AC1.2: closed by construction.\n  - verify: inspection — no producer path exists\n",
			want: 0,
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

// TestADR_0077_GrandfatherOptOutRequiresIssue pins the opt-out rule: a
// pre-existing user-facing scenario with no journey proof is grandfathered only
// when its journey-ok: opt-out cites a tracked issue. An opt-out with no issue
// reference is a hard failure.
func TestADR_0077_GrandfatherOptOutRequiresIssue(t *testing.T) { //nolint:paralleltest // captureOutput swaps os.Stdout globally
	fe, index := journeyFixture(t)
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "opt-out citing an issue number grandfathers the scenario",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e:mock; journey-ok: real-FD migration tracked in #692\n",
			want: 0,
		},
		{
			name: "opt-out citing an issues URL grandfathers the scenario",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e:mock; journey-ok: see https://github.com/stacklok/atrium/issues/692\n",
			want: 0,
		},
		{
			name: "opt-out with no issue reference fails",
			body: "**Surface:** user-facing\n" +
				"- AC1.1: renders.\n  - verify: ui-e2e:mock; journey-ok: migrate later\n",
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
