# Shared Core Specification

This document defines normative behavior shared by the active MVP. The key words **MUST**, **MUST NOT**, **SHOULD**, and **SHOULD NOT** are interpreted as described in RFC 2119 and RFC 8174 when, and only when, capitalized.

## Boundaries And Terminology

- **Provider:** a configured hosting integration, identified by a unique alias.
- **Provider type:** the hosting platform family a provider speaks to (`github`, `gitlab`; `gitea` and `forgejo` are planned future types, each an independently identifiable adapter).
- **Account:** the authenticated provider principal.
- **Namespace:** the repository owner in provider-native terms (for example a GitHub username or organization, or a GitLab group or subgroup/full path). Namespace semantics differ per provider type and live inside provider adapters; common code MUST only treat a namespace as an opaque owner string.
- **Identity:** repository-local Git `user.name` and `user.email`.
- **Defaults:** provider- or namespace-associated choices such as visibility.
- **Project:** a provider-hosted or local Git repository managed by Colt.
- **Workspace:** a declared set of desired projects and the local root where Colt reconciles them.
- **Credential:** a provider API credential used by Colt for hosting-provider API operations; distinct from Git transport credentials and from Git commit identity.
- **Credential source:** where a credential comes from, resolved deterministically: explicitly configured `token_env`, conventional provider environment variable, or persisted Colt credential (secure store or explicitly enabled plaintext fallback).
- **Git transport credential:** material used by native Git for clone/fetch/push over SSH or HTTPS; HTTPS transport resolves through the Colt Git credential helper from the Colt credential subsystem, while Colt-invoked operations MAY additionally use an ephemeral process-scoped mechanism.

Provider account, namespace, Git identity, and Git transport identity are separate concepts:

``` text
Provider account != namespace != Git identity != Git transport identity
```

And at the authentication-layer level:

``` text
Provider API authentication
    !=
Git transport authentication
    !=
Git commit identity
```

Git identity (`user.name`/`user.email`) identifies commit authorship only. It is NOT a provider credential and NOT a Git transport credential. It is not secret and MUST be configured repository-local (`git config --local`); Colt MUST NOT modify global Git identity unless an explicit future command requests it.

| ID                   | Requirement                                                                                                                                                                                                                                                  | Verification                             |
|:---------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-----------------------------------------|
| `CORE-ARCH-001`      | Hosting operations **MUST** use direct supported-provider HTTP APIs (currently GitHub and GitLab; Gitea and Forgejo are planned future adapters) and **MUST NOT** invoke provider-specific CLIs (such as `gh` or `glab`) or `curl`.                        | Architecture review.                     |
| `CORE-ARCH-002`      | Native `git` **MUST** be the sole external executable.                                                                                                                                                                                                       | Architecture review.                     |
| `CORE-MODEL-001`     | Provider, account, namespace, identity, and defaults **MUST** remain logically separate concepts; the design **MUST NOT** assume a one-to-one relationship among them. The MVP **SHOULD** avoid separate configuration objects until behavior requires them. | Architecture review.                     |
| `CORE-NAMESPACE-001` | Common commands, output, and models **MUST** use `namespace`; provider-native vocabulary **SHOULD** remain inside provider integrations.                                                                                                                     | Specification and implementation review. |

## Provider Configuration And Authentication

The active MVP provides provider setup and status through:

``` text
colt auth login <github|gitlab> <alias> [--replace]
colt auth status [--offline]
colt auth logout <alias> [--revoke]
```

`<github|gitlab>` enumerates the currently supported provider types. Future
types (`gitea`, `forgejo`) extend this position when their adapters land; the
 surrounding command shape MUST NOT change to accommodate them.

Configuration **MUST** use `os.UserConfigDir()` with `colt/config.yaml` appended; on Linux this is `$XDG_CONFIG_HOME/colt/config.yaml` when `XDG_CONFIG_HOME` is set, otherwise `~/.config/colt/config.yaml`. `COLT_CONFIG` **MUST** override the complete path. Storage shape is intentionally unspecified.

