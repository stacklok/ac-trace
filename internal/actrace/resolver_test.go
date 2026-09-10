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

// TestADR_0077_EdgeVerifyRejectedWithoutResolver pins the no-silent-no-op rule:
// an `edge:` (custom-prefix) verify token in a repo that has configured no
// resolver for that prefix is a hard failure, not a pass. Before this gate an
// unrecognised token resolved to zero test citations and slipped through green.
func TestADR_0077_EdgeVerifyRejectedWithoutResolver(t *testing.T) { //nolint:paralleltest // checkResolverRefs prints to os.Stdout; captureOutput swaps it globally
	e := acEntry{id: "AC1.1", verify: "edge:agentloop->files.GetFile", hasVerify: true}

	var failures int
	out := captureOutput(func() { failures = checkResolverRefs(e, Config{}) })

	assert.Equal(t, 1, failures, "an edge: token with no configured resolver must fail")
	assert.Contains(t, out, "no resolver configured for prefix \"edge:\"")
}

// TestADR_0077_ResolverTokenPassedAsArgv pins the injection-safety rule
// (CWE-78 / CWE-88): ac-trace passes the raw verify token to the resolver as a
// single argv element and never through a shell. A token carrying shell
// metacharacters must reach the resolver verbatim as $1, and must not be
// interpreted — no side effect fires.
func TestADR_0077_ResolverTokenPassedAsArgv(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	captured := filepath.Join(tmp, "captured")
	injected := filepath.Join(tmp, "INJECTED")
	script := filepath.Join(tmp, "resolver.sh")
	// The resolver records its first argument verbatim and exits 0.
	require.NoError(t, os.WriteFile(script,
		[]byte("#!/bin/sh\nprintf '%s' \"$1\" > "+captured+"\n"), 0o755)) //nolint:gosec // test fixture

	// A token that WOULD run `touch INJECTED` if it ever hit a shell.
	token := "edge:agentloop->files.GetFile`touch " + injected + "`"

	satisfied, err := runResolver([]string{"sh", script}, token)
	require.NoError(t, err)
	assert.True(t, satisfied, "resolver exited 0, so the proof holds")

	got, err := os.ReadFile(captured)
	require.NoError(t, err)
	assert.Equal(t, token, string(got), "the token must arrive as one discrete argv element, verbatim")

	_, statErr := os.Stat(injected)
	assert.True(t, os.IsNotExist(statErr), "no shell interpretation: the injection side effect must not fire")
}

// TestResolverTokens_FiltersBuiltinsAndURLs checks token extraction: a
// custom-prefix token is picked up, a built-in ui* front-end reference is left
// to checkUIRefs, a Go test name is not a resolver token, and a URL is skipped.
func TestResolverTokens_FiltersBuiltinsAndURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		verify string
		want   []string
	}{
		{"edge token", "edge:agentloop->files.GetFile", []string{"edge:agentloop->files.GetFile"}},
		{"edge alongside a go test", "TestFoo_Bar, edge:a->b.C", []string{"edge:a->b.C"}},
		{"builtin fe prefixes ignored", "ui-e2e-realfd:chat, ui-unit:bubble", nil},
		{"go test only", "TestADR_0077_Foo", nil},
		{"url ignored", "demonstration — see https://example.com/x", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, resolverTokens(tc.verify))
		})
	}
}

// TestRunResolver_ExitCodeMapsToSatisfied pins the exit-code contract: exit 0
// means the proof holds, a clean non-zero exit means it does not (no error),
// and an unrunnable command is an error the gate surfaces.
func TestRunResolver_ExitCodeMapsToSatisfied(t *testing.T) {
	t.Parallel()
	ok, err := runResolver([]string{"true"}, "edge:a->b.C")
	require.NoError(t, err)
	assert.True(t, ok)

	notOk, err := runResolver([]string{"false"}, "edge:a->b.C")
	require.NoError(t, err, "a clean non-zero exit is a 'no', not an error")
	assert.False(t, notOk)

	_, err = runResolver([]string{filepath.Join(t.TempDir(), "does-not-exist")}, "edge:a->b.C")
	assert.Error(t, err, "an unrunnable resolver command is a surfaced error")
}
