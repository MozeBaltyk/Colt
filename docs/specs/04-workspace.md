# Workspace Reconciliation Specification

**Milestone 4 is planned and not implemented.** Its scenarios describe intended acceptance behavior, not current command or release availability. Workspace operations use normal provider abstractions and the lifecycle list/clone primitives from [Milestone 2](02-project-lifecycle.md).

## Manifest

The workspace is declared by a small data-only YAML document:

``` yaml
workspace:
  repositories:
    - provider: work
      namespace: example-org
      include: [api, web]
    - provider: personal
      namespace: example-user
```

Omitting `include` selects all repositories visible in that provider and namespace selection.

| ID                       | Planned requirement                                                                                                                                                                                                                                                                                                                                                                                          | Acceptance specification                                                              |
|:-------------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-MANIFEST-001` | Colt **MUST** accept a single `workspace.repositories` list containing provider alias, namespace, and optional repository selections. Omitted `include` **MUST** mean all visible repositories in that selection. Unknown or executable YAML features **MUST** be rejected, and every derived repository identity and local path **MUST** be unique, deterministic, and confined beneath the workspace root. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

## Status And Sync

``` text
colt status
colt sync [--dry-run]
```

Top-level `colt status` is workspace status; `colt auth status` is provider configuration/connectivity status.

| ID                     | Planned requirement                                                                                                                                                                                                                                        | Acceptance specification                                                              |
|:-----------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-STATUS-001` | `colt status` **MUST** read the manifest, local workspace, and remote metadata without mutation and report deterministic categories for desired repositories that are present, need cloning, are absent remotely, or have remote/configuration mismatches. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-SYNC-001`   | `colt sync` **MUST** clone missing desired repositories and report existing, inconsistent, and remotely absent repositories. Independent failures **MUST** be summarized without discarding completed clones.                                              | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-DRYRUN-001` | `colt sync --dry-run` **MUST** compute and report the same reconciliation plan while performing no local, remote, or configuration mutation. Remote metadata reads **MAY** occur.                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-SAFETY-001` | Reconciliation **MUST** keep local operations within the workspace root and **MUST NOT** rewrite origins, delete or prune repositories, overwrite unrelated or non-empty paths, or reset dirty or diverged repositories.                                   | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

Destructive pruning and bulk fetch/update are deferred. Implementation-level filesystem race hardening should be specified when this milestone becomes active.
