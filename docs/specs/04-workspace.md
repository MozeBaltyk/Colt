# Workspace Reconciliation Specification

**Milestone 4 is implemented.** Workspace operations use normal provider abstractions and the lifecycle list/clone primitives from [Milestone 2](02-project-lifecycle.md). Deterministic fake-backed acceptance covers provider and Git orchestration; live provider sync and mirroring remain gated and are not run on developer hosts.

## Manifest

Workspaces are declared by a small data-only YAML document. Names are unique map keys; every workspace has its own absolute local path and repository selections:

``` yaml
workspaces:
  services:
    path: /home/example/work/services
    repositories:
      - provider: work
        namespace: example-org
        include: [api, web]
  personal:
    path: /home/example/work/personal
    repositories:
      - provider: personal
        namespace: example-user
```

Omitting `include` selects all repositories visible in that exact provider namespace. An explicit empty `include: []` selects none. Repositories are placed directly at `<workspace.path>/<project>`. Workspace paths must be unique. The former singular `workspace:` form is invalid and MUST produce a migration error directing users to `workspaces.<name>`.

| ID                       | Requirement                                                                                                                                                                                                                                                                                                                                                                                  | Acceptance specification                                                              |
|:-------------------------|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-MANIFEST-001` | Colt **MUST** accept a bounded `workspaces` map whose safe unique names identify declarations containing a required clean absolute `path` and a `repositories` list. Omitted `include` **MUST** mean all visible repositories in that exact selection; an empty list means none. Workspace paths **MUST** be unique, and every derived repository identity and flat project path **MUST** be unique, deterministic, and confined beneath its workspace root. Unknown, singular `workspace:`, or executable YAML features **MUST** be rejected before provider or local access. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-SELECTION-001` | `colt status` and no-namespace `colt sync` **MUST** process every configured workspace in deterministic name order when `--workspace` is omitted. `--workspace <name>` **MUST** select exactly one configured workspace and reject unknown names. Output **MUST** identify the workspace on every result line. A failure in one workspace **MUST NOT** prevent independent workspaces from being inspected or reconciled. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

## Status And Sync

``` text
colt status [--workspace <name>]
colt sync [--workspace <name>] [--dry-run] [--fetch | --pull]
colt sync <namespace> --path <existing-directory> [--provider <alias>] [--workspace <name> --save] [--dry-run] [--fetch | --pull]
```

Top-level `colt status` is workspace status; `colt auth status` is provider configuration/connectivity status.

### colt status Nuances

| ID                     | Requirement                                                                                                                                                                                                                                                | Acceptance specification                                                              |
|:-----------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-STATUS-001` | `colt status` **MUST** read each selected manifest entry, local workspace root, and remote metadata without mutation and report workspace-qualified deterministic categories for desired repositories that are present, need cloning, absent remotely, or mismatched. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

### colt sync Nuances

| ID                       | Requirement                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Acceptance specification                                                              |
|:-------------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------|
| `WORKSPACE-SYNC-001`   | `colt sync` **MUST** be manifest-driven: clone only missing desired repositories, never clone extra ones. It **MUST** report existing, inconsistent, and remotely absent repositories. Independent failures **MUST** be summarized without discarding completed clones. A missing configured workspace path **MUST** be created with owner-only permissions; dry-run, status, and check **MUST NOT** create it. `colt sync` **MUST** be idempotent — running again against an already-synced workspace is safe and reports no changes.                                                                                                                                               | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-DRYRUN-001` | `colt sync --dry-run` **MUST** compute and report the same reconciliation plan while performing no local, remote, or configuration mutation. Remote metadata reads **MAY** occur.                                                                                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-SAFETY-001` | Reconciliation **MUST** keep local operations within the workspace root and **MUST NOT** rewrite origins, delete or prune repositories, overwrite unrelated or non-empty paths, or reset dirty or diverged repositories.                                                                                                   | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-REFRESH-001` | `colt sync --fetch` **MUST** run a bounded `git fetch --prune` from the validated `origin` for each present repository without changing its current branch, index, or worktree. It **MUST NOT** refresh a repository cloned by the same invocation. Independent fetch failures **MUST** be summarized without preventing other repositories from refreshing. In dry-run mode Colt **MUST** report each intended fetch without invoking Git mutation. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `WORKSPACE-PULL-001` | `colt sync --pull` **MUST** update each present repository's checked-out branch from the same branch on its validated `origin`, using fast-forward-only semantics with hooks disabled. It **MUST** reject dirty worktrees, detached heads, divergence, missing remote branches, and unsafe repository-local Git configuration without resetting, merging, rebasing, or discarding local work. Independent pull failures **MUST** be summarized. In dry-run mode Colt **MUST** report each intended pull without invoking Git mutation. `--pull` and `--fetch` **MUST** be mutually exclusive. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

