# Project Lifecycle Specification

**Milestone 2 is planned and not implemented.** Its scenarios describe intended acceptance behavior, not current command or release availability. It uses normal provider resolution and the shared rules in [shared core](00-core.md).

## Repository Operations

``` text
colt list [--provider <alias> | --all]
colt clone <repository> [--provider <alias>] [--transport https|ssh]
```

Lifecycle provides the list and clone primitives later consumed by [workspace reconciliation](04-workspace.md). It defines no `colt sync` behavior.

| ID                    | Planned requirement                                                                                                                                                                                                                                                                                               | Acceptance specification                                                |
|:----------------------|:------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------|
| `LIFECYCLE-LIST-001`  | `colt list` **MUST** return deterministic repository results for one provider selected by normal resolution unless `--all` explicitly requests every configured provider. With `--all`, independent provider failures **MUST** be reported without losing successful results.                                     | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `LIFECYCLE-CLONE-001` | `colt clone` **MUST** resolve the provider and repository unambiguously, use an authoritative clone target from selected-provider metadata (HTTPS or SSH, selected per `CORE-GIT-009` with product default HTTPS), keep the persistent remote URL credential-free, configure the repository-local Colt credential helper for HTTPS clones (so subsequent ordinary `git fetch`/`pull`/`push` authenticate without re-entering credentials), leave SSH key management to the user's existing SSH environment, and refuse an unrelated or non-empty destination. The effective clone target **MUST** still match the selected provider, host, namespace, and repository, and existing authority/host validation MUST NOT be weakened. For the initial Colt-driven clone, HTTPS credentials MAY be supplied process-safely without embedding them in the clone URL. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |

### Clone Nuances

| ID                       | Requirement                                                                                                                                                                                                                                                                                                                          | Acceptance specification                                            |
|:-------------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------|
| `LIFECYCLE-CLONE-002`  | Clone target **MUST** come from provider API metadata, never from assumed hostname or path patterns.                                                                                                                                                                                                                | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `LIFECYCLE-CLONE-003`  | The persistent remote URL **MUST** be credential-free — no tokens, passwords, userinfo, or authorization headers in the clone URL or `.git/config`.                                                                                                                                                                                     | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `LIFECYCLE-CLONE-004`  | For HTTPS clones, Colt **MUST** configure the repository-local credential helper (`helper = colt`) so subsequent ordinary `git fetch`/`pull`/`push` authenticate without re-entering credentials. For SSH clones, Colt **MUST NOT** configure a credential helper and **MUST** use the user's existing SSH environment without reading private keys. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `LIFECYCLE-CLONE-005`  | Colt **MUST** refuse an unrelated or non-empty destination. The destination **MUST** be a clean relative missing or empty path. Relative paths resolve from the current directory and require an existing parent.                                                                                                                          | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `LIFECYCLE-CLONE-006`  | For the initial Colt-driven HTTPS clone, credentials **MAY** be supplied process-safely without embedding them in the clone URL. The credential exists only in process memory and the configured credential backend during the interaction.                                                                                              | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |

## Minimum Release Flow

``` text
colt release <version> [--provider <alias>]
```

The initial provider-independent release flow is:

``` text
validate repository
-> validate version and tag
-> create local tag
-> push tag (configured Git transport: SSH or HTTPS)
-> create provider release (requires provider API authentication)
```

Local tag push and provider release creation are separate operations; if the tag push succeeds but provider release creation fails, Colt MUST report the partial state per `CORE-FAILURE-001` without destructive rollback. Provider release creation is a per-provider capability (`CORE-PROVIDER-011`): a future adapter whose platform lacks releases reports that distinctly rather than failing as a generic error.

| ID                      | Planned requirement                                                                                                                                                                                                                                                                                                                                                                                                                         | Acceptance specification                                                |
|:------------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------|
| `LIFECYCLE-RELEASE-001` | `colt release` **MUST** resolve one provider, validate the repository and tag before mutation, verify that the effective push target belongs to the selected provider and repository, then create the local tag, push it over the configured transport (HTTPS via the Colt credential helper / process-scoped credential for the Colt-driven push without Colt reading private keys, or SSH via the user's existing SSH environment without Colt reading private keys), and create the provider release using provider API authentication, in that order. It **MUST NOT** overwrite or force an existing tag or release. The persistent remote URL **MUST** stay credential-free, and partial failure **MUST** follow `CORE-FAILURE-001`. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |

Implementation-level Git hardening, redirect handling, and path-confinement mechanisms should be specified with the implementation when this milestone becomes active, while preserving the security properties above. Transport preference precedence, the HTTPS helper contract, and SSH ownership rules are defined by `CORE-GIT-004` through `CORE-GIT-010` in [shared core](00-core.md) and apply here.

| ID | Planned requirement | Acceptance specification |
|:---|:---|:---|
| `LIFECYCLE-TRANSPORT-001` | HTTPS transport **MUST** select the authoritative HTTPS repository URL and SSH transport the authoritative SSH URL; an explicit transport preference **MUST** override the product default (HTTPS); an unsupported transport **MUST** fail before repository mutation. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |

Advanced release notes, changelog integration, and signed tags are deferred.