| ID                  | Requirement                                                                                                                                                                                                                                                                                                                                                                                                                                          | Verification                                                                                                                             |
|:--------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-----------------------------------------------------------------------------------------------------------------------------------------|
| `CORE-PROVIDER-001` | Colt **MUST** configure providers by unique alias, type, host, namespace, default visibility, identity name, identity email, and credential source (interactive secure storage or environment variable). The currently supported types are GitHub (github.com) and GitLab (GitLab.com and self-hosted); Gitea and Forgejo are planned future types (see `CORE-PROVIDER-010`). | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-002` | `colt auth login` **MUST** validate credentials directly against the configured host and identify the authenticated account before reporting success. An existing alias **MUST** require explicit `--replace`, and replacement **MUST** preserve the prior configuration on validation or save failure.                                                                                                                                              | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-003` | Self-hosted GitLab **MUST** use its configured base URL for authentication and later provider operations. The same host-first principle applies to every self-hosted provider type: a provider type MUST NOT determine its API hostname (see `CORE-PROVIDER-010`).                                                                                                                                 | [`provider_configuration.feature`](../../features/provider_configuration.feature)                                                        |
| `CORE-PROVIDER-004` | Provider API endpoints **MUST** use HTTPS, and Colt **MUST NOT** forward credentials across a redirect to another host.                                                                                                                                                                                                                                                                                                                              | Security integration test.                                                                                                               |
| `CORE-PROVIDER-005` | `colt auth status` **MUST** show configured providers deterministically by alias. By default it **MUST** perform a lightweight read-only provider API check using the resolved credential and report the authenticated account and connection state. `--offline` **MUST** inspect configuration only and **MUST NOT** read environment, secure-store, or credential-file secret values, nor invoke provider or Git operations. Neither mode may mutate provider, Git, or configuration state. | [`provider_configuration.feature`](../../features/provider_configuration.feature) and authentication output in [shared core](00-core.md) |
| `CORE-PROVIDER-006` | `colt auth login github <alias>` SHOULD offer a native interactive GitHub browser/device authorization flow: display the authorization URL and user code, wait for user approval, identify the authenticated account, and persist the received credential via the credential subsystem. This optional SHOULD-level flow is not required for M1 completion and is currently `@unimplemented`. If implemented, it MUST communicate directly with GitHub HTTP APIs and MUST NOT invoke `gh`, `glab`, `curl`, or another provider-specific CLI. The authorization/device code is not a reusable credential and MAY be displayed. The exact authorization application model, registration details, scopes, token lifetime, and refresh behavior SHOULD remain implementation-specific unless they affect observable behavior. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-PROVIDER-007` | Provider-specific authentication implementations MUST remain behind the provider abstraction. The common contract covers authentication, credential persistence, credential retrieval, account identification, failure classification, and secret handling. Provider-specific protocol details (for example GitHub device flow vs GitLab token mechanism) MUST remain inside provider integrations; providers NEED NOT expose identical protocols. Gitea and Forgejo authorization mechanisms are planned and MUST be designed inside their adapters when those adapters are specified. | Architecture review. |
| `CORE-PROVIDER-008` | `colt auth logout <alias>` MUST remove only the Colt-owned locally persisted credential for the exact credential ID of that alias, from both the secure store and the plaintext fallback when present. It MUST preserve provider configuration, environment variables, shell profiles, `~/.ssh`, SSH keys, `ssh-agent` state, Git identity, and unrelated Colt credentials. It MUST NOT require provider connectivity and MUST work offline, with an invalid credential, or when the provider API is unreachable. If an environment credential still resolves after logout, output SHOULD say so without revealing the value (for example `! GITHUB_TOKEN is still available`). | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-PROVIDER-009` | `colt auth logout <alias> --revoke` MUST attempt provider-side revocation and then remove the local Colt-owned credential, reporting each outcome independently (for example `! remote revocation failed` + `✓ local credential removed`). Remote revocation is provider-dependent and MAY report supported, unsupported, or failed; ordinary logout without `--revoke` MUST NOT attempt remote revocation. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |

### Supported Provider Types And Provider Identity

``` text
Colt
  |
  +-- Provider API abstraction
  |     +-- GitHub
  |     +-- GitLab
  |     +-- Gitea       [planned]
  |     +-- Forgejo     [planned]
  |
  +-- native Git
```

