# Plan: M6 — Analyzer

Bounded future intent, not an active acceptance contract.

## Dependencies

M5 (project health diagnostics).

## Status

| # | Action | Where | Status |
|:--|:---|:---|:--:|
| 1 | Analysis collector | `internal/` | ❌ future |
| 2 | `inventory.yaml` schema | `internal/` | ❌ future |
| 3 | Report/rendering pipeline | `cmd/colt/` | ❌ future |
| 4 | BDD scenarios | `features/analyzer.feature` | ❌ future |

## Left to do (order)

1. **Collector** — repository inventory, read-only, no execution.
2. **Schema** — canonical `inventory.yaml`.
3. **Rendering** — derived reports from inventory, never independent sources.
4. **Security** — no credentials, provider response bodies, or raw file contents in output.

## Acceptance

- Read-only, credential-safe, bounded output.
- Inventory is canonical; reports are derived views.
