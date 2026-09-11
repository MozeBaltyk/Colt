# Analyzer Intent

The analyzer is a future capability, not an active acceptance contract.

```text
repository -> analyzer -> inventory.yaml -> report/rendering
```

`inventory.yaml` is the canonical analysis result. Reports and other renderings
are derived views and never independent sources of truth.

Analysis must not modify the source repository or execute repository content.
Credentials, secret values, and secret payloads must not enter inventories,
reports, logs, or errors. Detailed collection, schema, and reporting behavior
will be specified when this milestone becomes active.