| ID                  | Requirement                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Verification              |
|:--------------------|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:--------------------------|
| `CORE-PROVIDER-010` | Colt MUST treat `github`, `gitlab`, `gitea`, and `forgejo` as distinct provider types. GitHub and GitLab are the currently supported types; Gitea and Forgejo are planned future adapters and MUST NOT be presented as implemented. Forgejo MUST NOT be modeled as an alias for Gitea: each type MUST remain an independently identifiable adapter even where their present-day APIs resemble each other. Provider identity is conceptually `provider type + host + alias`, never provider type alone: a type MUST NOT determine its API hostname, so multiple independent installations of one type (for example several Gitea or Forgejo hosts such as `code.example.invalid`, `git.company.example`, `forge.example.invalid`) MUST be configurable side by side. Common code MUST select behavior per configured provider (adapter dispatch by type, authority checks by host) and MUST NOT assume `github.com` / `gitlab.com` are the only possible hosts. | Architecture review.      |
| `CORE-PROVIDER-011` | The common provider abstraction MUST contain only capabilities Colt actually requires, stated generically: authenticated account, repository lookup, repository creation, repository listing, namespace listing, HTTPS clone target, SSH clone target, and releases. A provider type MAY support, omit, or implement a capability differently; adapters MUST NOT be forced to expose identical concepts where their APIs differ. Colt MUST obtain HTTPS/SSH clone targets from provider metadata and MUST NOT construct repository URLs from assumed hostname or path patterns. | Architecture review.      |

Human-readable authentication output, failure categories, and the distinction between provider API connectivity and repository Git connectivity are specified in authentication output in [shared core](00-core.md).

An illustrative environment configuration uses the normative `auth.source` schema:

``` yaml
providers:
  work:
    type: gitlab
    default: true
    host: gitlab.company.example
    namespace: infrastructure
    auth:
      source: env
      token_env: GITLAB_TOKEN
```

The schema describes credential resolution, not the provider authorization protocol. The term `source` is normative; `method` MUST NOT be used for this field because it is confused with protocol concepts (OAuth, device flow, PAT, bearer).

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
ls-remote or equivalent read-only access checks
```

A successful provider API authentication check MUST NOT imply that Git clone or push access has been validated.

Likewise, working SSH access to a Git repository MUST NOT imply that Colt has valid provider API credentials.

## Authentication Output

Default authentication output is intended for humans rather than scripts. It **MUST** remain concise, provider-independent, actionable on failure, and safe for credentials.

Human-readable provider names **SHOULD** use the platform's own name (`GitHub`, `GitLab`, and later `Gitea`, `Forgejo` for their adapters). Compact state markers **SHOULD** use:

``` text
✓ success
! warning
✗ failure
```

Authenticated account, configured namespace, and Git identity are distinct concepts and **MUST NOT** be treated as interchangeable.

### `auth login`

A successful interactive login **SHOULD** use the authorization prompt followed by one concise line (GitHub adapter example; other adapters define their own mechanism behind `CORE-PROVIDER-007`):

``` text
Authorize Colt with GitHub.

Open: https://github.com/login/device
Code: ABCD-EFGH

Waiting for authorization...

✓ personal · GitHub · example-user · default
```

Reusable credentials (access tokens, refresh tokens, PATs, passwords, authorization headers, store/file values, token fragments) MUST NOT be displayed. The authorization/device code is not a reusable credential and MAY be displayed when the provider protocol requires it. Exact wording and whether a browser is opened automatically are implementation details.

The host **SHOULD** be shown for self-hosted providers and **MAY** be omitted when the standard host is obvious.

Failures **MUST** identify the failed layer and **SHOULD** provide one safe, actionable reason when available. Human-facing categories **SHOULD** distinguish configuration failure, missing credentials, authentication failure, authorization failure, connection failure, provider unavailability, namespace inaccessibility, and credential storage failure. Provider messages **MAY** be included only after sanitization and bounding. A persisted credential rejected by the provider SHOULD advise re-login (for example `Run: colt auth login github personal`); a transient network failure MUST NOT delete or replace the persisted credential.

### `auth status`

Providers **MUST** be displayed deterministically by alias as compact sections. The default command performs the read-only provider API check defined by `CORE-PROVIDER-005`.

``` text
personal (default)
  GitHub · github.com
  Account:     example-user
  Namespace:   example-user
  Credential:  stored
  Git name:    Example User
  Git email:   user@example.invalid
  Connection:  ✓ connected
