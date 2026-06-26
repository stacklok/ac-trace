# ac-trace

Acceptance-criteria traceability gate for Stacklok Go moduliths. Every
numbered acceptance criterion in a `docs/acceptance/<plan>.md` carries a
`verify:` sub-line naming its proof. This tool checks that every named
proof resolves in the tree.

Extracted from Atrium's `internal/tools/actrace` (ADR-0065) so both repos
share one gate implementation instead of a copied convention with no
enforcement.

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

Because ac-trace is a private repo, share it org-wide once: in this repo's
**Settings → Actions → General → Access**, choose **"Accessible from
repositories in the organization"**. Then in any stacklok repo:

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

(Requires `actrace` on `PATH`, or `go run github.com/stacklok/ac-trace/cmd/actrace@<ref> --strict`
after the standard private-module auth step.)

Run `actrace --matrix` and commit the generated
`docs/acceptance/traceability.md`. Gate it for freshness with your
`verify-gen` task.

## Project layout

- `cmd/actrace/main.go` — binary entrypoint (18 lines; calls
  `actrace.Run`, exits with its return code).
- `internal/actrace/actrace.go` — plan parser, status lifecycle, forward
  gate, backward gate, `Run()` entrypoint.
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
(e.g. `go 1.26`, not a patch). Lint config is `.golangci.yml`, mirroring
Atrium's linter set; `gosec` is suppressed for `internal/actrace/` because
the tool reads the repo tree by discovered path (G304) by design.

## Provenance

Lifted from Atrium's `internal/tools/actrace` plus the `classifyStatus`
logic from `internal/tools/adrstatus`, with the external process
dependency inlined. The design decisions live in Atrium's
[ADR-0065](https://github.com/stacklok/atrium/blob/main/docs/adr/0065-acceptance-criteria-carry-verify-field-gated-by-actrace.md).

## License

Copyright 2026 Stacklok, Inc. LicenseRef-Stacklok-Proprietary.
