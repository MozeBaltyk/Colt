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
| `INIT-005` | With `--local`, Colt **MUST** stop successfully after local initialization and **MUST NOT** call a provider mutation or add `origin`.                                                         | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-006` | Without `--local`, Colt **MUST** create a repository in the selected namespace through the selected provider’s direct HTTP API using configured defaults.                                     | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-007` | After remote creation, Colt **MUST** add only the created repository URL as `origin` and push the initial branch and commit with upstream tracking using native `git`.                        | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-008` | Colt **MUST** treat both a pre-existing remote and an authoritative conflict returned during creation as conflicts and apply `CORE-SAFETY-001` and `CORE-CONFLICT-001`.                       | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `INIT-009` | Failures after local or remote mutation **MUST** follow `CORE-FAILURE-001`; a push failure **MUST** leave the local commit, `origin`, and created remote intact.                              | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

On success, output **SHOULD** identify the provider alias, namespace, project, local path, initial commit, and remote URL when present.

The Gherkin files are acceptance specifications. Active M1 behavior is covered by requirement-named Go tests without prescribing a BDD framework.
