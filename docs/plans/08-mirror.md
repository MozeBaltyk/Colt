# Plan: M4 Mirror — `colt mirror`

Provider-to-provider namespace mirror. One-way copy, not continuous sync.

## Dependencies

M2 (list/clone/release), M7 (local providers for target).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | `colt mirror <source> <target> [--namespace]` CLI | `cmd/colt/` | ❌ planned |
| 2 | List source repos, clone to temp, create on target, push | `internal/` | ❌ planned |
| 3 | `--replace` flag for overwriting existing target repos | `cmd/colt/` | ❌ planned |
| 4 | Independent failure summary | `internal/` | ❌ planned |
| 5 | BDD scenarios | `features/workspace_reconciliation.feature` | ❌ planned |

## Left to do (order)

1. **List** — source provider namespace listing.
2. **Clone + push** — temp dir per repo, create on target, push all branches/tags.
3. **Cleanup** — remove temp dirs, report partial failures.
4. **Tests** — unit + BDD.

## Acceptance

- `MIRROR-001` through `MIRROR-005` all pass.
- Target remotes are credential-free.
- Source and target remain independent after mirroring.
