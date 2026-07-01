# ac-trace

Go CLI tool that gates acceptance-criteria traceability for Stacklok Go
moduliths (Airlock, Atrium). Every numbered AC in a
`docs/acceptance/<plan>.md` carries a `verify:` sub-line naming its
proof; this tool checks those proofs resolve in the tree.

## Commands

- `go build ./...` — build the library and binary
- `go test ./...` — run the test suite (all tests are unit, no external deps)
- `go run ./cmd/actrace` — run the tool against the cwd's tree
- `go run ./cmd/actrace --strict` — gate (exit 1 on failures)
- `go run ./cmd/actrace --plan foo` — check one plan
- `go vet ./...` — vet

## Architecture

- `cmd/actrace/main.go` — binary entrypoint. 18 lines: calls
  `actrace.Run(os.Args[1:])`, `os.Exit` with the return code.
- `internal/actrace/` — the library. All logic lives here.
  - `actrace.go` — plan parser, status lifecycle, forward gate, backward
    gate, `Run()` entrypoint (returns exit code int).
  - `config.go` — the optional `.actrace.yml` loader (opt-in features).
  - `resolver.go` — custom verify-method prefix→command hook (e.g. `edge:`).
  - `journey.go` — the ADR-0077 `**Surface:**` tag + journey-proof gate.
  - `reverse.go` — orphan gate, staleness report.
  - `report.go` — result model, `--json` renderer.
  - `matrix.go` — committed traceability matrix renderer.

The binary is a thin wrapper. `Run(args []string) int` parses flags and
returns an exit code (0 success, 1 gate failure under `--strict`, 2
usage/internal error). Never call `os.Exit` from the library.

## Conventions

- `package actrace` is the library; `package main` is only in
  `cmd/actrace/`. Tests live in `internal/actrace/` as `package actrace`.
- The tool runs against the **cwd** of the caller, not a flag. It globs
  `docs/acceptance/*.md`, `docs/adr/NNNN-*.md`, and
  `docs/design/principles.md` relative to `.`.
- Front-end proof vocabulary (`ui:`, `ui-e2e:`, `ui-unit:`,
  `ui-e2e-realfd:`) is Atrium-specific. In a repo with no `ui/` dir the
  spec index is empty and all `ui:` checks are no-ops. Do not remove this
  logic — Atrium depends on it.
- The ADR-0077 journey-integrity gate (`config.go`, `resolver.go`,
  `journey.go`) is **opt-in**: it fires only for a repo that ships an
  `.actrace.yml`. Absent config → zero `Config` → every new check off, so
  a repo like Airlock is unaffected. `journey_integrity: true` enables the
  `**Surface:**` tag + journey-proof gate; `resolvers:` maps a
  custom `verify:` prefix (e.g. `edge:`) to a command. The journey-proof
  location/substance conventions (`ui/tests/e2e/*.realfd.spec.ts`,
  `test/e2e/`, `synthetic`, `idpfake`, `waitForResponse`) are Atrium-shaped
  like the `ui:` vocabulary and no-op where those paths don't exist.

## Things that will bite you

- **The backward gate inlines adrstatus.** The original Atrium code did
  `exec.Command("go", "run", "./internal/tools/adrstatus")`. That
  dependency is inlined here as `loadADRStatusByNumber` + `classifyStatus`
  in `actrace.go`. If you change ADR status classification logic, change
  it in one place — there is no separate adrstatus tool in this repo.
- **`classifyStatus` is copied from Atrium's adrstatus.** If Atrium's
  `classifyStatus` changes, this copy drifts. Check Atrium's
  `internal/tools/adrstatus/main.go` before changing status
  classification.
- **`Run()` returns an exit code, it does not call `os.Exit`.** Only
  `cmd/actrace/main.go` calls `os.Exit`. Library code must stay
  exit-clean for testability.
- **Tests assume the old Atrium `package main` layout.** They were
  ported verbatim and now live in `package actrace`. If you rename a
  function the tests call, update the test too — there is no separate
  test package.

## Verification

- After any change: `go build ./... && go test ./... && go vet ./...`
- Against a real repo: `cd /path/to/repo && actrace` (or
  `go run github.com/stacklok/ac-trace/cmd/actrace` from within the
  module)
