# Plan: M7 — `colt run [gitea|forgejo]`

Deploy a permanent Gitea or Forgejo server via systemd + podman.

## Dependencies

None (host-level primitive). Must complete before M4 for local provider sync.

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Define run/deploy spec | `docs/specs/07-run-deploy.md` | ✅ done |
| 2 | Write Gherkin acceptance scenarios | `features/run_gitea_forgejo.feature` | ✅ done |
| 3 | Add M7 to roadmap | `docs/specs/90-roadmap.md` | ✅ done |
| 4 | Implement `colt run` CLI + unit generation | `cmd/colt/` + `internal/` | ⬜ left |
| 5 | Tests — unit + container-backed integration | `tests/` / `features/` | ⬜ left |
| 6 | Procedures / usage doc update | `docs/procedures/` | ⬜ left |

## Left to do (order)

1. **Implement** — CLI subcommand `run/status/stop/start/rm`, systemd unit templating, podman network/volume/container creation, env-file generation, root check.
2. **Test** — unit tests for config/validation; real container-backed BDD scenarios matching `features/run_gitea_forgejo.feature` (needs root + podman).
3. **Docs** — add `colt run` to usage/procedures if CLI surface stabilizes.

## Open

- Whether `colt run` should also register the instance as a provider alias (default: no — use `colt auth login` after deployment).
- SSH port override flag name.
- Custom env file path support.