The namespace form selects every repository in that exact namespace and places it directly beneath the explicit existing directory. `--save` requires `--workspace <name>` and persists that named workspace after validation and before cloning; it is incompatible with `--dry-run`. Without `--save`, `--workspace` is not required for an ad-hoc namespace sync.

### colt sync vs colt clone vs colt mirror

| Command | Direction | Scope | What it does |
|:--------|:----------|:------|:-------------|
| `colt clone` | provider → local | single repo | Clone one repo from a provider, configure credential helper |
| `colt sync` | provider → local | manifest-driven | Clone missing repos; optionally fetch-prune remote branches or fast-forward clean worktrees when explicitly requested |
| `colt mirror` | provider → provider | namespace / one repo | Copy repos from a source provider namespace (or a single repository with `--repository`) to a target provider; one-way copy, not continuous sync |

### Mirror (M7 prerequisite)

`colt mirror` copies a source namespace from one provider to another. It is useful with M7 local providers: mirror from GitHub/GitLab to your local Gitea/Forgejo instance. `--namespace` overrides the source namespace; `--target-namespace` writes into a different namespace on the target provider (defaults to the target provider's configured namespace); `--repository` mirrors a single repository instead of the whole source namespace. `--replace` force-pushes the source mirror's refs to an existing target repository; it does not delete/recreate the provider repository or modify unrelated provider state.

``` text
colt mirror <source-provider> <target-provider> [--namespace <namespace>] [--target-namespace <namespace>] [--repository <project>] [--replace]
```

| ID                    | Requirement                                                                                                                                                                                                                                                                                                           | Acceptance specification                                    |
|:----------------------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------|
| `MIRROR-001`        | `colt mirror` **MUST** list all visible repositories from the source provider namespace, clone each into a temporary directory, create the repository on the target provider, push all branches and tags, then clean up the temporary directory.                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-002`        | Independent repository failures **MUST** be summarized without discarding completed mirrors. A mirror of one repo must not abort the mirror of other repos.                                                                                                                                                          | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-003`        | `colt mirror` **MUST NOT** overwrite an existing repository on the target provider unless `--replace` is supplied. Without `--replace`, an existing target repository MUST fail safely.                                                                                                                              | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-004`        | The mirror **MUST** be one-way: source and target remain independent after mirroring. `colt mirror` is not a continuous sync — subsequent changes on the source are not reflected on the target unless `colt mirror` is run again.                                                                                      | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-005`        | Remote URLs on the target **MUST** be credential-free. The target provider's remote URL **MUST** be used, not the source provider's URL.                                                                                                                                                                           | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-006`        | `--target-namespace` **MUST** redirect repository creation and push to a validated namespace on the target provider, defaulting to the target provider's configured namespace when omitted. It **MUST NOT** weaken target-side host/namespace/repository validation.                                                  | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |
| `MIRROR-007`        | `--repository <project>` **MUST** mirror exactly one repository from the source namespace and **MUST** fail actionably if no listed source repository matches. The source and target names **MUST** be matched case-insensitively so a target provider that normalizes names (for example GitLab lowercasing) still verifies. | [`workspace_reconciliation.feature`](../../features/workspace_reconciliation.feature) |

Status and sync resolve each workspace root's ancestors once (so platform paths such as macOS `/tmp` work), reject a link at the root itself or beneath it, and inspect or install through an opened `os.Root`. `colt sync` creates a missing configured workspace path with owner-only permissions; `colt status` and `colt check` never create it. Clone data is prepared in a private temporary directory and descriptor-relatively installed only after Git and credential-helper setup succeeds. Destructive local repository pruning remains deferred.
