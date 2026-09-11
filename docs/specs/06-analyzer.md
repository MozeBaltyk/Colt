# Analyzer Intent

**Milestone 6 is planned and not implemented.** It is bounded future intent, not an active acceptance contract.

``` text
repository -> analyzer -> inventory.yaml -> report/rendering
```

`inventory.yaml` is the canonical analysis result. Reports and other renderings are derived views and never independent sources of truth.

Analysis is read-only and does not execute repository content. Inventories, reports, logs, and errors must not expose credentials, provider response bodies, or raw repository file contents. Collector behavior, inventory schema, report formats, and implementation details remain deferred until the analyzer becomes an active milestone.
