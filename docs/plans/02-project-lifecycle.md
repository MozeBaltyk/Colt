# Plan: M2 — Project Lifecycle

## Dependencies

M1 (provider resolution, credential retrieval).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | `colt list [--provider|--all]` | `cmd/colt/` | ✅ done |
| 2 | `colt clone <repo> [--provider]` | `cmd/colt/` | ✅ done |
| 3 | `colt release <version> [--provider]` | `cmd/colt/` | ✅ done |
| 4 | Transport preference (`CORE-GIT-009`) | `internal/` | ✅ done |
| 5 | Core lifecycle BDD scenarios | `features/project_lifecycle.feature` | ✅ done |
| 6 | Clone path-race confinement | `internal/`, `features/` | ✅ done |
| 7 | Hostile Git-config rejection | `internal/`, `features/` | planned |

## Delivered

- **List** — provider-aware list with `--all` independent-failure reporting.
- **Clone** — authoritative URL resolution, credential-free remote, helper configuration, local identity, SSH key delegation, and descriptor-backed destination confinement.
- **Release** — validated explicit tag push, provider release, and partial-failure reporting per `CORE-FAILURE-001`.
- **Tests** — active core lifecycle acceptance and focused native-Git isolation coverage.

Still planned: fail-closed rejection of hostile repository-local execution controls. Those scenarios remain tagged `@planned` rather than claiming active acceptance.

## Acceptance

- Active `LIFECYCLE-LIST-001`, `LIFECYCLE-CLONE-001` through `004` and `006`, `LIFECYCLE-RELEASE-001`, and shared `CORE-GIT-004`/`009` scenarios pass.
- `LIFECYCLE-CLONE-005` and `LIFECYCLE-SECURITY-005`/`006` have active confinement coverage.
- `LIFECYCLE-SECURITY-001` through `003` retain planned acceptance coverage.
