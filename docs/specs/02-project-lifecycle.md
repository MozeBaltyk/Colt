# Project Lifecycle Specification

**Milestone 2 is planned and not implemented.** Its scenarios describe intended acceptance behavior, not current command or release availability. It uses normal provider resolution and the shared rules in [shared core](00-core.md).

## Repository Operations

``` text
colt list [--provider <alias> | --all]
colt clone <repository> [--provider <alias>]
```

Lifecycle provides the list and clone primitives later consumed by [workspace reconciliation](04-workspace.md). It defines no `colt sync` behavior.

| ID                    | Planned requirement                                                                                                                                                                                                                                                                                               | Acceptance specification                                                |
|:----------------------|:------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------|
| `LIFECYCLE-LIST-001`  | `colt list` **MUST** return deterministic repository results for one provider selected by normal resolution unless `--all` explicitly requests every configured provider. With `--all`, independent provider failures **MUST** be reported without losing successful results.                                     | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `LIFECYCLE-CLONE-001` | `colt clone` **MUST** resolve the provider and repository unambiguously, use the authoritative clone URL from selected-provider metadata (HTTPS or SSH as supported), keep credentials process-scoped, and refuse an unrelated or non-empty destination. The effective clone target **MUST** remain within the selected workspace root. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |

## Minimum Release Flow

``` text
colt release <version> [--provider <alias>]
```

The initial provider-independent release flow is:

``` text
validate repository
-> validate version and tag
-> create local tag
-> push tag
-> create provider release
```

| ID                      | Planned requirement                                                                                                                                                                                                                                                                                                                                                                                                                         | Acceptance specification                                                |
|:------------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------|
| `LIFECYCLE-RELEASE-001` | `colt release` **MUST** resolve one provider, validate the repository and tag before mutation, verify that the effective push target belongs to the selected provider and repository, then create the local tag, push it, and create the provider release in that order. It **MUST NOT** overwrite or force an existing tag or release. Credentials **MUST** remain process-scoped, and partial failure **MUST** follow `CORE-FAILURE-001`. Push transport (HTTPS or SSH) MUST correspond to the selected provider and available authentication. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |

Implementation-level Git hardening, redirect handling, and path-confinement mechanisms should be specified with the implementation when this milestone becomes active, while preserving the security properties above.

Advanced release notes, changelog integration, and signed tags are deferred.
