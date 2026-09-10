# ac-trace

Acceptance-criteria traceability gate for Go moduliths. Every
numbered acceptance criterion in a `docs/acceptance/<plan>.md` carries a
`verify:` sub-line naming its proof. This tool checks that every named
proof resolves in the tree.

## Install

```
go install github.com/stacklok/ac-trace/cmd/actrace@latest
```

Requires Go 1.26+.

## Quick start

Run from the repo root of the project you want to check:

```
actrace
```

Output for a landed plan with all proofs resolving:

```
== docs/acceptance/phase-1-core-mcp-flow.md ==  [landed]
  44 ACs · 0 failure(s)

== backward gate: TestADR_NNNN_* pins ==
  0 TestADR_* pin(s) over a missing or orphaned-retired ADR

== reverse: scenario tests no landed plan tracks ==
  0 scenario test(s) scanned · 0 excused · 0 untracked

every landed plan passes its forward checks; every TestADR_* pins a live ADR
```

Gate in CI with `--strict` (exits 1 on any failure):

```
actrace --strict
```

## Commands

| Command | What it does |
|---|---|
| `actrace` | Report-only: print coverage, never exit non-zero |
| `actrace --strict` | Gate: exit 1 when a landed plan fails a forward check |
| `actrace --reverse` | Also print the orphan and staleness reports |
| `actrace --matrix` | Regenerate `docs/acceptance/traceability.md` (never gates) |
| `actrace --json` | Print coverage as indented JSON (automation interface) |
| `actrace --plan foo` | Check one plan (path or basename, with or without `.md`) |

## What it checks

**Forward gate** (plan → test). On a `landed` plan, every AC must carry a
`verify:` field, every named Go test must exist, and every bare-text
`ADR-NNNN` or `Principle N` citation must resolve to a real decision.

**Backward gate** (test → ADR). A `TestADR_NNNN_*` test cannot outlive the
ADR it defends. A test naming a missing or superseded ADR (with no
resolvable supersessor) fails.

**Orphan gate** (test → plan). A scenario test
(`Test<Plan>_Scenario<N>_*`) that no landed plan claims fails, unless it
carries an `//actrace:untracked-ok` comment on the line above its
declaration.

**Staleness report** (report-only). A draft plan whose `verify:` tests
have all landed is a candidate to flip to `landed`.

## The convention

Each numbered acceptance criterion carries a `verify:` sub-line:

```markdown
- AC1.1: behaviour holds.
  - verify: `TestThing_Exists`
- AC1.2: closed by construction.
  - verify: none — reserved with no V1 producer
```

Plans declare a lifecycle on the `**Status:**` line:

| Status | Gated? | Behavior |
|---|---|---|
| `draft` | No | Reported, never fails |
| `in-progress` | No | Reported, never fails |
| `landed` | Yes | All forward checks enforced |
| `superseded` | — | Skipped entirely |

Only `landed` plans are gated. A plan names its tests before they are
built, so enforcement waits until its implementation merges.

## Journey integrity (opt-in, ADR-0077)

An optional `.actrace.yml` at the repo root turns on extra checks. Absent,
the tool behaves exactly as above — a repo that ships no config is unaffected.

```yaml
# .actrace.yml
journey_integrity: true          # enable the Surface tag + journey-proof gate
resolvers:
  "edge:":                       # a custom verify-method prefix
    command: ["scripts/resolve-edge.sh"]
```

**Custom `verify:` methods (e.g. `edge:`).** A token like
`edge:agentloop->files.GetFile` is resolved by the command its prefix maps
to in `resolvers`. ac-trace runs that command with the raw token as one
final argv element — never through a shell — and reads the exit code: 0
means the proof holds, non-zero means it does not. A custom-prefix token
whose prefix has no configured resolver is a hard failure, never a silent
pass.

**The `**Surface:**` scenario tag.** With `journey_integrity: true`, each
scenario in a landed plan carries a `**Surface:** user-facing |
backend-foundation` field. A `user-facing` scenario must carry at least one
AC whose `verify:` cites a **journey proof**:

