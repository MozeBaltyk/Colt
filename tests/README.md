# Tests

`just test` is the default gate. It runs Colt's unit and deterministic BDD
suites, including BDD subpackages, then the lightweight repository check.
Development test recipes delegate to `scripts/development/test.sh <layer>`.

| Layer | Location | Command | Needs |
|---|---|---|---|
| unit | `internal/*/*_test.go` (colocated, Go-idiomatic) | `just test-unit` | nothing (no network) |
| integration | `integration/` (`-tags integration`) | `just test-integration` | Podman or Docker, registry access, native Git |
| features | `features/*.feature` driven by `tests/bdd/` | `just test-bdd` | native `git` on PATH |

Plus: `just test-bdd-blackbox` (real binary smoke-test, build tag `bdd`),
`just bdd-coverage` (regenerates `.local/bdd-coverage.md`), and `go vet ./...`.

## `tests/bdd/` layout

```
tests/bdd/
├── godog_test.go      # suite only: TestBDD, Before/After hooks, tag filter
├── blackbox_test.go   # TestBlackbox (build tag: bdd), self-contained
├── feature_metadata_test.go  # tag hygiene + requirement index, no godog
├── fixture/           # shared world: scenario state, doubles, helpers
│   ├── world.go       # World, Reset, Run/RunArgs, LoginArgs, env handling
│   ├── fakes.go       # FakeGit, FakeClient, FakeCredentialStore
│   └── helpers.go     # StdProvider, GitOut, SplitCmd, snapshots (+ helpers_test.go)
└── steps/             # godog step definitions, one file per domain
    ├── init.go        # RegisterInitSteps (init + resolution scenarios)
    └── auth.go        # RegisterAuthSteps (auth + transport scenarios)
```

Rules:

- `godog_test.go` stays thin: wiring only, no steps, no fixtures.
- `fixture/` exports what steps need (`World`, `FakeGit`, …). New shared
  state goes here with `// NOTE`-style reset coverage in `World.Reset`, so
  scenario state cannot leak.
- `steps/` holds only step registrations plus tiny step-local helpers.
  Domain files (`init.go`, `auth.go`, …); add a file per domain, not per scenario.
- `steps/` and `fixture/` are regular (non-test) packages so the suite can
  import them; `go build ./...` and `go vet ./...` cover them like product code.
- Scenario status lives in feature tags, enforced by `TestBDDTagHygiene`:
  no tag = active and executed; `@planned` / `@unimplemented` = excluded from
  `just test-bdd`; `@integration` workflows run the real binary against live
  container backends under `just test-integration`.

## `integration/` layout

Single build-tagged package (`integration_test.go`): shared container lifecycle
(pull → run → wait-ready → admin/token bootstrap → remove), backend probes, and
the tagged Godog Gitea and Forgejo workflows. Each workflow creates an ephemeral CA,
runs the provider with trusted HTTPS, builds and invokes Colt, then verifies the pushed
branch through Git using Colt's repository-local credential helper. CI runs both
vertical scenarios with Docker; the explicit local recipe also runs the optional
Gitea/Forgejo backend probes.

## Adding coverage

- New unit behavior → `*_test.go` next to the code, runs in `just test-unit`.
- New backend behavior → `integration/integration_test.go` table entry.
- New scenario → `features/*.feature` with a requirement-ID tag, steps in
  `tests/bdd/steps/`, shared state in `tests/bdd/fixture/`.
