# Shared Core Specification

This document defines normative behavior shared by the active MVP. The key words
**MUST**, **MUST NOT**, **SHOULD**, and **SHOULD NOT** are to be interpreted as
described in RFC 2119 and RFC 8174 when, and only when, capitalized.

## Boundaries And Terminology

- **Provider:** a configured hosting integration, identified by a unique alias.
- **Account:** the authenticated provider principal.
- **Namespace:** the provider-independent owner scope containing repositories.
- **Identity:** repository-local Git `user.name` and `user.email`.
- **Defaults:** provider- or namespace-associated choices such as visibility.
- **Project:** a repository being initialized or managed by Colt.
- **Workspace:** the local root under which Colt may place projects.

| ID | Requirement | Verification |
| --- | --- | --- |
| `CORE-ARCH-001` | Hosting operations **MUST** use direct GitHub or GitLab HTTP APIs and **MUST NOT** invoke `gh`, `glab`, or `curl`. | Architecture constraint; unsuitable for CLI BDD. |
| `CORE-ARCH-002` | Native `git` **MUST** be the sole external executable. | Architecture constraint; unsuitable for CLI BDD. |
| `CORE-MODEL-001` | Provider, account, namespace, identity, and defaults **MUST** remain logically separate concepts; the design **MUST NOT** assume a one-to-one relationship among them. The MVP **SHOULD** avoid separate configuration objects until behavior requires them. | Architecture review; non-behavioral. |
| `CORE-NAMESPACE-001` | Common commands, output, and models **MUST** use `namespace`; provider-native vocabulary **SHOULD** remain inside provider integrations. | Specification and implementation review; non-behavioral. |

## Provider Configuration And Authentication

The active MVP provides provider setup through:

```text
colt auth add <github|gitlab> <alias>
```

Storage shape is intentionally unspecified. A single structure may hold these
logically separate values during the MVP.

| ID | Requirement | Verification |
| --- | --- | --- |
| `CORE-PROVIDER-001` | Colt **MUST** configure GitHub.com, GitLab.com, and self-hosted GitLab. Each provider **MUST** have a unique alias, type, host, namespace, default visibility, identity name, identity email, and environment-based credential source. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-PROVIDER-002` | `colt auth add` **MUST** validate credentials directly against the configured host before reporting success and **MUST** preserve the prior configuration on failure. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-PROVIDER-003` | Self-hosted GitLab **MUST** use its configured base URL for authentication and later provider operations. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-PROVIDER-004` | Provider API endpoints **MUST** use HTTPS, and Colt **MUST NOT** forward credentials across a redirect to another host. | Security integration test. |

An illustrative configuration may use `token_env` without prescribing the full
configuration schema:

```yaml
providers:
  work:
    type: gitlab
    default: true
    host: gitlab.company.example
    namespace: infrastructure
    token_env: GITLAB_TOKEN
```

## Provider Resolution

| ID | Requirement | Verification |
| --- | --- | --- |
| `CORE-RESOLVE-001` | A command requiring one provider **MUST** resolve it in exactly this order: explicit `--provider <alias>`; the configured default; the only configured provider of any type; otherwise fail. It **MUST NOT** prefer a provider type. | [`provider_resolution.feature`](../../features/provider_resolution.feature) |
| `CORE-RESOLVE-002` | An unknown explicit alias, multiple configured defaults, or an invalid selected configuration **MUST** fail without falling through to a lower-precedence candidate. | [`provider_resolution.feature`](../../features/provider_resolution.feature) |

## Credentials And Identity

| ID | Requirement | Verification |
| --- | --- | --- |
| `CORE-CREDENTIAL-001` | MVP credentials **MUST** come from environment variables. Colt **MUST** use the configured `token_env` when present; otherwise it **MUST** use `GITHUB_TOKEN` for GitHub or `GITLAB_TOKEN` for GitLab. An OS keyring **MUST NOT** be required. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-002` | Tokens **MUST NOT** be stored as plaintext in normal Colt configuration or project files and **MUST** be redacted from normal, verbose, and error output. | Security constraint and [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-003` | A missing or empty selected token environment variable **MUST** fail before an authenticated request or project mutation. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-IDENTITY-001` | Colt **MUST** apply the selected identity with repository-local Git configuration before creating a commit and **MUST NOT** change global Git identity. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-GIT-001` | Colt **MUST** report an actionable error before mutation if native `git` is unavailable for a Git operation. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

## Safety, Conflicts, And Partial Failure

These rules are authoritative for every capability and are not repeated in
capability specifications.

| ID | Requirement | Verification |
| --- | --- | --- |
| `CORE-SAFETY-001` | Colt operations **MUST** be non-destructive by default: Colt **MUST NOT** overwrite a non-empty destination, replace unrelated state, discard changes, or delete local or remote state as automatic rollback. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-CONFLICT-001` | Colt **SHOULD** perform inexpensive preflight checks, but provider mutations **MUST** remain authoritative and Colt **MUST** safely handle a provider conflict even when an earlier existence check found none. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-FAILURE-001` | On partial failure Colt **MUST** stop, preserve completed work, report local and remote state and the failed step, provide a safe recovery action, and return failure. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

Errors **SHOULD** distinguish invalid input, authentication failure, conflict,
and partial completion. Output **SHOULD** be concise and identify affected
resources without exposing credentials.

See [blank project initialization](01-project-init.md) for capability-specific
active-MVP requirements.
