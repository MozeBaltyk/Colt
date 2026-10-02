# Plan: M6 — Analyzer

Implemented.

## Dependencies

M5 (project health diagnostics).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Analysis collector | `internal/app/analyze.go` | ✅ done |
| 2 | `inventory.yaml` schema | `internal/app/analyze.go` | ✅ done |
| 3 | Report/rendering pipeline | `internal/app/analyze.go` | ✅ done |
| 4 | BDD scenarios | `features/analyzer.feature` | ✅ done |

## Acceptance

- Read-only, credential-safe, bounded output.
- Inventory is canonical; reports are derived views.
- Deterministic detection; no repository-content execution.