# Plan: M1 — Authentication & Blank Initialization

Partially implemented; absent behavior is `@unimplemented`.

## Dependencies

None (foundation milestone).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Provider adapters (GitHub, GitLab, Gitea, Forgejo) | `internal/` | ⬜ partial |
| 2 | GitHub OAuth Device Flow | `cmd/colt/` | ⬜ partial |
| 3 | Native secure credential persistence | `internal/` | ⬜ partial |
| 4 | Plaintext fallback with explicit consent | `internal/` | ⬜ partial |
| 5 | `colt auth login/status/logout` | `cmd/colt/` | ⬜ partial |
| 6 | `colt init` (remote + `--local`) | `cmd/colt/` | ⬜ partial |
| 7 | Container-backed Gitea/Forgejo vertical tests | `features/` | ✅ done |
| 8 | Production native-backend persistence acceptance | — | ❌ @unimplemented |
| 9 | Provider-side revocation (`logout --revoke`) | — | ❌ @unimplemented |

## Left to do (order)

1. **Persistence backend** — secure OS store + explicit-consent plaintext fallback + `@unimplemented` acceptance.
2. **Provider-side revocation** — `logout --revoke` per provider adapter.
3. **Remaining provider adapters** — fill gaps for GitLab/Gitea/Forgejo if any adapter is partial.

## Acceptance

- `CORE-PROVIDER-001` through `CORE-PROVIDER-011` all pass.
- `CORE-CREDENTIAL-001` through `CORE-CREDENTIAL-008` all pass.
- Container-backed Gitea/Forgejo `@planned` scenarios green.