- a `ui-e2e-realfd:` Playwright spec (a `.realfd.spec.ts` under
  `ui/tests/e2e/`) that asserts on a real server response
  (`waitForResponse`), not on rendered DOM alone; or
- a Go test under `test/e2e/` that is a real-cluster run — `//go:build
  e2e`, not `synthetic`, and not importing `idpfake`.

A mock spec, a unit test, a `test/e2e/` test that fakes its seam, or a
`demonstration` / `scenario` / `manual` method cannot satisfy a
`user-facing` scenario. A missing, unrecognised, or ambiguous tag is a hard
failure.

**Grandfathering.** A pre-existing `user-facing` scenario with no journey
proof yet opts out with `journey-ok: <reason>` on an AC's verify line — but
only when the reason cites a tracked issue (`#123` or an issues URL), so the
debt is visible and attributed.

## Wiring into a repo

Add to your `Taskfile.yml`:

```yaml
  ac-trace:
    desc: Trace acceptance criteria to their verification tests
    cmds:
      - actrace

  ac-trace-strict:
    desc: Gate a landed plan's verify contract (CI)
    cmds:
      - actrace --strict

  ac-trace-matrix:
    desc: Regenerate the committed traceability matrix
    cmds:
      - actrace --matrix
```

## Use as a GitHub Action

This repo is also a composite action, so a consumer repo can gate on the
traceability check in one step — no Go setup, no `GOPRIVATE`, no
module-read token. The action builds the binary from its own source and
runs it against your checked-out tree.

Use it in any repo:

```yaml
      - uses: actions/checkout@<sha>   # the tree being checked
      - uses: stacklok/ac-trace@v1     # pin to a tag or SHA
        with:
          args: --strict               # default; gate on landed-plan failures
```

Inputs:

| Input | Default | Description |
|---|---|---|
| `args` | `--strict` | Arguments passed to `actrace`. Empty string for report-only; e.g. `--plan foo --strict`. |
| `working-directory` | `.` | Repo root of the project being checked. |

The action sets up Go from its own bundled `go.mod`, so the gate always
builds against the version it was tested with.

### Or call the binary directly

If you'd rather not use the action, add to CI:

```yaml
      - name: Acceptance-criteria traceability gate
        run: actrace --strict
```

(Requires `actrace` on `PATH`, or `go run github.com/stacklok/ac-trace/cmd/actrace@<ref> --strict`.)

Run `actrace --matrix` and commit the generated
`docs/acceptance/traceability.md`. Gate it for freshness with your
`verify-gen` task.

## Project layout

- `cmd/actrace/main.go` — binary entrypoint (18 lines; calls
  `actrace.Run`, exits with its return code).
- `internal/actrace/actrace.go` — plan parser, status lifecycle, forward
  gate, backward gate, `Run()` entrypoint.
- `internal/actrace/config.go` — the optional `.actrace.yml` loader.
- `internal/actrace/resolver.go` — custom verify-method prefix→command hook.
- `internal/actrace/journey.go` — the ADR-0077 Surface tag + journey-proof gate.
- `internal/actrace/reverse.go` — orphan gate, staleness report.
- `internal/actrace/report.go` — result model, `--json` renderer.
- `internal/actrace/matrix.go` — committed traceability matrix renderer.

## Development

```
go build ./...
go test ./...
golangci-lint run
```

CI (`.github/workflows/ci.yml`) runs lint + test + build on every push and
PR to `main`, and enforces that `go.mod` is pinned to a minor Go version
(e.g. `go 1.26`, not a patch). Lint config is `.golangci.yml`; `gosec` is
suppressed for `internal/actrace/` because the tool reads the repo tree by
discovered path (G304) by design.

## Provenance

ac-trace was originally developed as an in-repo tool and extracted into this
standalone module so multiple repos can share one gate implementation. The ADR
status-classification logic is inlined in `internal/actrace/actrace.go` (see
`classifyStatus`).

## License

Copyright 2026 Stacklok, Inc. Apache-2.0 — see LICENSE.
