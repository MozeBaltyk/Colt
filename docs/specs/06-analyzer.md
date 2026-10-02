# Analyzer Specification

**Milestone 6 is implemented.**

``` text
repository -> analyzer -> inventory.yaml -> report/rendering
```

`colt analyze` builds a read-only, deterministic inventory of a repository and renders reports from it. The `inventory.yaml` representation is the canonical analysis result; every report is a view over that same inventory and is never a second source of truth.

``` text
colt analyze [path] [--format summary|json|yaml] [--output inventory.yaml]
```

| ID                   | Requirement                                                                                                                                                                                                                                                                                                                                                                    | Acceptance specification                          |
|:---------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-------------------------------------------------|
| `ANALYZER-COLLECT-001` | `colt analyze [path]` **MUST** inventory repository identity (name, absolute root, sanitized origin) and detected package ecosystems by reading only filesystem metadata. It **MUST NOT** execute repository content.                                                                                                                                                             | [`analyzer.feature`](../../features/analyzer.feature) |
| `ANALYZER-SCHEMA-001` | The canonical inventory **MUST** be schema-versioned and deterministic: ecosystems sorted by name, manifests de-duplicated and normalized to repository-relative forward slashes, and detection repeated on the same tree **MUST** yield identical output.                                                                                                                       | [`analyzer.feature`](../../features/analyzer.feature) |
| `ANALYZER-REPORT-001` | Reports **MUST** be derived from the canonical inventory. `--format` selects the view (`summary`, `json`, or `yaml`); `--output` writes the canonical `inventory.yaml`. Every view and the written file **MUST** agree on the same inventory.                                                                                                                                     | [`analyzer.feature`](../../features/analyzer.feature) |
| `ANALYZER-SAFETY-001` | Analysis **MUST** be read-only and confined beneath the repository root (no linked ancestors, no symlink traversal, no mutation). Output **MUST** be bounded and **MUST NOT** expose credentials, provider response bodies, or repository file contents.                                                                                                                        | [`analyzer.feature`](../../features/analyzer.feature) |

Ecosystem detection uses an allowlist of well-known manifest basenames (`go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`, `pom.xml`, `composer.json`, `Dockerfile`, …). Detection reads only directory entry names under `os.Root` confinement, bounded by a maximum entry count and depth; oversized trees fail deterministically rather than being silently truncated. Dependency-graph extraction, per-file contents, and deeper manifests remain deferred.

`inventory.yaml` has this shape:

``` yaml
schema_version: 1
repository:
  name: demo
  path: /abs/path/to/demo
  origin: https://example.com/ns/demo.git
ecosystems:
  - name: go
    manifests: [go.mod, go.sum]
```

`origin` is recorded only when a Git remote exists, and is redacted first: any embedded credentials, query strings, and fragments are removed. The analyzer never reads manifest contents, so no repository file contents can leak into an inventory or report.