```

Live status MAY report the resolved credential source as `Credential: stored` or `Credential: environment` without revealing secrets (no token value, prefix, store secret, authorization header, or sensitive internal identifier).

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

`colt auth status --offline` **MUST** inspect configuration only. It **MUST NOT** read environment credential values, secure-store values, or plaintext-file secret values; **MUST NOT** contact provider APIs or Git remotes; and **MUST NOT** perform SSH or HTTPS Git authentication. It MAY report the configured non-secret `auth.source` and `credential_id` without resolving their secrets, and **MUST NOT** claim a stored credential exists or is valid when checking would require backend access:

``` text
Connection:  not checked
```

Example offline section:

``` text
personal (default)
  GitHub · github.com
  Namespace:      example-user
  Auth source:    stored
  Credential ID:  github.com/personal
  Connection:     not checked
```

If `colt auth status` runs inside a Colt-managed Git repository with a concrete `origin`, Colt **MAY** perform an additional read-only Git access check:

``` text
Git access:  ✓ origin reachable
```

and MAY extend it concisely with transport detail without exposing secrets:

``` text
Repository
  Transport:      HTTPS
  Credential:     Colt helper
  Git access:     ✓ origin reachable
```

or:

``` text
Repository
  Transport:      SSH
  Git access:     ✓ origin reachable
