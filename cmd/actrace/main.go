// SPDX-FileCopyrightText: Copyright 2026 Stacklok, Inc.
// SPDX-License-Identifier: LicenseRef-Stacklok-Proprietary

// Command actrace reports acceptance-criteria → test coverage for the plans
// under docs/acceptance/: every test a plan names as the proof of a criterion
// should exist in the tree. It is the report-only stage of the traceability
// work.
package main

import (
	"os"

	"github.com/stacklok/ac-trace/internal/actrace"
)

func main() {
	os.Exit(actrace.Run(os.Args[1:]))
}
