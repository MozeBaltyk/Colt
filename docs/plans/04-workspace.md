# Plan: M4 — Declarative Workspace Reconciliation

## Dependencies

M2 (list/clone primitives), M7 (local Gitea/Forgejo for `Colt mirror` / `colt sync` with self-hosted providers).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Workspace manifest schema & validation | `internal/` | ❌ planned |
| 2 | `colt status` (read-only reconciliation) | `cmd/colt/` | ❌ planned |
| 3 | `colt sync` (manifest-driven, idempotent) | `cmd/colt/` | ❌ planned |
| 4 | `colt sync --dry-run` | `cmd/colt/` | ❌ planned |
| 5 | `colt mirror` (provider-to-provider) | `cmd/colt/` | ❌ planned |
| 6 | BDD scenarios | `features/workspace_reconciliation.feature` | ❌ planned |

## Left to do (order)

1. **Manifest** — YAML parse, unique/deterministic identities, path confinement.
2. **Status** — read-only desired-vs-actual categorization.
3. **Sync** — clone missing, report inconsistent/remote-absent, independent failure summary, idempotent.
4. **Mirror** — list source repos, clone to temp, create on target, push all branches/tags, cleanup.
5. **Dry-run** — same plan, no mutation.
6. **Tests** — unit + BDD (needs M7 local providers for full coverage).

## Acceptance

- `WORKSPACE-MANIFEST-001`, `WORKSPACE-STATUS-001`, `WORKSPACE-SYNC-001`, `WORKSPACE-DRYRUN-001`, `WORKSPACE-SAFETY-001`, `MIRROR-001` through `MIRROR-005` all pass.