```

This check **MUST** remain distinct from provider API connectivity and **MUST NOT** push, mutate remotes, or modify repository state. It **MUST NOT** run when there is no concrete repository remote to validate. Git access MUST NOT be claimed merely because provider API authentication succeeds, and tokens or SSH private-key material MUST NOT be exposed.

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
| `CORE-CREDENTIAL-001` | Colt MUST resolve provider API credentials deterministically in this order: (1) explicitly configured `token_env`; (2) the conventional variable defined by the provider adapter (`GITHUB_TOKEN` for GitHub, `GITLAB_TOKEN` for GitLab; future adapters define their own); (3) persisted Colt credential (secure store, then explicitly enabled plaintext fallback); (4) otherwise fail with `credentials missing`. Selecting an environment credential for one invocation MUST NOT overwrite the persisted interactive credential. Environment credentials MUST NOT be copied into `config.yaml`, the secure store, the plaintext file, or Git configuration; they remain owned by the environment. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-002` | Reusable secret values MUST NOT appear in `config.yaml`, project files, remote URLs, stdout, stderr, normal/verbose/error output, diagnostics, or provider error dumps, and MUST be redacted from all such output. Interactive credentials in the secure store or plaintext fallback MUST NOT appear in any output either. | Security constraint and [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-003` | A missing or empty selected credential source MUST fail before an authenticated request or project mutation. For `colt auth status`, this is reported as a per-provider `credentials missing` connection state rather than as a successful connection. Stored-credential presence MUST NOT be reported as `✓ connected`; only a live provider check establishes that state. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-004` | Normal Colt configuration MUST contain only non-secret metadata and credential references. It MUST NOT contain reusable secret values in any field equivalent to `token`, `access_token`, `refresh_token`, `password`, or `secret`, regardless of provider. The normative reference model is `auth.source` (`env` or `stored`) plus either `token_env` (environment variable name, no secret) or `credential_id` (stable non-secret persisted-credential identifier). | Security review. |
| `CORE-CREDENTIAL-005` | A `stored` source MUST resolve its `credential_id` through the Colt persistent credential subsystem: preferred secure OS credential facility, else the explicitly approved plaintext fallback, without changing provider configuration when moving between backends. The public config MUST NOT encode backend choice (`keychain`, `secret-service`, `credential-manager`, `file`). Colt MUST NOT silently migrate a credential from secure storage to plaintext. `config.yaml` stores only the reference (for example `auth.source: stored`, `credential_id: github.com/personal`). If secure storage is unavailable, Colt MUST NOT silently write plaintext: it MUST offer an explicit choice (plaintext file, environment variable, or cancel) and MUST NOT report login as persistently successful unless the credential was persisted as requested. Credential-storage failure is a distinct failure layer. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-006` | The explicit plaintext fallback MUST use a separate internal versioned credential file (for example `~/.config/colt/credentials` with `version: 1`), never `config.yaml`. The file is internal Colt storage, not user configuration, and is not expected to be manually edited; serialization MAY evolve with safe deterministic migration. The file MUST be user-readable-only (0600 equivalent on Unix) and use deterministic parsing. A file with unsafe permissions MUST be rejected, not silently read. The user MUST be warned it is plaintext protected only by filesystem permissions, which provide no encryption. The file MUST never be committed, included in diagnostics, printed, or copied into `config.yaml`. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-CREDENTIAL-007` | Provider implementations MUST operate on already-resolved credentials and MUST NOT access OS credential stores or the credential file directly; the credential subsystem MUST NOT contain provider API behavior. All sources resolve to one conceptual `Credential` (`kind`, `secret`, `source`); the resolved `source` MAY be tracked for diagnostics. Credential IDs MUST be stable, deterministic, non-secret (`<provider-host>/<provider-alias>`, for example `github.com/personal`), MUST NOT contain secret material, and MAY appear in `config.yaml` and diagnostics. Deletion MUST target the exact credential ID so `personal` never deletes `github.com/work` or `gitlab.com/personal`. Replacing provider configuration (`--replace`) MUST NOT silently orphan, overwrite, copy, or migrate an unrelated stored credential. | Architecture review and [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-IDENTITY-001`   | Colt MUST apply the selected identity with repository-local Git configuration before creating a commit and MUST NOT change global Git identity.                                                                                                             | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature)             |
| `CORE-GIT-001`        | Colt MUST report an actionable error before mutation if native `git` is unavailable for a Git operation.                                                                                                                                                        | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature)             |

## Secure Credential Persistence

Normal Colt configuration MUST contain provider configuration and credential references, but MUST NOT contain reusable secret values. The normative schema is:

``` yaml
providers:
  personal:
    type: github
    host: github.com
    namespace: example-user
    auth:
      source: stored
      credential_id: github.com/personal
```

or:

``` yaml
providers:
  ci:
    type: github
    host: github.com
    auth:
      source: env
      token_env: GITHUB_TOKEN
```


but MUST NOT contain:

``` yaml
token: ghp_...
access_token: ...
refresh_token: ...
password: ...
secret: ...
```

`auth.source` is `env` (credential resolved from an environment variable) or `stored` (credential resolved from Colt-owned persistent storage). `credential_id` identifies the persisted credential, contains no secret material, and follows the deterministic form `<provider-host>/<provider-alias>` (for example `github.com/personal`, `gitlab.com/work`). `token_env` names the variable and contains no secret. A `stored` reference stays valid whether the credential physically lives in the OS secure store or the explicitly approved fallback file, so backend choice is never encoded in provider config. Conceptually:

``` text
CredentialResolver
    |
    +-- EnvCredentialSource
    |
    +-- SecureStoreCredentialSource
    |
    +-- FileCredentialSource
```

resolving to one `Credential` (`kind`, `secret`, `source`) consumed by `Provider.Authenticate(...)`. Exact type names are non-normative.

The credential identifier MUST NOT itself contain secret material. Interactive credentials SHOULD be stored using the operating system's secure credential facility (conceptually service `colt`, credential-id `github.com/personal`); the specification describes this security property rather than prescribing a platform backend or library. Backend preference is secure store first, then explicitly approved plaintext fallback; Colt MUST NOT silently migrate secure credentials to plaintext.

If secure persistent credential storage is unavailable, Colt MUST NOT silently write plaintext. It SHOULD offer an explicit choice among the plaintext credential file, environment-variable usage, or cancel, with a warning that file storage is plaintext protected only by filesystem permissions. Colt MUST NOT report login as persistently successful when authorization succeeded but persistence failed.

Even when explicitly enabled, plaintext secrets MUST live in a separate internal versioned credential file (for example `~/.config/colt/credentials`), never in `config.yaml`. Conceptually:

``` yaml
version: 1

credentials:
  github.com/personal:
    type: bearer_token
    value: <secret>
```

The serialization is illustrative; normative properties are versioning, deterministic parsing, isolation, 0600-equivalent user-only permissions, rejection of unsafe permissions, and no encryption claim. The file MUST be user-readable-only, rejected (not silently read) when permissions are unsafe, never committed, never included in diagnostics, never printed, and never copied into `config.yaml`.

## Logout And Revocation

`colt auth logout <alias>` removes the Colt-owned locally persisted credential for that alias's exact credential ID; provider configuration, environment, SSH, and Git identity remain. After logout, `colt auth status` either uses a higher-priority environment credential or reports `credentials missing`; re-login recreates the stored credential without reconfiguring the alias. Ordinary logout needs no provider connectivity. `colt auth logout <alias> --revoke` additionally attempts provider-side revocation first (supported, unsupported, or failed per provider) and reports local and remote outcomes independently per `CORE-FAILURE-001`, never hiding partial failure. Local credential removal is `!=` remote provider revocation.

Credential ownership is fixed:

``` text
Interactive provider credential -> owner: Colt, persistence: Colt credential backend
Environment provider credential -> owner: environment/user/CI, no automatic persistence by Colt
SSH private key                 -> owner: user/OS, outside Colt
Git identity                    -> repository/user configuration, not a secret
```

Colt MUST NOT migrate credentials between these domains. Environment credentials MUST NOT be copied into the store, the file, `config.yaml`, or Git configuration. A persisted credential that the provider rejects (expired/revoked/insufficient) MUST produce an actionable authentication failure advising re-login; transient network errors MUST NOT delete or replace it.

## Git Repository Authentication

Colt MUST NOT treat provider API credentials, Git transport credentials, and Git identity as one conceptual credential. Native Git remains responsible for Git repository mechanics. A Colt-managed repository MUST remain usable through ordinary native Git commands (`git fetch`, `git pull`, `git push`) without requiring the user to re-enter provider credentials on every invocation or to replace them with Colt-prefixed commands (for example Colt MUST NOT require `colt git push`), provided the corresponding credential remains valid and available.

``` text
                   Colt
                    |
          +---------+---------+
          |                   |
     Provider API          Git operation
          |                   |
       HTTP API             native git
          |                   |
 provider credential    +------+------+
                        |             |
                       SSH          HTTPS
                                      |
                          Colt credential helper
                                      |
                          Colt credential subsystem
```

Provider repository metadata conceptually exposes authoritative HTTPS and SSH clone targets; Colt MAY select either according to configuration and available authentication, provided the selected target still matches the expected provider, host, namespace, and repository. Existing protections against authority confusion, unexpected hosts, credential forwarding, malicious redirects, and repository mismatch MUST NOT be weakened to support either transport. A provider credential MAY be valid for both the provider HTTP API and Git HTTPS transport, but the two roles remain architecturally distinct: permissions may differ, and failures MUST be attributable to the correct layer.

| ID | Requirement | Verification |
|:---|:---|:---|
| `CORE-GIT-002` | A Colt-managed repository MUST be usable with ordinary native Git commands after setup without re-entering provider credentials on every invocation while the credential remains valid and available. Colt MUST NOT require `colt git push` / `colt git pull` replacements. | Architecture review and [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-GIT-003` | The selected Git transport (HTTPS or SSH) MUST correspond to an authoritative clone/push target returned or validated for the selected provider repository. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-GIT-004` | Colt MUST support HTTPS and SSH as first-class Git transports with the deterministic preference defined by `CORE-GIT-009`; the default transport MUST be HTTPS with the Colt credential helper unless the product direction explicitly changes it. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `CORE-GIT-005` | The Colt Git credential helper MUST implement the standard Git credential-helper contract (`get`, `store`, `erase` over Git's stdin/stdout credential protocol with fields such as `protocol`, `host`, `path`, `username`) and MUST NOT invent a custom credential protocol. The preferred repository-local configuration is the named-helper form `helper = colt` (resolving to an installed `git-credential-colt` executable or equivalent dispatch through the Colt binary); the shell-snippet form `helper = !colt git-credential` is an accepted equivalent when the named form is impractical, with quoting and platform portability tested. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-GIT-006` | The credential helper MUST reuse the Colt credential subsystem (`CredentialResolver`: explicit `token_env`, conventional provider variable, persisted Colt credential, same deterministic precedence per `CORE-CREDENTIAL-001`) and MUST NOT create an independent credential database. `get` MUST parse the request, resolve the provider safely, resolve the selected Colt credential, return only the required username/password fields, and write no unrelated output to stdout; human-readable diagnostics go to stderr without secrets. With no matching credential the helper returns no credential and follows normal Git fallback semantics. The reusable secret exists only in process memory and the configured credential backend during the interaction and MUST NOT be written into the remote URL. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-GIT-007` | The helper MUST only return a credential when the request matches a configured Colt provider (validating at minimum `protocol`, `host`, configured provider, and repository context when available); a credential for one host MUST NOT be returned for an unrelated or redirected authority. Existing cross-host forwarding protections (`CORE-PROVIDER-004` and clone/release authority checks) continue to apply. For GitHub HTTPS the helper returns a safe account username when known plus the token as the password field; other providers MAY use their own username/token convention. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-GIT-008` | Colt MUST configure the helper repository-local (`git config --local`) for Colt-managed repositories, MUST NOT silently overwrite unrelated local helper configuration (conflicts handled deterministically and non-destructively), and MUST NOT modify `~/.gitconfig` unless the user explicitly requests a global integration mode. `.git/config` MUST NOT contain reusable secrets: no tokens, passwords, or credential-bearing URLs (never `https://TOKEN@host/...`); the helper reference is non-secret. `store` MUST accept the protocol invocation without automatically persisting unknown Git-supplied credentials; `erase` MUST NOT silently delete a Colt persisted credential (never equivalent to `colt auth logout`). | [`provider_configuration.feature`](../../features/provider_configuration.feature) |
| `CORE-GIT-009` | Git transport preference MAY exist globally, per provider, or both (for example `git.transport: https` or `ssh`, adapted to existing configuration conventions), with deterministic precedence: explicit command option > provider transport preference > global transport preference > product default (HTTPS). An unsupported transport MUST fail before repository mutation. Colt MUST NOT invent command-line flags inconsistent with the existing CLI design. | [`project_lifecycle.feature`](../../features/project_lifecycle.feature) |
| `CORE-GIT-010` | SSH transport uses the user's existing SSH infrastructure: private keys remain owned by the user/OS SSH subsystem and MUST NOT be stored in the Colt credential subsystem. For the initial implementation Colt MUST NOT generate key pairs, modify private keys, manage `ssh-agent`, or invoke `ssh-keygen`/`ssh-add`. Colt MAY rely on user-managed `~/.ssh/config` (optional, not mandatory; only required for non-default keys, multiple identities, custom hosts/ports, or enterprise hosts) and MAY perform a read-only native-Git remote query (not `ssh -T`) to validate access when requested or during setup. For GitHub-style SSH the SSH user is `git` (not the account name); the account is determined by the accepted public key, and the repository owner stays in the path (`git@github.com:example-user/example-project.git`). Multiple accounts on one host use SSH host aliases (for example `git@github-personal:example-user/project.git`); the alias is a local SSH name, not a provider authority, and security checks MUST resolve it to the expected provider/repository without confusing alias with hostname. | [`provider_configuration.feature`](../../features/provider_configuration.feature) |

### Native Git outside Colt commands

After Colt has initialized or cloned a repository, `git fetch` / `git pull` / `git push` SHOULD authenticate without invoking Colt explicitly: for HTTPS, native Git invokes the repository-local Colt credential helper, which resolves the provider and returns the stored credential to the Git process; for SSH, native Git uses the user's SSH configuration, agent, or private key whose public counterpart is registered with the provider account. Provider API authentication remains required for remote creation regardless of transport.

### SSH

Colt SHOULD support repositories whose authoritative provider metadata exposes an SSH clone/push URL. Colt MUST NOT require ownership of the user's SSH private keys. Existing SSH configuration, keys, agents, and platform facilities may be used by native Git. A managed SSH repository contains, for example:

``` ini
[remote "origin"]
    url = git@github.com:example-user/example-project.git
    fetch = +refs/heads/*:refs/remotes/origin/*
```

with optional illustrative user-managed SSH configuration (not required when the existing SSH environment already works):

``` text
Host github.com
    HostName github.com
    User git
    IdentityFile ~/.ssh/id_ed25519
```

For the initial implementation, Colt SHOULD NOT generate SSH keys, modify SSH configuration, install keys, or manage `ssh-agent`. This preserves the architectural rule that native `git` is Colt's only required external executable. If SSH key management is introduced later, it must be specified separately because tools such as `ssh-keygen` or `ssh-agent` would change the external-executable boundary. `colt auth logout` MUST NOT alter SSH access (`~/.ssh`, `ssh-agent`, provider-registered public keys, SSH config, SSH remote URL); API logout with working SSH push still working is expected.

### HTTPS

For HTTPS Git operations Colt integrates through the standard Git credential-helper protocol so native Git can resolve credentials independently. A managed HTTPS repository contains, for example:

``` ini
[remote "origin"]
    url = https://github.com/example-user/example-project.git
    fetch = +refs/heads/*:refs/remotes/origin/*

[branch "main"]
    remote = origin
    merge = refs/heads/main

[credential]
    helper = colt

[user]
    name = Example User
    email = user@example.invalid
```

Conceptually (`get`):

``` text
Git -> credential get (protocol=https, host=github.com, path=...) -> Colt helper
  -> repository/provider resolution -> credential_id -> CredentialResolver
  -> stored credential -> username/password response to Git
```

In addition, provider credentials MAY be supplied to Colt-invoked native Git using an ephemeral/process-scoped mechanism (for example the initial clone or push performed by Colt itself). Credentials MUST NOT be embedded persistently in origin URLs (never leave `https://TOKEN@host/...`), `.git/config`, workspace manifests, Colt configuration, or command output. The persistent remote URL must remain credential-free. `colt auth logout <alias>` removes the persisted credential but MUST NOT remove remotes or unrelated Git configuration; it MAY warn that Colt-managed HTTPS repositories relying on the removed credential may require reauthentication, and a subsequent `git push` may fail or fall through to another Git credential source per normal Git behavior.

### Architecture

``` text
                          Colt CLI
                             |
           +-----------------+------------------+
           |                 |                  |
         Config           Git Ops          Provider Ops
           |                 |                  |
           |              native git         HTTP APIs
           |                 |                  |
           |          +------+-------+     +----+----+- - - - -+
           |          |              |     |         |         |
           |         SSH           HTTPS GitHub   GitLab    Gitea/Forgejo
           |                                               [planned]
           |
     Credential Resolver
           |
      +----+-------------------+
      |                        |
 environment credentials   persisted credentials
                               |
                      +--------+---------+
                      |                  |
                  secure store     plaintext fallback
```

Central rule: provider operations use Colt HTTP implementations; repository mechanics use native Git; credential persistence uses the Colt credential subsystem; SSH key management stays external to Colt.

## Safety, Conflicts, And Partial Failure

These rules are authoritative for implemented and planned capabilities unless a capability adds a stricter rule.

| ID                  | Requirement                                                                                                                                                                                                     | Verification                                                                                  |
|:--------------------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------|
| `CORE-SAFETY-001`   | Colt operations **MUST** be non-destructive by default: Colt **MUST NOT** overwrite a non-empty destination, replace unrelated state, discard changes, or delete local or remote state as automatic rollback.   | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-CONFLICT-001` | Colt **SHOULD** perform inexpensive preflight checks, but provider mutations **MUST** remain authoritative and Colt **MUST** safely handle a provider conflict even when an earlier existence check found none. | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |
| `CORE-FAILURE-001`  | On partial failure Colt **MUST** stop, preserve completed work, report local and remote state and the failed step, provide a safe recovery action, and return failure.                                          | [`blank_project_initialization.feature`](../../features/blank_project_initialization.feature) |

Errors **SHOULD** distinguish invalid input, missing credentials, authentication, authorization, connectivity, conflict, and partial completion. Output **SHOULD** be concise and identify affected resources without exposing credentials.
