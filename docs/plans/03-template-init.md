# Plan: M3 — Parameterized Template Initialization

## Dependencies

M2 (provider resolution, clone primitives).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Template source resolution & version pinning | `internal/` | ❌ planned |
| 2 | `colt init <project> --template <name>[@<version>]` | `cmd/colt/` | ❌ planned |
| 3 | `colt template list/show` | `cmd/colt/` | ❌ planned |
| 4 | Data-only interpolation (no eval/hooks) | `internal/` | ❌ planned |
| 5 | BDD scenarios | `features/template_initialization.feature` | ❌ planned |

## Left to do (order)

1. **Template registry/metadata** — source resolution, version pins, `list`/`show`.
2. **Parameter validation** — declared params, `--set`, interactive prompts.
3. **Materialization** — deterministic data-only interpolation, fresh git history, no inherited remotes.
4. **Tests** — unit + BDD.

## Acceptance

- `TEMPLATE-SOURCE-001`, `TEMPLATE-PARAM-001`, `TEMPLATE-SAFETY-001`, `TEMPLATE-INIT-001` all pass.
