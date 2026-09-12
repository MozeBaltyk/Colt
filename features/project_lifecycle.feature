Feature: Planned project lifecycle
  Milestone 2 behavior is specified but not implemented or released.

  @planned @LIFECYCLE-LIST-001
  Scenario: List repositories through normal provider resolution
    Given one provider is selected by normal provider resolution
    When I run `colt list`
    Then all visible repositories from that provider are returned in deterministic order

  @planned @LIFECYCLE-LIST-001
  Scenario: List every provider without losing successful results
    Given multiple providers are configured and one provider request fails
    When I run `colt list --all`
    Then complete results from successful providers are returned in deterministic order
    And the independent provider failure is reported

  @planned @LIFECYCLE-CLONE-001
  Scenario: Clone one unambiguous repository safely
    Given selected-provider metadata resolves "api" to a clean authoritative URL on its configured authority without userinfo, query, or fragment
    And its destination is a clean relative missing or empty path confined beneath the workspace root
    When I run `colt clone api`
    Then destination access remains root-relative, no-follow, and confined throughout the operation
    And native Git ignores inherited GIT controls and global and system configuration
    And HTTPS credentials resolve through the repository-local Colt credential helper without URL persistence
    And SSH clone uses the existing user SSH environment without Colt reading private keys
    And the selected identity is applied only to the local repository

  @planned @LIFECYCLE-CLONE-001
  Scenario: Refuse an unsafe clone
    Given repository resolution is ambiguous or its origin is not an authoritative HTTPS or SSH URL on the selected configured authority
    And the origin has userinfo, query, fragment, or an authority-changing redirect
    And the derived destination is absolute, traversing, a symlink, beneath an escaping symlink ancestor, or non-empty
    And a destination ancestor may be swapped to an escaping symlink between validation and an actual write
    When I run `colt clone api`
    Then the command fails closed before Git invocation, credential exposure, or outside-root access or mutation
    And the destination is unchanged

  @planned @LIFECYCLE-RELEASE-001
  Scenario: Create a minimum release in order
    Given the repository, version, and tag pass preflight
    And provider metadata supplies a clean authoritative push URL matching the current repository identity, origin, provider, repository, and authority after URL rewriting
    When I run `colt release 1.2.3`
    Then release Git ignores inherited, global, system, and unsafe repository-local controls and disables hooks including pre-push
    And Colt creates the local tag, pushes it over the configured transport to the validated explicit URL, and creates the provider release using provider API authentication in order
    And the persistent remote URL stays credential-free

  @planned @LIFECYCLE-RELEASE-001
  Scenario: Tag push uses configured Git transport
    Given the configured Git transport is SSH or HTTPS
    When I run `colt release 1.2.3`
    Then the tag push uses the configured transport
    And provider release creation still requires provider API authentication

  @planned @LIFECYCLE-RELEASE-001
  Scenario: Reject unsafe release push configuration
    Given a remote name or nominal fetch URL appears to match the selected repository
    And pushurl or URL rewriting selects a different authority or repository
    And repository-local configuration requests malicious execution, transport, proxy, credential-helper, or pre-push hook behavior
    When I run `colt release 1.2.3`
    Then the command fails before creating or changing a tag, executing local controls, or exposing credentials

  @planned @LIFECYCLE-TRANSPORT-001
  Scenario: HTTPS transport selects the authoritative HTTPS repository URL
    Given the transport preference resolves to HTTPS
    When Colt selects the clone/push target
    Then the target is the authoritative HTTPS URL for the selected provider repository
    And the managed repository configures the repository-local Colt credential helper

  @planned @LIFECYCLE-TRANSPORT-001
  Scenario: SSH transport selects the authoritative SSH repository URL
    Given the transport preference resolves to SSH
    When Colt selects the clone/push target
    Then the target is the authoritative SSH URL for the selected provider repository

  @planned @LIFECYCLE-TRANSPORT-001
  Scenario: Explicit transport preference overrides the default
    Given the product default transport is HTTPS
    And an explicit transport preference selects SSH
    When Colt selects the clone/push target
    Then the SSH target is used

  @planned @LIFECYCLE-TRANSPORT-001
  Scenario: Unsupported transport fails before repository mutation
    Given the requested transport is unsupported for the selected provider
    When Colt selects the clone/push target
    Then the command fails before repository mutation

  @planned @LIFECYCLE-TRANSPORT-001
  Scenario: Clone leaves ordinary Git usable without re-entering credentials
    Given a repository was cloned with HTTPS transport and the Colt credential helper
    When I run ordinary "git fetch" without invoking Colt
    Then authentication resolves through the Colt credential helper without re-entering credentials

  @planned @LIFECYCLE-RELEASE-001
  Scenario: Preserve partial release state
    Given the local release tag was created and pushed over the configured Git transport
    When provider release creation fails
    Then Colt does not force, overwrite, or roll back the tag or release
    And the completed tag state and failed step are reported as partial completion
