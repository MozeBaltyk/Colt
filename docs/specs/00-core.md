# Shared Core Specification

This document defines normative behavior shared by the active MVP. The key words **MUST**, **MUST NOT**, **SHOULD**, and **SHOULD NOT** are interpreted as described in RFC 2119 and RFC 8174 when, and only when, capitalized.

## Boundaries And Terminology

- **Provider:** a configured hosting integration, identified by a unique alias.
- **Account:** the authenticated provider principal.
- **Namespace:** the repository owner: a GitHub username or organization, or a GitLab group or subgroup/full path.
- **Identity:** repository-local Git `user.name` and `user.email`.
- **Defaults:** provider- or namespace-associated choices such as visibility.
- **Project:** a provider-hosted or local Git repository managed by Colt.
- **Workspace:** a declared set of desired projects and the local root where Colt reconciles them.

| ID                   | Requirement                                                                                                                                                                                                                                                  | Verification                             |
|:---------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-----------------------------------------|
| `CORE-ARCH-001`      | Hosting operations **MUST** use direct GitHub or GitLab HTTP APIs and **MUST NOT** invoke `gh`, `glab`, or `curl`.                                                                                                                                           | Architecture review.                     |
| `CORE-ARCH-002`      | Native `git` **MUST** be the sole external executable.                                                                                                                                                                                                       | Architecture review.                     |
| `CORE-MODEL-001`     | Provider, account, namespace, identity, and defaults **MUST** remain logically separate concepts; the design **MUST NOT** assume a one-to-one relationship among them. The MVP **SHOULD** avoid separate configuration objects until behavior requires them. | Architecture review.                     |
| `CORE-NAMESPACE-001` | Common commands, output, and models **MUST** use `namespace`; provider-native vocabulary **SHOULD** remain inside provider integrations.                                                                                                                     | Specification and implementation review. |

## Provider Configuration And Authentication

The active MVP provides provider setup and status through:

``` text
colt auth login <github|gitlab> <alias> [--replace]
colt auth status [--offline]
```

Configuration **MUST** use `os.UserConfigDir()` with `colt/config.yaml` appended; on Linux this is `$XDG_CONFIG_HOME/colt/config.yaml` when `XDG_CONFIG_HOME` is set, otherwise `~/.config/colt/config.yaml`. `COLT_CONFIG` **MUST** override the complete path. Storage shape is intentionally unspecified.

| ID                  | Requirement                                                                                                                                                                                                                                                                                                                                                                                                                                          | Verification                                                                                                                             |
|:--------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-----------------------------------------------------------------------------------------------------------------------------------------|
| `CORE-PROVIDER-001` | Colt **MUST** configure GitHub.com, GitLab.com, and self-hosted GitLab. Each provider **MUST** have a unique alias, type, host, namespace, default visibility, identity name, identity email, and environment-based credential source.                                                                                                                                                                                                               | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-002` | `colt auth login` **MUST** validate credentials directly against the configured host and identify the authenticated account before reporting success. An existing alias **MUST** require explicit `--replace`, and replacement **MUST** preserve the prior configuration on validation or save failure.                                                                                                                                              | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-003` | Self-hosted GitLab **MUST** use its configured base URL for authentication and later provider operations.                                                                                                                                                                                                                                                                                                                                            | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-004` | Provider API endpoints **MUST** use HTTPS, and Colt **MUST NOT** forward credentials across a redirect to another host.                                                                                                                                                                                                                                                                                                                              | Security integration test.                                                                                                               |
| `CORE-PROVIDER-005` | `colt auth status` **MUST** show configured providers deterministically by alias. By default it **MUST** perform a lightweight read-only provider API check using the configured credential source and report the authenticated account and connection state. `--offline` **MUST** inspect configuration only and **MUST NOT** read credentials or invoke provider or Git operations. Neither mode may mutate provider, Git, or configuration state. | [`provider_configuration.feature`](../../features/provider_configuration.feature) and authentication output in [shared core](00-core.md) |

Human-readable authentication output, failure categories, and the distinction between provider API connectivity and repository Git connectivity are specified in authentication output in [shared core](00-core.md).

An illustrative configuration may use `token_env` without prescribing the full configuration schema:

``` yaml
providers:
  work:
    type: gitlab
    default: true
    host: gitlab.company.example
    namespace: infrastructure
    token_env: GITLAB_TOKEN
```

## Authentication Output

Default authentication output is intended for humans rather than scripts. It **MUST** remain concise, provider-independent, actionable on failure, and safe for credentials.

Human-readable provider names **SHOULD** use `GitHub` and `GitLab`. Compact state markers **SHOULD** use:

``` text
✓ success
! warning
✗ failure
```

Authenticated account, configured namespace, and Git identity are distinct concepts and **MUST NOT** be treated as interchangeable.

### `auth login`

A successful login **SHOULD** use one concise line:

``` text
✓ personal · GitHub · MozeBaltyk · default
✓ work · GitLab · source.example.com · john.doe
```

The host **SHOULD** be shown for self-hosted providers and **MAY** be omitted when the standard host is obvious.

Failures **MUST** identify the failed layer and **SHOULD** provide one safe, actionable reason when available. Human-facing categories **SHOULD** distinguish configuration failure, missing credentials, authentication failure, authorization failure, connection failure, provider unavailability, and namespace inaccessibility. Provider messages **MAY** be included only after sanitization and bounding.

### `auth status`

Providers **MUST** be displayed deterministically by alias as compact sections. The default command performs the read-only provider API check defined by `CORE-PROVIDER-005`.

``` text
personal (default)
  GitHub · github.com
  Account:     MozeBaltyk
  Namespace:   mozebaltyk
  Git name:    mozebaltyk
  Git email:   morze.baltyk@proton.me
  Connection:  ✓ connected
