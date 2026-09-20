# Plan: M3 — Parameterized Template Initialization

## Dependencies

M2 (provider resolution, clone primitives).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Template source resolution & version pinning | `internal/` | ✅ complete |
| 2 | `colt init <project> --template <name>[@<version>]` | `cmd/colt/` | ✅ complete |
| 3 | `colt template list/show` | `cmd/colt/` | ✅ complete |
| 4 | Data-only interpolation (no eval/hooks) | `internal/` | ✅ complete |
| 5 | BDD scenarios | `features/template_initialization.feature` | ✅ complete |

## Implementation

Complete. Local directory versions use explicit canonical SHA-256 pins, strict declared parameters, bounded preflight materialization plans, and the shared local/remote initialization flow.

## Acceptance

- `TEMPLATE-SOURCE-001`, `TEMPLATE-PARAM-001`, `TEMPLATE-SAFETY-001`, `TEMPLATE-INIT-001` all pass.
