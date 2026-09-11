Feature: Provider configuration and authentication
  Provider API authentication persists across sessions; Git transport and Git identity stay separate.

  @CORE-PROVIDER-006 @CORE-PROVIDER-002 @CORE-CREDENTIAL-002
  Scenario: Interactive GitHub authentication succeeds
    Given no persisted credential exists for provider "personal"
    And the provider authorization flow reports approval for account "octocat"
    When I run `colt auth login github personal`
    Then Colt displays the authorization URL and user code
    And Colt identifies the authenticated account as "octocat"
    And the command succeeds with "personal · GitHub · octocat"
    And no reusable credential value appears in output

  @CORE-PROVIDER-006
  Scenario: Interactive authorization is rejected
    Given the provider authorization flow reports rejection by the user
    When I run `colt auth login github personal`
    Then the command fails with an authorization error
    And no credential is persisted
    And no provider configuration is changed

  @CORE-PROVIDER-006
  Scenario: Interactive authorization expires or times out
    Given the provider authorization flow reports expiry before approval
    When I run `colt auth login github personal`
    Then the command fails with an authorization error
    And no credential is persisted

  @CORE-PROVIDER-002 @CORE-PROVIDER-006
  Scenario: Authenticated provider account is identified
    Given the provider authorization flow reports approval for account "octocat"
    When I run `colt auth login github personal`
    Then the authenticated account is identified before success is reported
    And a mismatched account identity fails the login without persisting

  @CORE-CREDENTIAL-004
  Scenario: Stored authentication uses a non-secret credential identifier
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    Then config.yaml contains "credential_id: github.com/personal"
    And config.yaml contains no reusable credential value
    And "github.com/personal" contains no secret material

  @CORE-CREDENTIAL-004
  Scenario: Environment authentication references a token environment variable
    Given provider "ci" has auth source "env" with token_env "GITHUB_TOKEN"
    Then config.yaml contains "token_env: GITHUB_TOKEN"
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

  @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-004 @CORE-CREDENTIAL-007
  Scenario: Stored credential resolves from secure credential backend
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And the secure credential backend holds "github.com/personal"
    When Colt authenticates provider "personal"
    Then the provider receives the resolved credential without knowing its backend type
    And config.yaml contains no reusable credential value

  @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-007
  Scenario: Stored credential resolves from explicitly enabled plaintext fallback
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And the secure credential backend is unavailable
    And the plaintext fallback holds "github.com/personal" with user-only permissions
    When Colt authenticates provider "personal"
    Then the provider receives the resolved credential without knowing its backend type
    And provider configuration is unchanged

  @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-002
  Scenario: Plaintext credential serialization is versioned
    Given the plaintext fallback file declares "version: 1"
    And it holds credential_id "github.com/personal"
    When Colt parses the credential file
    Then parsing is deterministic
    And config.yaml remains free of reusable credential values

  @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-004
  Scenario: Interactive credential is stored in the secure credential backend
    Given the secure credential backend is available
    And the provider authorization flow reports approval
    When I run `colt auth login github personal`
    Then the reusable credential is stored under a non-secret credential id
    And normal Colt configuration does not contain the reusable credential value

  @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-004
  Scenario: Persisted credential is reused by a later Colt process
    Given provider "personal" has a persisted secure credential
    When a new Colt process authenticates provider "personal" without re-login
    Then authentication succeeds without repeating the authorization flow
    And normal Colt configuration still does not contain the reusable credential value

  @CORE-CREDENTIAL-005
  Scenario: Secure credential storage is unavailable
    Given the secure credential backend is unavailable
    And the provider authorization flow reports approval
    When I run `colt auth login github personal`
    Then Colt offers plaintext file persistence, environment-variable usage, or cancel
    And the command is not reported as persistently successful until a choice is persisted

  @CORE-CREDENTIAL-005 @CORE-CREDENTIAL-002
  Scenario: Colt does not silently fall back to plaintext storage
    Given the secure credential backend is unavailable
    And the user has not consented to plaintext storage
    When interactive authentication succeeds
    Then no reusable credential is written to any file
    And output warns that plaintext storage requires explicit consent

  @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-002
  Scenario: User explicitly accepts plaintext credential persistence
    Given the secure credential backend is unavailable
    And the user accepts plaintext credential persistence
    When interactive authentication succeeds
    Then the credential is stored in the separate credential file, not in config.yaml
    And the credential file has user-readable-only permissions
    And output warns the file is plaintext protected only by filesystem permissions

  @CORE-CREDENTIAL-006
  Scenario: User rejects plaintext credential persistence
    Given the secure credential backend is unavailable
    And the user rejects plaintext credential persistence
    When interactive authentication succeeds
    Then no reusable credential is written to any file
    And the command reports that authentication was not persisted

  @CORE-CREDENTIAL-006 @CORE-CREDENTIAL-002
  Scenario Outline: Unsafe credential file permissions are rejected safely
    Given the plaintext credential file <condition>
    When Colt resolves the persisted credential
    Then Colt fails safely without exposing the secret value
    And normal, verbose, and error output do not contain "plaintext-fake-secret-1"

    Examples:
      | condition                  |
      | is group-readable          |
      | is world-readable          |

  @CORE-CREDENTIAL-002
  Scenario: Credential file contents are never printed
    Given the plaintext credential file contains "plaintext-fake-secret-1"
    When any auth command runs with verbose output enabled
    Then normal, verbose, error, and diagnostic output do not contain "plaintext-fake-secret-1"
    And config.yaml does not contain "plaintext-fake-secret-1"

  @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002
  Scenario: Resolve a token by environment reference and redact it
    Given provider "work" has auth source "env" with token_env "COMPANY_GL_TOKEN"
    And "COMPANY_GL_TOKEN" contains "secret-token-value"
    When provider authentication fails with verbose output enabled
    Then authentication used the value from "COMPANY_GL_TOKEN"
    And normal, verbose, and error output do not contain "secret-token-value"
    And normal Colt configuration and project files do not contain "secret-token-value"

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
  Scenario: Explicit environment credential overrides persisted credential
    Given provider "personal" has a persisted interactive credential
    And "GITHUB_TOKEN" contains a different temporary token "env-fake-secret-2"
    When Colt authenticates provider "personal"
    Then authentication uses the value from "GITHUB_TOKEN"
    And the persisted interactive credential is unchanged
    And output does not contain "env-fake-secret-2"

  @CORE-CREDENTIAL-001
  Scenario: Environment credential is not persisted
    Given provider "personal" has no persisted credential
    And "GITHUB_TOKEN" contains "env-fake-secret-3"
    When Colt authenticates provider "personal"
    Then authentication succeeds for this invocation
    And no credential is written to the secure store or credential file
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

  @CORE-PROVIDER-005
  Scenario: Persisted credential presence does not imply successful connection
    Given provider "work" has a persisted credential that the provider now rejects
    When I run `colt auth status`
    Then status does not show "connected"
    And the failure advises re-login without exposing the secret

  @CORE-PROVIDER-005
  Scenario: Provider API authentication success does not imply Git access
    Given provider API authentication succeeds for provider "work"
    And native Git access to its repositories fails
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
    And each provider shows its alias, type, host, namespace, auth source, credential reference, and default status
    And "Connection" is "not checked"

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Offline auth status reads no secret values and contacts nothing
    Given providers are configured with environment, secure-store, and plaintext credential sources
    When I run `colt auth status --offline`
    Then no environment credential value is read
    And no secure credential value is read
    And no plaintext credential value is read
    And no provider API or Git remote is contacted
    And output does not contain any reusable credential value

  @CORE-PROVIDER-008
  Scenario: Logout removes persisted secure credential
    Given provider "personal" has a persisted secure credential
    When I run `colt auth logout personal`
    Then the secure credential for "personal" is removed
    And provider "personal" configuration is unchanged
    And unrelated credentials are unchanged

  @CORE-PROVIDER-008
  Scenario: Logout removes persisted plaintext credential
    Given provider "personal" has a persisted plaintext credential
    When I run `colt auth logout personal`
    Then the stored secret for "personal" is removed
    And provider "personal" configuration is unchanged

  @CORE-PROVIDER-008
  Scenario: Logout does not delete environment credentials or SSH state
    Given provider "personal" resolves an environment credential
    When I run `colt auth logout personal`
    Then the environment variable is unchanged
    And no SSH key, SSH configuration, or ssh-agent state is modified
    And output explains the environment credential still resolves without revealing it

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Persisted credential rejected by provider produces actionable failure
    Given provider "personal" has a persisted credential the provider rejects
    When I run `colt auth status`
    Then the command reports an authentication failure advising re-login
    And output does not contain the rejected secret value

  @CORE-PROVIDER-005
  Scenario: Network failure does not delete persisted credential
    Given provider "personal" has a persisted credential
    And the provider API is unreachable due to a network failure
    When I run `colt auth status`
    Then the command reports a connection failure
    And the persisted credential is unchanged

  @CORE-PROVIDER-005
  Scenario: Git SSH access succeeds independently of provider API credential
    Given native Git SSH access to a repository works
    And the provider API credential is missing
    When repository access is checked
    Then Git transport success does not imply provider API authentication
    And provider status still reports "credentials missing"

  @CORE-PROVIDER-005
  Scenario: Provider API authentication succeeds while Git SSH access fails
    Given provider API authentication succeeds for provider "personal"
    And native Git SSH access fails
    When repository access is checked
    Then provider status still reports "connected"
    And the Git failure is reported separately from provider authentication

  @CORE-PROVIDER-008 @CORE-CREDENTIAL-007
  Scenario: Logout removes only the selected credential ID
    Given persisted credentials exist for "github.com/personal" and "github.com/work"
    When I run `colt auth logout personal`
    Then credential "github.com/personal" is removed
    And credential "github.com/work" is unchanged
    And Git identity and provider configuration are unchanged

  @CORE-PROVIDER-008
  Scenario: Logout works without provider connectivity
    Given provider "personal" has a persisted credential
    And the provider API is unreachable
    When I run `colt auth logout personal`
    Then the local persisted credential is removed
    And no provider API call is required

  @CORE-PROVIDER-008
  Scenario: Environment credential remains usable after stored credential logout
    Given provider "personal" has a persisted stored credential
    And "GITHUB_TOKEN" contains "env-fake-secret-4"
    When I run `colt auth logout personal`
    Then the stored credential is removed
    And `colt auth status` still authenticates via "GITHUB_TOKEN"
    And output does not contain "env-fake-secret-4"

  @CORE-PROVIDER-009
  Scenario: Logout with revoke removes remote and local credential
    Given provider "personal" supports provider-side revocation
    When I run `colt auth logout personal --revoke`
    Then the remote credential is revoked
    And the local persisted credential is removed

  @CORE-PROVIDER-009
  Scenario: Remote revocation failure still allows local credential removal
    Given provider-side revocation fails for provider "personal"
    When I run `colt auth logout personal --revoke`
    Then output reports "! remote revocation failed"
    And output reports local credential removal
    And the local persisted credential is removed

  @CORE-PROVIDER-009
  Scenario: Local credential removal failure is reported after remote revocation
    Given provider-side revocation succeeds for provider "personal"
    And local credential deletion fails
    When I run `colt auth logout personal --revoke`
    Then output reports the remote revocation
    And output reports "local credential removal failed"

  @CORE-PROVIDER-009
  Scenario: Unsupported provider-side revocation is reported safely
    Given the provider reports revocation as unsupported
    When I run `colt auth logout personal --revoke`
    Then output reports revocation is unsupported without exposing secrets
    And the local persisted credential is still removed

  @CORE-PROVIDER-009
  Scenario: Ordinary logout does not attempt remote revocation
    Given the provider API is unreachable
    When I run `colt auth logout personal`
    Then no revocation API call is attempted
    And the local persisted credential is removed

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Live status reports stored credential source without exposing secret
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    And its credential "stored-fake-secret-5" is valid
    When I run `colt auth status`
    Then status shows "Credential: stored"
    And status shows "connected"
    And output does not contain "stored-fake-secret-5"

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Live status reports environment credential source without exposing secret
    Given provider "personal" has auth source "env" with token_env "GITHUB_TOKEN"
    And "GITHUB_TOKEN" contains "env-fake-secret-6"
    When I run `colt auth status`
    Then status shows "Credential: environment"
    And output does not contain "env-fake-secret-6"

  @CORE-PROVIDER-005 @CORE-CREDENTIAL-002
  Scenario: Offline status reports configured credential reference without resolving secret
    Given provider "personal" has auth source "stored" with credential_id "github.com/personal"
    When I run `colt auth status --offline`
    Then status shows "Auth source: stored"
    And status shows "Credential ID: github.com/personal"
    And status shows "not checked"
    And no secret value is read or displayed