```

Missing identity configuration **MUST** be explicit:

``` text
Git identity: not configured
```

A successful `Connection` state means only that Colt reached the configured provider API, the credentials were accepted, and the authenticated account was identified. It **MUST NOT** imply that repository clone or push access was tested.

Connection failures **SHOULD** use concise states such as:

``` text
Connection:  ✗ credentials missing
Connection:  ✗ authentication failed
Connection:  ✗ unreachable
Connection:  ✗ provider unavailable
Connection:  ✗ timeout
```

When useful, one indented safe explanation **SHOULD** follow the state.

`colt auth status --offline` **MUST** inspect configuration only, **MUST NOT** read credentials, and **MUST NOT** claim a live connection:

``` text
Connection:  not checked
```

If `colt auth status` runs inside a Colt-managed Git repository with a concrete `origin`, Colt **MAY** perform an additional read-only Git access check:

``` text
Git access:  ✓ origin reachable
```

This check **MUST** remain distinct from provider API connectivity and **MUST NOT** push, mutate remotes, or modify repository state. It **MUST NOT** run when there is no concrete repository remote to validate.

The default provider **SHOULD** be displayed as `personal (default)` rather than as an internal field such as `default=true`. Standard and self-hosted hosts **SHOULD** use the same display structure.

Normal output **MUST NOT** expose tokens, token fragments, authorization headers, or raw secret values and **SHOULD NOT** expose token scopes, credential-source paths, API endpoints, HTTP headers, Git protocol details, raw provider responses, or internal configuration keys.

A future machine-readable mode such as `colt auth status --json` **MAY** expose structured non-secret fields, but default output **MUST NOT** be optimized for parsing.

> Successful operations should be terse. Status commands may provide structured detail. Failures should identify the failed layer and one actionable reason, without exposing internal representation or secrets.

## Provider Resolution

| ID                 | Requirement                                                                                                                                                                                                                          | Verification                                                                |
|:-------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------|
| `CORE-RESOLVE-001` | A command requiring one provider **MUST** resolve it in exactly this order: explicit `--provider <alias>`; the configured default; the only configured provider of any type; otherwise fail. It **MUST NOT** prefer a provider type. | [`provider_resolution.feature`](../../features/provider_resolution.feature) |
| `CORE-RESOLVE-002` | An unknown explicit alias, multiple configured defaults, or an invalid selected configuration **MUST** fail without falling through to a lower-precedence candidate.                                                                 | [`provider_resolution.feature`](../../features/provider_resolution.feature) |

## Credentials And Identity

| ID                    | Requirement                                                                                                                                                                                                                                                         | Verification                                                                                              |
|:----------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------------------|
| `CORE-CREDENTIAL-001` | MVP credentials **MUST** come from environment variables. Colt **MUST** use the configured `token_env` when present; otherwise it **MUST** use `GITHUB_TOKEN` for GitHub or `GITLAB_TOKEN` for GitLab. An OS keyring **MUST NOT** be required.                      | [`provider_configuration.feature`](../../features/provider_configuration.feature)                         |
| `CORE-CREDENTIAL-002` | Tokens **MUST NOT** be stored as plaintext in normal Colt configuration or project files and **MUST** be redacted from normal, verbose, and error output.                                                                                                           | Security constraint and [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-003` | A missing or empty selected token environment variable **MUST** fail before an authenticated request or project mutation. For `colt auth status`, this is reported as a per-provider `credentials missing` connection state rather than as a successful connection. | [`provider_configuration.feature`](../../features/provider_configuration.feature)                         |
| `CORE-IDENTITY-001`   | Colt **MUST** apply the selected identity with repository-local Git configuration before creating a commit and **MUST NOT** change global Git identity.                                                                                                             | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature)             |
| `CORE-GIT-001`        | Colt **MUST** report an actionable error before mutation if native `git` is unavailable for a Git operation.                                                                                                                                                        | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature)             |

## Safety, Conflicts, And Partial Failure

These rules are authoritative for implemented and planned capabilities unless a capability adds a stricter rule.

| ID                  | Requirement                                                                                                                                                                                                     | Verification                                                                                  |
|:--------------------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------|
| `CORE-SAFETY-001`   | Colt operations **MUST** be non-destructive by default: Colt **MUST NOT** overwrite a non-empty destination, replace unrelated state, discard changes, or delete local or remote state as automatic rollback.   | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-CONFLICT-001` | Colt **SHOULD** perform inexpensive preflight checks, but provider mutations **MUST** remain authoritative and Colt **MUST** safely handle a provider conflict even when an earlier existence check found none. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-FAILURE-001`  | On partial failure Colt **MUST** stop, preserve completed work, report local and remote state and the failed step, provide a safe recovery action, and return failure.                                          | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

Errors **SHOULD** distinguish invalid input, missing credentials, authentication, authorization, connectivity, conflict, and partial completion. Output **SHOULD** be concise and identify affected resources without exposing credentials.
