# Plan: M5 — Project Health

## Dependencies

M4 (workspace manifest, provider resolution).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | `colt check [--all]` | `cmd/colt/` | ❌ planned |
| 2 | Health policy schema & validation | `internal/` | ❌ planned |
| 3 | Diagnostic checks (identity, origin, remote, branch, cleanliness, files, visibility) | `internal/` | ❌ planned |
| 4 | BDD scenarios | `features/project_health.feature` | ❌ planned |

## Left to do (order)

1. **Policy parser** — strict YAML, documented fields only.
2. **Check engine** — read-only diagnostics, bounded output, redaction.
3. **Exit codes** — 0 healthy, 1 policy drift, 2 operational error.
4. **Tests** — unit + BDD.

## Acceptance

- `HEALTH-CHECK-001`, `HEALTH-POLICY-001`, `HEALTH-OUTPUT-001`, `HEALTH-SAFETY-001` all pass.
