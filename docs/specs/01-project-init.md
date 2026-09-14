# Blank Project Initialization Specification

This is the detailed normative **first MVP** project-creation capability. It uses every applicable requirement in [shared core](00-core.md).

``` text
colt init <project> [--local] [--provider <alias>] [--destination <path>] [--visibility <private|public>]
```

The command creates a blank project. Parameterized template selection and materialization belong to planned [Milestone 3](03-template-init.md).

## Requirements

| ID         | Requirement                                                                                                                                                                                   | Acceptance specification                                                                      |
|:-----------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------|
| `INIT-001` | Colt **MUST** validate the project name, provider configuration, identity, destination, and required runtime before mutation where practical.                                                 | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-002` | Colt **MUST** resolve a provider using `CORE-RESOLVE-001`, including for `--local`, because provider selection supplies identity.                                                             | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-003` | Colt **MUST** refuse an existing non-empty destination without modifying it.                                                                                                                  | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-004` | Colt **MUST** clone provider-created repositories to `<user-home>/<namespace>/<project>` by default without injecting credentials into the URL, arguments, environment, or persistent Git configuration, apply `CORE-IDENTITY-001`, and create one initial commit. `--destination <path>` replaces the complete remote clone path; relative paths resolve from the current directory and require an existing parent. It is invalid with `--local`. With `--local`, the destination remains `<current-directory>/<project>` and is initialized directly. Existing non-empty, file, and symlink destinations are refused. The blank commit **MAY** be empty. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-005` | With `--local`, Colt **MUST NOT** resolve or require provider API credentials, construct or contact a provider client, add `origin`, configure a credential helper, or push. It **MUST** stop successfully after local initialization. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-006` | Without `--local`, Colt **MUST** create a repository in the selected namespace through the selected provider's direct HTTP API using configured defaults. The creation path MUST work through the provider abstraction for any supported type (currently GitHub, GitLab, Gitea, Forgejo). | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-007` | After remote creation, Colt **MUST** clone the created repository at the resolved destination, retain only the created repository URL as `origin`, and push the initial branch and commit with upstream tracking using native `git`. Host and repository matching remain exact; only GitHub owner matching is case-insensitive because GitHub account names are case-insensitive and its API may canonicalize owner casing. The clone URL transport (HTTPS or SSH) MUST correspond to the selected provider and available authentication, selected per the deterministic transport preference (`CORE-GIT-009`) with product default HTTPS. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-008` | Colt **MUST** treat both a pre-existing remote and an authoritative conflict returned during creation as conflicts and apply `CORE-SAFETY-001` and `CORE-CONFLICT-001`.                       | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-009` | Failures after local or remote mutation **MUST** follow `CORE-FAILURE-001`; a push failure **MUST** leave the local commit, `origin`, and created remote intact.                              | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-010` | Provider `visibility` configuration **MUST** be the default for remote repository creation. `colt init --visibility <private|public>` **MUST** override that default for the repository being created without changing provider configuration. An omitted override uses the configured default. The option **MUST** reject other values before mutation and **MUST** be invalid with `--local`, which creates no remote repository. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-011` | `colt init` human output **MUST** be one deterministic multi-line report headed by the operation and project. It **MUST** list each applicable step in execution order as succeeded, failed, or skipped; local initialization MUST NOT claim remote-only work. Remote steps are preflight, provider API authentication or credential resolution as applicable, remote lookup, remote creation, clone, repository-local identity, initial commit, HTTPS credential helper when applicable, and push. After partial failure it MUST retain completed outcomes, mark the failed and useful later applicable steps, report preserved local and remote state, classify the cause from bounded sanitized evidence, and give a safe actionable recovery command or suggestion. Advice MUST match evidence: helper-not-found, public-key denial, host-key verification, provider authentication rejection, and connectivity are distinct; unknown failures use generic safe recovery, and PATH MUST NOT be blamed without exact evidence. Reusable secrets, credential-bearing URLs, authorization headers, and unbounded provider or Git output MUST NOT be emitted. ANSI color MAY distinguish green success, red failure, and yellow skipped/warning only when output itself is a terminal and `NO_COLOR` is absent; symbols and text MUST remain meaningful without color. The report MUST be emitted once, including when the command returns failure. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

On success, output **SHOULD** identify the provider alias, namespace, project, local path, initial commit, and remote URL when present, subject to `INIT-011` redaction.

Remote repository creation requires provider API authentication regardless of the Git transport later selected for the initial push:

``` text
colt init project
        |
        +-> provider API authentication
        |       |
        |       +-> create remote repository
        |

        +-> clone to <user-home>/<namespace>/<project> (or --destination)
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
    +-> clone to owner/project with origin=https://provider/owner/project.git
        (authoritative, credential-free; transient Colt helper authenticates clone)
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
    +-> origin=<ssh-user>@provider:owner/project.git (authoritative SSH URL; the SSH user is per-provider convention, `git` for GitHub-style remotes)
    |
    +-> existing user SSH setup used (no Colt key management per CORE-GIT-010)
    |
    +-> native Git push
```

Git commit identity is applied repository-local in both cases (`user.name`/`user.email` set via `git config --local`) and remains independent of the transport.

The Gherkin files are acceptance specifications. Active M1 behavior is covered by requirement-named Go tests; the tagged Gitea and Forgejo scenarios additionally exercise the real Colt binary, HTTPS API, and Git push against containerized backends.
