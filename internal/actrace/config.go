// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package actrace

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the optional `.actrace.yml` at a repo root. It is how consuming
// repos turn on opt-in journey-proof enforcement; repos without the config are
// unaffected. A missing file yields the zero Config, which disables every
// feature — so the tool behaves exactly as it did before this file existed.
type Config struct {
	// Resolvers maps a verify-method prefix (including its colon, e.g.
	// "edge:") to an external command that decides whether a token with that
	// prefix holds. ac-trace invokes the command with the raw verify token as
	// a single, final argv element — no shell — and reads the exit code: 0
	// means satisfied, non-zero means the proof does not hold. This keeps
	// ac-trace domain-agnostic: the consuming repo owns what "edge:" means.
	Resolvers map[string]ResolverConfig `yaml:"resolvers"`

	// JourneyIntegrity enables the ADR-0077 gate on a landed plan: the
	// `**Surface:**` scenario tag, the journey-proof requirement on a
	// user-facing scenario, and the rejection of weak proof methods for a
	// user-facing AC. Off by default so a repo that has not adopted the
	// convention is unaffected.
	JourneyIntegrity bool `yaml:"journey_integrity"`
}

// ResolverConfig is one verify-method-prefix resolver: the argv of the command
// ac-trace runs. ac-trace appends the raw verify token as one additional argv
// element, so the token reaches the command as inert data, never through a
// shell.
type ResolverConfig struct {
	Command []string `yaml:"command"`
}

// loadConfig reads `.actrace.yml` at root. A missing file is not an error — it
// returns the zero Config (every opt-in feature off). A present-but-malformed
// file is an error: a typo must not silently disable a gate the repo meant to
// turn on.
func loadConfig(root string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(root, ".actrace.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("reading .actrace.yml: %w", err)
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf(".actrace.yml: %w", err)
	}
	return c, nil
}
