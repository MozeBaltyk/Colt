# Plan: M4 — Declarative Workspace Reconciliation

## Dependencies

M2 (list/clone primitives), M7 (local Gitea/Forgejo for `Colt mirror` / `colt sync` with self-hosted providers).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Workspace manifest schema & validation | `internal/` | ✅ complete |
| 2 | `colt status` (read-only reconciliation) | `internal/app/` | ✅ complete |
| 3 | `colt sync` (manifest-driven, idempotent) | `internal/app/` | ✅ complete |
| 4 | `colt sync --dry-run` | `internal/app/` | ✅ complete |
| 5 | `colt mirror` (provider-to-provider) | `internal/app/` | ✅ complete |
| 6 | BDD scenarios | `features/workspace_reconciliation.feature` | ✅ fake-backed |

## Remaining gated coverage

Live Gitea/Forgejo mirror acceptance is intentionally not executed without an approved disposable provider deployment. Unit and BDD fakes cover orchestration, all-ref pushes, replacement semantics, cleanup, URL validation, redaction, and independent failures.

## Acceptance

- `WORKSPACE-MANIFEST-001`, `WORKSPACE-STATUS-001`, `WORKSPACE-SYNC-001`, `WORKSPACE-DRYRUN-001`, `WORKSPACE-SAFETY-001`, `MIRROR-001` through `MIRROR-005` all pass.
