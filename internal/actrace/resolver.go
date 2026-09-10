// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package actrace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// resolverTokenRe captures a custom verify-method token: a lowercase prefix
// then a colon then a value, e.g. `edge:agentloop->files.GetFile`. The value
// runs to the next space, comma, or semicolon. A Go test name (starts
// uppercase) and a bare method word (no colon) do not match. A URL is excluded
// separately (it contains "://"), and the built-in `ui*:` front-end prefixes
// are handled by checkUIRefs, so they are skipped here.
var resolverTokenRe = regexp.MustCompile(`\b[a-z][a-z0-9-]{1,20}:[^\s,;]+`)

// isBuiltinFEToken reports whether a token is one of the built-in front-end
// proof prefixes, which checkUIRefs already owns.
func isBuiltinFEToken(tok string) bool {
	for _, p := range []string{"ui-e2e-realfd:", "ui-e2e:", "ui-unit:", "ui:"} {
		if strings.HasPrefix(tok, p) {
			return true
		}
	}
	return false
}

// resolverTokens returns the custom-prefix tokens in a verify field that an
// external resolver should decide. It excludes Go test names, built-in
// front-end references, and URLs (a `://` token is a link, not a proof).
func resolverTokens(verify string) []string {
	var out []string
	for _, tok := range resolverTokenRe.FindAllString(verify, -1) {
		// Skip built-in front-end refs (checkUIRefs owns them), URLs, and
		// opt-out markers like `ui-unit-ok:` / `journey-ok:` (a prefix ending
		// in "-ok:"), which are declarations, not proofs to resolve.
		if isBuiltinFEToken(tok) || strings.Contains(tok, "://") || strings.HasSuffix(tokenPrefix(tok), "-ok:") {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// tokenPrefix returns the method prefix of a resolver token, including the
// colon: "edge:agentloop->files.GetFile" → "edge:".
func tokenPrefix(tok string) string {
	if i := strings.IndexByte(tok, ':'); i >= 0 {
		return tok[:i+1]
	}
	return tok
}

// runResolver invokes a configured resolver command with the raw verify token
// as a single, final argv element. It never runs the token through a shell, so
// an author-controlled token cannot inject a command (CWE-78 / CWE-88). A
// zero exit means the proof holds; a non-zero exit means it does not; any other
// failure (command not found, not executable) is returned as an error so the
// gate reports it rather than treating it as a silent "no".
func runResolver(command []string, token string) (satisfied bool, err error) {
	if len(command) == 0 {
		return false, errors.New("empty resolver command")
	}
	argv := append(append([]string{}, command[1:]...), token)
	// #nosec G204 -- command comes from the repo's own reviewed .actrace.yml,
	// and token is passed as one discrete argv element (no shell), so a
	// plan-authored token cannot inject a command.
	cmd := exec.Command(command[0], argv...)
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	if runErr == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return false, nil // clean non-zero exit: the proof does not hold
	}
	return false, fmt.Errorf("running resolver %q: %w", command[0], runErr)
}

// checkResolverRefs resolves every custom-prefix verify token in an AC through
// its configured resolver. A token whose prefix has no resolver in .actrace.yml
// is a hard failure — never a silent no-op — which is how an `edge:` proof is
// rejected in a repo that has not wired the resolver. Returns the failure count.
func checkResolverRefs(e acEntry, cfg Config) int {
	failures := 0
	for _, tok := range resolverTokens(e.verify) {
		prefix := tokenPrefix(tok)
		rc, ok := cfg.Resolvers[prefix]
		if !ok {
			failures++
			fmt.Printf("  ✗ %s  verify names %s — no resolver configured for prefix %q in .actrace.yml\n", e.id, tok, prefix)
			continue
		}
		satisfied, err := runResolver(rc.Command, tok)
		switch {
		case err != nil:
			failures++
			fmt.Printf("  ✗ %s  verify names %s — resolver error: %v\n", e.id, tok, err)
		case !satisfied:
			failures++
			fmt.Printf("  ✗ %s  verify names %s — resolver rejected it (proof does not hold)\n", e.id, tok)
		}
	}
	return failures
}
