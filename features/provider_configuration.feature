Feature: Provider configuration and authentication
  Provider API authentication, Git transport, and Git identity stay separate. Persistent storage and stored-credential logout remain unimplemented; the plaintext FileStore is not enabled in production.

  @unimplemented @CORE-PROVIDER-006 @CORE-PROVIDER-002 @CORE-CREDENTIAL-002
  Scenario: Interactive GitHub authentication succeeds
    Given no persisted credential exists for provider "personal"
    And the provider authorization flow reports approval for account "octocat"
    When I run `colt auth login github personal`
    Then Colt displays the authorization URL and user code
    And Colt identifies the authenticated account as "octocat"
    And the command succeeds with "personal · GitHub · octocat"
    And no reusable credential value appears in output

  @unimplemented @CORE-PROVIDER-006
  Scenario: Interactive authorization is rejected
    Given the provider authorization flow reports rejection by the user
    When I run `colt auth login github personal`
    Then the command fails with an authorization error
    And no credential is persisted
    And no provider configuration is changed

  @unimplemented @CORE-PROVIDER-006
  Scenario: Interactive authorization expires or times out
    Given the provider authorization flow reports expiry before approval
    When I run `colt auth login github personal`
    Then the command fails with an authorization error
    And no credential is persisted

  @CORE-PROVIDER-002
  Scenario: Authenticated provider account is identified
    Given provider "personal" has auth source "env" with token_env "GITHUB_TOKEN"
    And "GITHUB_TOKEN" contains "bdd-fake-secret"
    When Colt authenticates provider "personal"
    Then the command succeeds
    And output identifies the authenticated account as "example-user"

  @CORE-CREDENTIAL-004
  Scenario: Stored authentication uses a non-secret credential identifier
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    Then config.yaml contains "credential_id: github.com/personal"
    And config.yaml contains no reusable credential value

  @CORE-CREDENTIAL-004
  Scenario: Environment authentication references a token environment variable
    Given provider "ci" has auth source "env" with token_env "GITHUB_TOKEN"
    Then config.yaml contains "source: env"
    And config.yaml contains "token_env: GITHUB_TOKEN"
    And config.yaml contains no reusable credential value

  @CORE-CREDENTIAL-004 @CORE-CREDENTIAL-002
  Scenario Outline: Reusable credential values are rejected from normal configuration
    Given config.yaml declares "<field>" with value "fake-secret-9"
    When Colt loads the configuration
    Then the load fails with a configuration error
    And no authentication is attempted

    Examples:
      | field         |
      | token         |
      | access_token  |
      | refresh_token |
      | password      |
      | secret        |

  @CORE-PROVIDER-002
  Scenario: Authentication failure preserves prior configuration
    Given provider "work" has an existing valid configuration
    And its referenced credential is invalid
    When I run `colt auth login gitlab work --replace` with the new settings
    Then the command fails with an authentication error
    And the prior provider configuration is unchanged

  @CORE-PROVIDER-001
  Scenario: Provider aliases are unique
    Given provider "work" is already configured
    When I try to log in to another provider named "work"
    Then the command fails with a duplicate alias error
    And the existing provider configuration is unchanged

  @CORE-PROVIDER-003
  Scenario: Self-hosted GitLab uses its configured base URL
    Given a GitLab provider uses host "gitlab.company.example"
    When Colt authenticates the provider
    Then the request uses the configured self-hosted GitLab base URL
    And no request is sent to GitLab.com

  @unimplemented @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-004 @CORE-CREDENTIAL-007
  Scenario: Stored credential resolves from secure credential backend
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And the secure credential backend holds "github.com/personal"
    When Colt authenticates provider "personal"
    Then the provider receives the resolved credential without knowing its backend type
    And config.yaml contains no reusable credential value

  @unimplemented @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-007
  Scenario: Stored credential resolves from an injected plaintext credential subsystem
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And a plaintext file credential store is explicitly injected with "github.com/personal"
    When Colt authenticates provider "personal"
    Then the provider receives the resolved credential without knowing its backend type
    And provider configuration is unchanged

  @unimplemented @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-002
  Scenario: Plaintext credential serialization is versioned
    Given the plaintext fallback file declares "version: 1"
    And it holds credential_id "github.com/personal"
    When Colt parses the credential file
    Then repeated parsing returns the same credential
    And config.yaml remains free of reusable credential values

  @unimplemented @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-004
  Scenario: Interactive credential is stored in the secure credential backend
    Given the secure credential backend is available
    And the provider authorization flow reports approval
    When I run `colt auth login github personal`
    Then the reusable credential is stored under a non-secret credential id
    And normal Colt configuration does not contain the reusable credential value

  @unimplemented @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-004
  Scenario: Persisted credential is reused by a later Colt process
    Given provider "personal" has a persisted secure credential
    When a new Colt process authenticates provider "personal" without re-login
    Then authentication succeeds without repeating the authorization flow
    And normal Colt configuration still does not contain the reusable credential value

  @unimplemented @CORE-CREDENTIAL-005
  Scenario: Secure credential storage is unavailable
    Given the secure credential backend is unavailable
    And the provider authorization flow reports approval
    When I run `colt auth login github personal`
    Then Colt offers plaintext file persistence, environment-variable usage, or cancel
    And the command is not reported as persistently successful until a choice is persisted

  @unimplemented @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-002
  Scenario: Colt does not silently fall back to plaintext storage
    Given the secure credential backend is unavailable
    And the user has not consented to plaintext storage
    When interactive authentication succeeds
    Then no reusable credential is written to any file
    And output warns that plaintext storage requires explicit consent

  @unimplemented @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-002
  Scenario: User explicitly accepts plaintext credential persistence
    Given the secure credential backend is unavailable
    And the user accepts plaintext credential persistence
    When interactive authentication succeeds
    Then the credential is stored in the separate credential file, not in config.yaml
    And the credential file has user-readable-only permissions
    And output warns the file is plaintext protected only by filesystem permissions

  @unimplemented @CORE-CREDENTIAL-006
  Scenario: User rejects plaintext credential persistence
    Given the secure credential backend is unavailable
    And the user rejects plaintext credential persistence
    When interactive authentication succeeds
    Then no reusable credential is written to any file
    And the command reports that authentication was not persisted

  @unimplemented @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-002
  Scenario Outline: Unsafe credential file permissions are rejected safely
    Given the plaintext credential file <condition>
    When Colt resolves the persisted credential
    Then Colt fails safely without exposing the secret value
    And normal, verbose, and error output do not contain "plaintext-fake-secret-1"

    Examples:
      | condition                  |
      | is group-readable          |
      | is world-readable          |

  @unimplemented @CORE-CREDENTIAL-002
  Scenario: Credential file contents are never printed
    Given the plaintext credential file contains "plaintext-fake-secret-1"
    When any auth command runs with verbose output enabled
    Then normal, verbose, error, and diagnostic output do not contain "plaintext-fake-secret-1"
    And config.yaml does not contain "plaintext-fake-secret-1"

  @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002
  Scenario: Resolve a token by environment reference and redact it
    Given provider "work" has auth source "env" with token_env "COMPANY_GL_TOKEN"
    And "COMPANY_GL_TOKEN" contains "secret-token-value"
    When provider authentication fails
    Then authentication used the value from "COMPANY_GL_TOKEN"
    And normal and error output do not contain "secret-token-value"
    And normal Colt configuration does not contain "secret-token-value"

  @CORE-CREDENTIAL-001
  Scenario Outline: Use the provider's conventional token environment variable
    Given a <type> provider has no explicit token_env and no persisted credential
    And environment variable "<token_env>" contains a valid token
    When Colt authenticates the provider
    Then authentication uses the value from "<token_env>"

    Examples:
      | type   | token_env    |
      | GitHub | GITHUB_TOKEN |
      | GitLab | GITLAB_TOKEN |

  @CORE-CREDENTIAL-001
  Scenario: Explicit environment credential overrides an injected stored credential
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "stored-fake-secret-1"
    And "GITHUB_TOKEN" contains a different temporary token "env-fake-secret-2"
    When Colt authenticates provider "personal"
    Then authentication uses the value from "GITHUB_TOKEN"
    And the injected stored credential is unchanged and was not read
    And output does not contain "env-fake-secret-2"

  @CORE-CREDENTIAL-001
  Scenario: Environment credential is not persisted
    Given provider "personal" has no persisted credential
    And "GITHUB_TOKEN" contains "env-fake-secret-3"
    When Colt authenticates provider "personal"
    Then authentication succeeds for this invocation
    And no Colt credential file is written
    And config.yaml does not contain "env-fake-secret-3"

  @CORE-CREDENTIAL-003
  Scenario Outline: Reject an unusable token environment variable
    Given provider "work" has auth source "env" with token_env "COMPANY_GL_TOKEN"
    And "COMPANY_GL_TOKEN" is <state>
    When Colt authenticates the provider
    Then the command fails before sending an authenticated request
    And no project or provider state is changed

    Examples:
      | state        |
      | unset        |
      | an empty value |

  @CORE-PROVIDER-005
  Scenario: Show provider status with live connection check by default
    Given provider "work" is configured with a valid credential
    When I run `colt auth status`
    Then the command succeeds
    And status shows the authenticated account and "connected"

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Rejected injected stored credential is not reported as connected
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "rejected-fake-secret"
    And the provider rejects authentication
    When I run `colt auth status`
    Then status does not show "connected"
    And status reports authentication failure without exposing "rejected-fake-secret"

  @CORE-PROVIDER-005
  Scenario: Provider API authentication success does not imply Git access
    Given provider API authentication succeeds for provider "work"
    When I run `colt auth status`
    Then "connected" means only the provider API check passed
    And no Git clone or push access is claimed

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-003
  Scenario: Show credentials missing state when no source resolves
    Given provider "work" has no environment credential and no persisted credential
    When I run `colt auth status`
    Then the command succeeds
    And status shows "credentials missing" for the provider

  @CORE-PROVIDER-004
  Scenario: Redirect to another host never receives credentials
    Given a provider is configured with a host that redirects to a different host
    And a valid credential is available
    When authentication is attempted against the provider
    Then the redirect target never receives the credentials
    And the command fails with a redirect refusal error

  @CORE-PROVIDER-005
  Scenario: Show status for an empty provider configuration
    Given no providers are configured
    When I run `colt auth status`
    Then the command succeeds with a clear no-providers result
    And no provider or native Git operation is invoked

  @CORE-PROVIDER-005
  Scenario: Show configured provider status offline in alias order
    Given providers "work" and "personal" are configured out of alias order
    And their credential environment variables are unset
    When I run `colt auth status --offline`
    Then the command succeeds
    And status shows providers deterministically in alias order as "personal" then "work"
    And each provider shows its alias, type, host, namespace, and default status
    And "Connection" is "not checked"

  @unimplemented @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Offline auth status reads no secret values and contacts nothing
    Given providers are configured with environment, secure-store, and plaintext credential sources
    When I run `colt auth status --offline`
    Then no environment credential value is read
    And no secure credential value is read
    And no plaintext credential value is read
    And no provider API or Git remote is contacted
    And output does not contain any reusable credential value

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Offline status does not resolve or expose an environment credential
    Given provider "work" has auth source "env" with token_env "COMPANY_GL_TOKEN"
    And "COMPANY_GL_TOKEN" contains "offline-env-secret"
    When I run `colt auth status --offline`
    Then the command succeeds
    And no provider or native Git operation is invoked
    And output does not contain "offline-env-secret"

  @unimplemented @CORE-PROVIDER-008
  Scenario: Logout removes persisted secure credential
    Given provider "personal" has a persisted secure credential
    When I run `colt auth logout personal`
    Then the secure credential for "personal" is removed
    And provider "personal" configuration is unchanged
    And unrelated credentials are unchanged

  @unimplemented @CORE-PROVIDER-008
  Scenario: Logout removes persisted plaintext credential
    Given provider "personal" has a persisted plaintext credential
    When I run `colt auth logout personal`
    Then the stored secret for "personal" is removed
    And provider "personal" configuration is unchanged

  @CORE-PROVIDER-008
  Scenario: Logout preserves environment credentials and SSH state
    Given provider "personal" has auth source "env" with token_env "GITHUB_TOKEN"
    And "GITHUB_TOKEN" contains "logout-env-secret"
    And an empty injected credential store is supplied
    And representative SSH configuration, key, and agent state exist
    When I run `colt auth logout personal`
    Then the command succeeds
    And the environment variable is unchanged
    And no SSH key, SSH configuration, or ssh-agent state is modified
    And global Git identity is unchanged
    And provider configuration is unchanged
    And no credential store operation is attempted
    And no provider or native Git operation is invoked
    And output explains the environment credential still resolves without revealing it

  @unimplemented @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Persisted credential rejected by provider produces actionable failure
    Given provider "personal" has a persisted credential the provider rejects
    When I run `colt auth status`
    Then the command reports an authentication failure advising re-login
    And output does not contain the rejected secret value

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-007
  Scenario: Network failure does not delete an injected stored credential
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "stored-network-secret"
    And the provider API is unreachable due to a network failure
    When I run `colt auth status`
    Then the command reports a connection failure
    And the injected stored credential is unchanged

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-003
  Scenario: Credential storage failure is distinct from missing credentials
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And the injected credential store fails while reading
    When I run `colt auth status`
    Then status shows "credential storage failure" and not "credentials missing"
    And no provider client is constructed

  @unimplemented @CORE-PROVIDER-005
  Scenario: Git SSH access succeeds independently of provider API credential
    Given native Git SSH access to a repository works
    And the provider API credential is missing
    When repository access is checked
    Then Git transport success does not imply provider API authentication
    And provider status still reports "credentials missing"

  @unimplemented @CORE-PROVIDER-005
  Scenario: Provider API authentication succeeds while Git SSH access fails
    Given provider API authentication succeeds for provider "personal"
    And native Git SSH access fails
    When repository access is checked
    Then provider status still reports "connected"
    And the Git failure is reported separately from provider authentication

  @unimplemented @CORE-PROVIDER-008 @CORE-CREDENTIAL-007
  Scenario: Logout removes only the selected credential ID
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "selected-logout-secret"
    And the injected credential store also holds "github.com/work" with secret "neighbor-logout-secret"
    When I run `colt auth logout personal`
    Then the command succeeds
    And exactly credential "github.com/personal" is deleted from the injected store
    And credential "github.com/work" remains unchanged
    And provider configuration is unchanged
    And no provider or native Git operation is invoked

  @unimplemented @CORE-PROVIDER-008
  Scenario: Logout works without provider connectivity
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "offline-logout-secret"
    And the provider API is unreachable
    When I run `colt auth logout personal`
    Then the command succeeds
    And exactly credential "github.com/personal" is deleted from the injected store
    And provider configuration is unchanged
    And no provider API or Git remote is contacted

  @unimplemented @CORE-PROVIDER-008
  Scenario: Environment credential remains usable after stored credential logout
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "stored-logout-secret"
    And "GITHUB_TOKEN" contains "env-fake-secret-4"
    When I run `colt auth logout personal`
    Then exactly credential "github.com/personal" is deleted from the injected store
    And logout warns that "GITHUB_TOKEN" may still resolve without revealing its value
    And `colt auth status` still authenticates via "GITHUB_TOKEN"
    And output does not contain "env-fake-secret-4"

  @unimplemented @CORE-PROVIDER-009
  Scenario: Logout with revoke removes remote and local credential
    Given provider "personal" supports provider-side revocation
    When I run `colt auth logout personal --revoke`
    Then the remote credential is revoked
    And the local persisted credential is removed

  @unimplemented @CORE-PROVIDER-009
  Scenario: Remote revocation failure still allows local credential removal
    Given provider-side revocation fails for provider "personal"
    When I run `colt auth logout personal --revoke`
    Then output reports "! remote revocation failed"
    And output reports local credential removal
    And the local persisted credential is removed

  @unimplemented @CORE-PROVIDER-009
  Scenario: Local credential removal failure is reported after remote revocation
    Given provider-side revocation succeeds for provider "personal"
    And local credential deletion fails
    When I run `colt auth logout personal --revoke`
    Then output reports the remote revocation
    And output reports "local credential removal failed"

  @unimplemented @CORE-PROVIDER-009
  Scenario: Unsupported provider-side revocation is reported safely
    Given the provider reports revocation as unsupported
    When I run `colt auth logout personal --revoke`
    Then output reports revocation is unsupported without exposing secrets
    And the local persisted credential is still removed

  @unimplemented @CORE-PROVIDER-009
  Scenario: Ordinary logout does not attempt remote revocation
    Given the provider API is unreachable
    When I run `colt auth logout personal`
    Then no revocation API call is attempted
    And the local persisted credential is removed

  @unimplemented @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Live status reports stored credential source without exposing secret
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And its credential "stored-fake-secret-5" is valid
    When I run `colt auth status`
    Then status shows "Credential: stored"
    And status shows "connected"
    And output does not contain "stored-fake-secret-5"

  @unimplemented @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Live status reports environment credential source without exposing secret
    Given provider "personal" has auth source "env" with token_env "GITHUB_TOKEN"
    And "GITHUB_TOKEN" contains "env-fake-secret-6"
    When I run `colt auth status`
    Then status shows "Credential: environment"
    And output does not contain "env-fake-secret-6"

  @unimplemented @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Offline status reports configured credential reference without resolving secret
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    When I run `colt auth status --offline`
    Then status shows "Auth source: stored"
    And status shows "Credential ID: github.com/personal"
    And status shows "not checked"
    And no secret value is read or displayed

  @unimplemented @CORE-GIT-002 @CORE-GIT-005 @CORE-GIT-008
  Scenario: HTTPS managed repository uses the Colt credential helper
    Given a managed repository has remote "https://github.com/example-user/example-project.git"
    When I run ordinary "git push" without invoking Colt
    Then Git resolves credentials through the repository-local Colt credential helper
    And no provider credential is re-entered

  @CORE-GIT-006
  Scenario: Git credential get resolves an injected stored Colt credential
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "helper-stored-secret"
    When Git requests "protocol=https host=github.com path=example-user/example-project.git"
    Then helper stdout is exactly the GitHub username and password protocol fields for "helper-stored-secret"

  @CORE-GIT-006 @CORE-CREDENTIAL-001
  Scenario: Git credential get resolves an environment credential according to precedence
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "helper-stored-secret"
    And "GITHUB_TOKEN" contains a different temporary token "helper-env-secret"
    When Git requests the provider credential
    Then the helper uses the value from "GITHUB_TOKEN"
    And the injected stored credential is unchanged and was not read

  @CORE-GIT-007 @CORE-PROVIDER-004
  Scenario: Credential helper returns no credential for an unrelated host
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "unrelated-host-secret"
    When Git requests credentials for "example.org"
    Then the helper returns no credential and performs no store lookup

  @CORE-GIT-006 @CORE-CREDENTIAL-002
  Scenario: Credential helper exposes the token only through the Git credential protocol
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds "github.com/personal" with secret "helper-only-secret"
    When Git requests the provider credential
    Then helper stdout is exactly the GitHub username and password protocol fields for "helper-only-secret"

  @unimplemented @CORE-GIT-002 @CORE-GIT-006
  Scenario: HTTPS push reuses persisted Colt authentication
    Given provider "personal" has a persisted credential
    And the managed repository configures the Colt credential helper
    When I run ordinary "git push"
    Then the push authenticates without re-entering credentials

  @unimplemented @CORE-GIT-008 @CORE-CREDENTIAL-002
  Scenario: HTTPS remote URL never contains a reusable credential
    Given a managed HTTPS repository
    Then the "origin" URL contains no token, password, or userinfo
    And ".git/config" contains no reusable credential value

  @CORE-GIT-008
  Scenario: Credential helper store does not silently persist arbitrary Git credentials
    Given an injected credential store holds "github.com/personal" with secret "existing-store-secret"
    And Git invokes the helper with "store" for an unknown credential
    When the helper handles the invocation
    Then no unknown credential is written to the Colt credential store
    And the invocation follows safe Git credential-helper semantics

  @CORE-GIT-008
  Scenario: Credential helper erase is a no-op for Colt credentials
    Given an injected credential store holds "github.com/personal" with secret "existing-erase-secret"
    When Git invokes the helper with "erase" after a failed authentication
    Then the injected stored credential is unchanged and no delete was attempted
    And the invocation follows safe Git credential-helper semantics

  @CORE-GIT-006 @CORE-CREDENTIAL-002
  Scenario: Malformed credential helper input fails without reflecting input values
    Given Git supplies a malformed credential request containing "malformed-input-secret"
    When the helper handles the invocation
    Then the helper fails without outputting "malformed-input-secret"
    And no credential store operation is attempted

  @CORE-GIT-006 @CORE-CREDENTIAL-002
  Scenario: Credential helper suppresses protocol-injection credentials
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And an injected credential store holds a credential containing a newline
    When Git requests the provider credential
    Then the helper succeeds with no credential output

  @unimplemented @CORE-GIT-010
  Scenario: SSH managed repository uses the provider-authoritative SSH URL
    Given the transport preference resolves to SSH
    Then the "origin" URL is "git@github.com:example-user/example-project.git"

  @unimplemented @CORE-GIT-010
  Scenario: GitHub SSH URL uses git as the SSH user
    Given a GitHub SSH remote
    Then the SSH user is "git", not the provider account name
    And the repository owner remains in the repository path

  @unimplemented @CORE-GIT-010
  Scenario: Existing SSH configuration can authenticate Git operations
    Given the user's SSH agent and default keys select the correct identity
    Then no "~/.ssh/config" entry is required
    And Git operations authenticate through the existing SSH environment

  @unimplemented @CORE-GIT-010
  Scenario: SSH private key remains outside Colt credential storage
    Given a managed SSH repository
    Then the Colt credential subsystem holds no SSH private-key material
    And Colt never invokes key generation or agent management

  @unimplemented @CORE-GIT-010 @CORE-PROVIDER-005
  Scenario: Missing SSH access produces a Git transport failure distinct from provider API authentication
    Given provider API authentication succeeds for provider "personal"
    And native Git SSH access fails because no registered public key matches
    When repository access is checked
    Then the Git failure is reported separately from provider authentication

  @unimplemented @CORE-GIT-010 @CORE-PROVIDER-008
  Scenario: SSH repository access can succeed after Colt provider logout
    Given provider "personal" has no persisted credential after logout
    And the user's SSH key remains registered with the provider
    When I run ordinary "git push" over SSH
    Then the push can still succeed
    And no SSH configuration was modified by the logout

  @unimplemented @CORE-GIT-010
  Scenario: Multiple accounts on one SSH host use host aliases
    Given SSH hosts "github-personal" and "github-work" both resolve to "github.com"
    When the repository remote uses "git@github-personal:example-user/example-project.git"
    Then the alias is treated as a local SSH name, not a provider authority
    And the selected transport is validated against the expected provider repository

  @unimplemented @CORE-GIT-008 @CORE-PROVIDER-008
  Scenario: Logout leaves HTTPS helper configuration but removes its credential
    Given a managed HTTPS repository configures the Colt credential helper
    When I run `colt auth logout personal`
    Then the repository remote and helper configuration are unchanged
    And a later "git push" may fail or fall through to another credential source

  @CORE-IDENTITY-001
  Scenario: Git identity is preserved independently of provider authentication
    Given a locally initialized repository using provider "work"
    Then local Git user.name is "Example User"
    And local Git user.email is "user@example.invalid"
    And global Git identity is unchanged
