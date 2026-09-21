# Workspace Reconciliation Specification

**Milestone 4 is implemented.** Workspace operations use normal provider abstractions and the lifecycle list/clone primitives from [Milestone 2](02-project-lifecycle.md). Deterministic fake-backed acceptance covers provider and Git orchestration; live provider sync and mirroring remain gated and are not run on developer hosts.

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

Omitting `include` selects all repositories visible in that exact provider namespace. An explicit empty `include: []` selects none. The workspace root is `App.WorkDir`, or the current directory for the CLI, and each repository is placed at `<namespace>/<project>` beneath that root.

| ID                       | Requirement                                                                                                                                                                                                                                                                                                                                                                                  | Acceptance specification                                                              |
|:-------------------------|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-MANIFEST-001` | Colt **MUST** accept a single `workspace.repositories` list containing provider alias, namespace, and optional repository selections. Omitted `include` **MUST** mean all visible repositories in that selection. Unknown or executable YAML features **MUST** be rejected, and every derived repository identity and local path **MUST** be unique, deterministic, and confined beneath the workspace root. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

## Status And Sync

``` text
colt status
colt sync [--dry-run]
```

Top-level `colt status` is workspace status; `colt auth status` is provider configuration/connectivity status.

### colt status Nuances

| ID                     | Requirement                                                                                                                                                                                                                                                | Acceptance specification                                                              |
|:-----------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-STATUS-001` | `colt status` **MUST** read the manifest, local workspace, and remote metadata without mutation and report deterministic categories for desired repositories that are present, need cloning, are absent remotely, or have remote/configuration mismatches. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

### colt sync Nuances

| ID                       | Requirement                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Acceptance specification                                                              |
|:-------------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-SYNC-001`   | `colt sync` **MUST** be manifest-driven: clone only missing desired repositories, never clone extra ones. It **MUST** report existing, inconsistent, and remotely absent repositories. Independent failures **MUST** be summarized without discarding completed clones. `colt sync` **MUST** be idempotent — running again against an already-synced workspace is safe and reports no changes.                                                                                                                                               | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-DRYRUN-001` | `colt sync --dry-run` **MUST** compute and report the same reconciliation plan while performing no local, remote, or configuration mutation. Remote metadata reads **MAY** occur.                                                                                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-SAFETY-001` | Reconciliation **MUST** keep local operations within the workspace root and **MUST NOT** rewrite origins, delete or prune repositories, overwrite unrelated or non-empty paths, or reset dirty or diverged repositories.                                                                                                   | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

### colt sync vs colt clone vs colt mirror

| Command | Direction | Scope | What it does |
|:--------|:----------|:------|:-------------|
| `colt clone` | provider → local | single repo | Clone one repo from a provider, configure credential helper |
| `colt sync` | provider → local | manifest-driven | Clone missing repos from a declared workspace manifest; idempotent; never prunes or rewrites |
| `colt mirror` | provider → provider | namespace | Copy all repos from a source provider namespace to a target provider; one-way copy, not continuous sync |

### Mirror (M7 prerequisite)

`colt mirror` copies a namespace from one provider to another. It is useful with M7 local providers: mirror from GitHub/GitLab to your local Gitea/Forgejo instance. `--replace` force-pushes the source mirror's refs to an existing target repository; it does not delete/recreate the provider repository or modify unrelated provider state.

``` text
colt mirror <source-provider> <target-provider> [--namespace <namespace>]
```

| ID                    | Requirement                                                                                                                                                                                                                                                                                                           | Acceptance specification                                    |
|:----------------------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------|
| `MIRROR-001`        | `colt mirror` **MUST** list all visible repositories from the source provider namespace, clone each into a temporary directory, create the repository on the target provider, push all branches and tags, then clean up the temporary directory.                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-002`        | Independent repository failures **MUST** be summarized without discarding completed mirrors. A mirror of one repo must not abort the mirror of other repos.                                                                                                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-003`        | `colt mirror` **MUST NOT** overwrite an existing repository on the target provider unless `--replace` is supplied. Without `--replace`, an existing target repository MUST fail safely.                                                                                                                              | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-004`        | The mirror **MUST** be one-way: source and target remain independent after mirroring. `colt mirror` is not a continuous sync — subsequent changes on the source are not reflected on the target unless `colt mirror` is run again.                                                                                      | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-005`        | Remote URLs on the target **MUST** be credential-free. The target provider's remote URL **MUST** be used, not the source provider's URL.                                                                                                                                                                           | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

Status and sync inspect and install through an opened `os.Root`, without following workspace path links. Clone data is prepared in a private temporary directory and descriptor-relatively installed only after Git and credential-helper setup succeeds. Destructive pruning and bulk fetch/update remain deferred.
