# Blank Project Initialization Specification

This is the detailed normative **first MVP** project-creation capability. It uses every applicable requirement in [shared core](00-core.md).

``` text
colt init <project> [--local] [--provider <alias>]
```

The command creates a blank project. Parameterized template selection and materialization belong to planned [Milestone 3](03-template-init.md).

## Requirements

| ID         | Requirement                                                                                                                                                                                   | Acceptance specification                                                                      |
|:-----------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------|
| `INIT-001` | Colt **MUST** validate the project name, provider configuration, identity, destination, and required runtime before mutation where practical.                                                 | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-002` | Colt **MUST** resolve a provider using `CORE-RESOLVE-001`, including for `--local`, because provider selection supplies identity.                                                             | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-003` | Colt **MUST** refuse an existing non-empty destination without modifying it.                                                                                                                  | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-004` | Colt **MUST** create the destination, initialize a new Git repository with no inherited remotes, apply `CORE-IDENTITY-001`, and create one initial commit. The blank commit **MAY** be empty. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-005` | With `--local`, Colt **MUST NOT** resolve or require provider API credentials, construct or contact a provider client, add `origin`, configure a credential helper, or push. It **MUST** stop successfully after local initialization. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-006` | Without `--local`, Colt **MUST** create a repository in the selected namespace through the selected provider's direct HTTP API using configured defaults.                                     | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-007` | After remote creation, Colt **MUST** add only the created repository URL as `origin` and push the initial branch and commit with upstream tracking using native `git`. The clone URL transport (HTTPS or SSH) MUST correspond to the selected provider and available authentication, selected per the deterministic transport preference (`CORE-GIT-009`) with product default HTTPS. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-008` | Colt **MUST** treat both a pre-existing remote and an authoritative conflict returned during creation as conflicts and apply `CORE-SAFETY-001` and `CORE-CONFLICT-001`.                       | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-009` | Failures after local or remote mutation **MUST** follow `CORE-FAILURE-001`; a push failure **MUST** leave the local commit, `origin`, and created remote intact.                              | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

On success, output **SHOULD** identify the provider alias, namespace, project, local path, initial commit, and remote URL when present.

Remote repository creation requires provider API authentication regardless of the Git transport later selected for the initial push:

``` text
colt init project
        |
        +-> provider API authentication
        |       |
        |       +-> create remote repository
        |
        +-> local Git initialization
        |
        +-> select authoritative Git transport (SSH or HTTPS)
        |
        +-> native git push
```

SSH Git access does NOT imply permission to create the provider repository. `INIT-007` transport selection MUST still correspond to the selected provider and available authentication without weakening authority/host validation.

## Transport-specific initialization

For HTTPS transport (`INIT-007`, `CORE-GIT-005`, `CORE-GIT-008`):

``` text
colt init project
    |
    +-> provider API creates repository
    |
    +-> origin=https://provider/owner/project.git (authoritative, credential-free)
    |
    +-> local credential.helper configured for Colt (helper = colt)
    |
    +-> native Git push (process-scoped credential for the Colt-driven push;
        subsequent ordinary git push resolves via the helper)
```

The resulting repository contains a helper reference, never a token (see the HTTPS example in [shared core](00-core.md)).

For SSH transport:

``` text
colt init project
    |
    +-> provider API creates repository
    |
    +-> origin=git@provider:owner/project.git (authoritative SSH URL, SSH user git)
    |
    +-> existing user SSH setup used (no Colt key management per CORE-GIT-010)
    |
    +-> native Git push
```

Git commit identity is applied repository-local in both cases (`user.name`/`user.email` set via `git config --local`) and remains independent of the transport.

The Gherkin files are acceptance specifications. Active M1 behavior is covered by requirement-named Go tests without prescribing a BDD framework.
