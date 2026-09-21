# Plan: M4 Mirror — `colt mirror`

Provider-to-provider namespace mirror. One-way copy, not continuous sync.

## Dependencies

M2 (list/clone/release), M7 (local providers for target).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | `colt mirror <source> <target> [--namespace]` CLI | `internal/app/` | ✅ complete |
| 2 | List source repos, clone to temp, create target, push all refs | `internal/` | ✅ complete |
| 3 | `--replace` force-mirror refs onto an existing target | `internal/app/` | ✅ complete |
| 4 | Independent failure summary | `internal/app/` | ✅ complete |
| 5 | BDD scenarios | `features/workspace_reconciliation.feature` | ✅ fake-backed |

## Remaining gated coverage

Run the live provider/container lane only against an approved disposable Gitea or Forgejo deployment. The default suite never deploys or mutates a host service.

## Acceptance

- `MIRROR-001` through `MIRROR-005` all pass.
- Target remotes are credential-free.
- Source and target remain independent after mirroring.
