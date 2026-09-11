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
- **Credential:** a provider API credential used by Colt for hosting-provider API operations; distinct from Git transport credentials.
- **Credential source:** where a credential comes from: interactive secure storage or an environment variable.

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
| `CORE-PROVIDER-001` | Colt **MUST** configure GitHub.com, GitLab.com, and self-hosted GitLab. Each provider **MUST** have a unique alias, type, host, namespace, default visibility, identity name, identity email, and credential source (interactive secure storage or environment variable).                                                                                                                                               | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-002` | `colt auth login` **MUST** validate credentials directly against the configured host and identify the authenticated account before reporting success. An existing alias **MUST** require explicit `--replace`, and replacement **MUST** preserve the prior configuration on validation or save failure.                                                                                                                                              | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-003` | Self-hosted GitLab **MUST** use its configured base URL for authentication and later provider operations.                                                                                                                                                                                                                                                                                                                                            | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-004` | Provider API endpoints **MUST** use HTTPS, and Colt **MUST NOT** forward credentials across a redirect to another host.                                                                                                                                                                                                                                                                                                                              | Security integration test.                                                                                                               |
| `CORE-PROVIDER-005` | `colt auth status` **MUST** show configured providers deterministically by alias. By default it **MUST** perform a lightweight read-only provider API check using the configured credential source and report the authenticated account and connection state. `--offline` **MUST** inspect configuration only and **MUST NOT** read credentials or invoke provider or Git operations. Neither mode may mutate provider, Git, or configuration state. | [`provider_configuration.feature`](../../features/provider_configuration.feature) and authentication output in [shared core](00-core.md) |
| `CORE-PROVIDER-006` | `colt auth login github <alias>` SHOULD provide a native interactive GitHub authorization flow. The flow MUST communicate directly with GitHub HTTP APIs and MUST NOT invoke `gh`, `curl`, or another provider-specific CLI. The exact authorization application model, registration details, scopes, token lifetime, and refresh behavior SHOULD remain implementation-specific unless they affect observable behavior. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-PROVIDER-007` | Provider-specific authentication implementations MUST remain behind the provider abstraction. The common authentication model SHOULD support interactive securely stored credentials and noninteractive environment credentials. Provider-specific authorization details SHOULD remain inside provider integrations. | Architecture review. |

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

## Provider API Authentication vs Git Transport Authentication

Provider API authentication and Git transport authentication are distinct concepts:

``` text
Provider API authentication
    !=
Git repository transport authentication
```

Provider API authentication is used by Colt itself for operations such as:

``` text
authenticate account
inspect account
list repositories
create repository
inspect repository
create release
```

Git transport authentication is used by native Git for operations such as:

``` text
clone
fetch
push
```

A successful provider API authentication check MUST NOT imply that Git clone or push access has been validated.

Likewise, working SSH access to a Git repository MUST NOT imply that Colt has valid provider API credentials.

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

For GitHub interactive login, the flow communicates the authorization URL and code before the success line:

``` text
Authorize Colt with GitHub.

Open: https://github.com/login/device
Code: ABCD-EFGH

Waiting for authorization...

✓ personal · GitHub · MozeBaltyk · default
```

