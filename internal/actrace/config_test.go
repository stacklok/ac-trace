// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: LicenseRef-Stacklok-Proprietary

package actrace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfig_MissingFileIsZeroValue pins the opt-in contract: a repo that
// ships no .actrace.yml gets the zero Config (every feature off), not an error.
func TestLoadConfig_MissingFileIsZeroValue(t *testing.T) {
	t.Parallel()
	c, err := loadConfig(t.TempDir())
	require.NoError(t, err)
	assert.False(t, c.JourneyIntegrity)
	assert.Nil(t, c.Resolvers)
}

// TestLoadConfig_ParsesResolversAndFlag pins parsing of a present config: the
// journey_integrity flag and a prefix→command resolver mapping.
func TestLoadConfig_ParsesResolversAndFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := "" +
		"journey_integrity: true\n" +
		"resolvers:\n" +
		"  \"edge:\":\n" +
		"    command: [\"scripts/resolve-edge.sh\", \"--graph\", \".seamline/architecture.detail.json\"]\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".actrace.yml"), []byte(body), 0o644))

	c, err := loadConfig(dir)
	require.NoError(t, err)
	assert.True(t, c.JourneyIntegrity)
	require.Contains(t, c.Resolvers, "edge:")
	assert.Equal(t,
		[]string{"scripts/resolve-edge.sh", "--graph", ".seamline/architecture.detail.json"},
		c.Resolvers["edge:"].Command)
}

// TestLoadConfig_MalformedIsError pins that a present-but-broken config is a
// hard error — a typo must not silently disable a gate the repo turned on.
func TestLoadConfig_MalformedIsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".actrace.yml"), []byte("journey_integrity: [not a bool\n"), 0o644))
	_, err := loadConfig(dir)
	assert.Error(t, err)
}
