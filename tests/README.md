# Tests

Three layers, slowest last. Every Development test recipe delegates to
`scripts/development/test.sh <layer>` — run `just test-unit` (preferred) or
the script directly.

| Layer | Location | Command | Needs |
|---|---|---|---|
| unit | `internal/*/*_test.go` (colocated, Go-idiomatic) | `just test-unit` | nothing (no network) |
| integration | `integration/` (`-tags integration`) | `just test-integration` | container tool + registry access |
| features | `features/*.feature` driven by `tests/bdd/` | `just test-bdd` | native `git` on PATH |

Plus: `just test-bdd-blackbox` (real binary smoke-test, build tag `bdd`),
`just bdd-coverage` (regenerates `.local/bdd-coverage.md`).

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
  `just test-bdd`; `@integration` is reserved for selected live-backend
  workflows (none exist yet — they will run under `just test-integration`
  when the adapters land).

## `integration/` layout

Single build-tagged package (`integration_test.go`): container lifecycle
(pull → run → wait-ready → admin/token bootstrap → remove) plus one test per
backend behavior (version, token auth, repo create + HTTP clone/push
roundtrip verified with `ls-remote`). Backend images, ports, and credentials
are env-overridable; see the file header. When Gitea/Forgejo adapters land,
their contract tests plug into this harness.

## Adding coverage

- New unit behavior → `*_test.go` next to the code, runs in `just test-unit`.
- New backend behavior → `integration/integration_test.go` table entry.
- New scenario → `features/*.feature` with a requirement-ID tag, steps in
  `tests/bdd/steps/`, shared state in `tests/bdd/fixture/`.
