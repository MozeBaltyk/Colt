# Plan: M2 — Project Lifecycle

## Dependencies

M1 (provider resolution, credential retrieval).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | `colt list [--provider|--all]` | `cmd/colt/` | ✅ done |
| 2 | `colt clone <repo> [--provider]` | `cmd/colt/` | ✅ done |
| 3 | `colt release <version> [--provider]` | `cmd/colt/` | ✅ wired (was defined but not registered; fixed `client.Get` arg + `validatePushTarget` project comparison) |
| 4 | Transport preference (`CORE-GIT-009`) | `internal/` | ✅ done |
| 5 | BDD scenarios | `features/project_lifecycle.feature` | ✅ done |

## Left to do (order)

1. **List** — provider-aware list, `--all` independent-failure reporting.
2. **Clone** — authoritative URL resolution, credential-free remote, helper config, SSH key delegation.
3. **Release** — tag push + provider release, partial failure per `CORE-FAILURE-001`.
4. **Tests** — unit + container-backed integration.

## Acceptance

- `LIFECYCLE-LIST-001`, `LIFECYCLE-CLONE-001`, `LIFECYCLE-RELEASE-001`, `LIFECYCLE-TRANSPORT-001` all pass.
