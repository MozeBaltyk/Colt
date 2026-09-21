# Plan: M5 — Project Health

## Dependencies

M4 (workspace manifest, provider resolution).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | `colt check [--all]` | `cmd/colt/` | ✅ implemented |
| 2 | Health policy schema & validation | `internal/` | ✅ implemented |
| 3 | Diagnostic checks (identity, origin, remote, branch, cleanliness, files, visibility) | `internal/` | ✅ implemented |
| 4 | BDD scenarios | `features/project_health.feature` | ✅ fake-backed |

## Deferred

1. Auto-remediation.
2. Opt-in live-provider acceptance; current BDD coverage is fake-backed.

## Acceptance

- `HEALTH-CHECK-001`, `HEALTH-POLICY-001`, `HEALTH-OUTPUT-001`, `HEALTH-SAFETY-001` all pass.