The authorization code is not an access token and may be displayed when required by the authorization flow. Exact wording and whether a browser is opened automatically are implementation details.

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
| `CORE-CREDENTIAL-001` | Credentials MAY come from interactive secure storage or environment variables. Colt MUST use the configured `token_env` when present; otherwise it MUST use `GITHUB_TOKEN` for GitHub or `GITLAB_TOKEN` for GitLab. An OS credential store MAY be used for interactive credentials. | [`provider_configuration.feature`](../../features/provider_configuration.feature)                         |
| `CORE-CREDENTIAL-002` | Tokens MUST NOT be stored as plaintext in normal Colt configuration or project files and MUST be redacted from normal, verbose, and error output. Interactive credentials stored in the OS credential store MUST NOT appear in normal, verbose, or error output either.                                           | Security constraint and [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-003` | A missing or empty selected token environment variable MUST fail before an authenticated request or project mutation. For `colt auth status`, this is reported as a per-provider `credentials missing` connection state rather than as a successful connection. | [`provider_configuration.feature`](../../features/provider_configuration.feature)                         |
| `CORE-CREDENTIAL-004` | Normal Colt configuration MUST contain provider configuration and credential references, but MUST NOT contain access-token values. Interactive credentials SHOULD be stored using the operating system's secure credential facility. If secure persistent credential storage is unavailable, Colt MUST fail safely or explicitly use a non-persistent authentication mode. It MUST NOT silently fall back to plaintext credential storage. | Security review. |
| `CORE-CREDENTIAL-005` | When more than one credential source can exist, Colt MUST resolve credentials in this deterministic order: explicit configured environment credential; securely stored interactive credential; provider conventional environment credential; otherwise fail with `credentials missing`. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-IDENTITY-001`   | Colt MUST apply the selected identity with repository-local Git configuration before creating a commit and MUST NOT change global Git identity.                                                                                                             | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature)             |
| `CORE-GIT-001`        | Colt MUST report an actionable error before mutation if native `git` is unavailable for a Git operation.                                                                                                                                                        | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature)             |

## Secure Credential Persistence

Normal Colt configuration MUST contain provider configuration and credential references, but MUST NOT contain access-token values. For example, configuration MAY contain:

``` yaml
providers:
  personal:
    type: github
    host: github.com
    namespace: mozebaltyk
    auth:
      method: interactive
```

or:

``` yaml
providers:
  ci:
    type: github
    host: github.com
    token_env: GITHUB_TOKEN
```

but MUST NOT contain:

``` yaml
token: ghp_...
access_token: ...
```

Interactive credentials SHOULD be stored using the operating system's secure credential facility. The specification describes the security property rather than prescribing a specific library.

If secure persistent credential storage is unavailable, Colt MUST fail safely or explicitly use a non-persistent authentication mode. It MUST NOT silently fall back to plaintext credential storage.

## Git Repository Authentication

Colt MUST NOT treat provider API credentials and Git credentials as one conceptual credential. Native Git remains responsible for Git repository mechanics.

Git repository operations MAY use SSH or HTTPS with process-scoped credentials.

### SSH

Colt SHOULD support repositories whose authoritative provider metadata exposes an SSH clone/push URL. Colt MUST NOT require ownership of the user's SSH private keys. Existing SSH configuration, keys, agents, and platform facilities may be used by native Git.

For the initial implementation, Colt SHOULD NOT generate SSH keys, modify SSH configuration, install keys, or manage `ssh-agent`. This preserves the architectural rule that native `git` is Colt's only required external executable. If SSH key management is introduced later, it must be specified separately because tools such as `ssh-keygen` or `ssh-agent` would change the external-executable boundary.

### HTTPS

For HTTPS Git operations, provider credentials MAY be supplied to native Git using an ephemeral/process-scoped mechanism. Credentials MUST NOT be embedded persistently in origin URLs, `.git/config`, workspace manifests, Colt configuration, or command output.

## Safety, Conflicts, And Partial Failure

These rules are authoritative for implemented and planned capabilities unless a capability adds a stricter rule.

| ID                  | Requirement                                                                                                                                                                                                     | Verification                                                                                  |
|:--------------------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------|
| `CORE-SAFETY-001`   | Colt operations **MUST** be non-destructive by default: Colt **MUST NOT** overwrite a non-empty destination, replace unrelated state, discard changes, or delete local or remote state as automatic rollback.   | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-CONFLICT-001` | Colt **SHOULD** perform inexpensive preflight checks, but provider mutations **MUST** remain authoritative and Colt **MUST** safely handle a provider conflict even when an earlier existence check found none. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-FAILURE-001`  | On partial failure Colt **MUST** stop, preserve completed work, report local and remote state and the failed step, provide a safe recovery action, and return failure.                                          | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

Errors **SHOULD** distinguish invalid input, missing credentials, authentication, authorization, connectivity, conflict, and partial completion. Output **SHOULD** be concise and identify affected resources without exposing credentials.
