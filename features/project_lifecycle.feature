Feature: Project lifecycle
  Milestone 2 core list, clone, transport, minimum release, and hostile
  repository-configuration hardening are implemented.

  @LIFECYCLE-LIST-001
  Scenario: List repositories through normal provider resolution
    Given one provider is selected by normal provider resolution
    When I run `colt list`
    Then all visible repositories from that provider are returned in deterministic order

  @LIFECYCLE-LIST-001
  Scenario: List every provider without losing successful results
    Given multiple providers are configured and one provider request fails
    When I run `colt list --all`
    Then complete results from successful providers are returned in deterministic order
    And the independent provider failure is reported

  @LIFECYCLE-LIST-001
  Scenario: List only the requested namespace
    Given one provider has repositories in more than one namespace
    When I run `colt list --namespace example-namespace`
    Then only repositories in the requested namespace are returned
    And repositories in other namespaces are excluded

  @LIFECYCLE-LIST-001
  Scenario: List namespace rejects --all
    Given one provider is selected by normal provider resolution
    When I run `colt list --all --namespace example-namespace`
    Then the command fails because --namespace cannot be combined with --all

  @LIFECYCLE-CLONE-001 @LIFECYCLE-CLONE-002 @LIFECYCLE-CLONE-003 @LIFECYCLE-CLONE-004 @LIFECYCLE-CLONE-006
  Scenario: Clone one unambiguous HTTPS repository safely
    Given selected-provider metadata resolves "api" to a clean authoritative URL on its configured authority without userinfo, query, or fragment
    And its destination is a clean relative missing or empty path
    When I run `colt clone api`
    Then HTTPS credentials resolve through the repository-local Colt credential helper without URL persistence
    And the selected identity is applied only to the local repository

  @LIFECYCLE-CLONE-001 @LIFECYCLE-CLONE-002 @LIFECYCLE-CLONE-003 @LIFECYCLE-SECURITY-004
  Scenario: Refuse untrusted clone metadata
    Given repository resolution is ambiguous or its origin is not an authoritative HTTPS or SSH URL on the selected configured authority
    And the origin has userinfo, query, fragment, or an authority-changing redirect
    When I run `colt clone api`
    Then the command fails closed before Git invocation, credential exposure, or outside-root access or mutation

  @LIFECYCLE-CLONE-005 @LIFECYCLE-SECURITY-005
  Scenario: Refuse an unsafe clone destination
    Given the clone destination is a symlink to an outside sentinel
    When I run `colt clone api`
    Then the command fails closed before Git invocation, credential exposure, or outside-root access or mutation
    And the outside sentinel is unchanged

  @LIFECYCLE-SECURITY-006
  Scenario: Confine clone installation during an actual work-root swap
    Given the work root is renamed and replaced by an escaping symlink while Git clones to private staging
    When I run `colt clone api`
    Then the completed clone is installed beneath the opened work root
    And the outside sentinel is unchanged

  @LIFECYCLE-SECURITY-001
  Scenario: Clone ignores inherited Git controls
    Given inherited, global, and system Git controls are hostile
    When I run `colt clone api`
    Then native Git ignores inherited GIT controls and global and system configuration

  @LIFECYCLE-CLONE-004
  Scenario: SSH clone delegates authentication without reading keys
    Given selected-provider metadata resolves "api" to a clean authoritative SSH URL
    When I run `colt clone api --transport ssh`
    Then SSH clone uses the existing user SSH environment without Colt reading private keys

  @LIFECYCLE-RELEASE-001
  Scenario: Create a minimum release in order
    Given the repository, version, and tag pass preflight
    And provider metadata supplies a clean authoritative push URL matching the current repository identity, origin, provider, repository, and authority after URL rewriting
    When I run `colt release 1.2.3`
    Then Colt creates the local tag, pushes it over the configured transport to the validated explicit URL, and creates the provider release using provider API authentication in order
    And the persistent remote URL stays credential-free

  @LIFECYCLE-RELEASE-001
  Scenario: Tag push uses configured Git transport
    Given the configured Git transport is SSH or HTTPS
    When I run `colt release 1.2.3`
    Then the tag push uses the configured transport
    And provider release creation still requires provider API authentication

  @LIFECYCLE-RELEASE-001 @LIFECYCLE-SECURITY-004
  Scenario: Reject mismatched release push metadata
    Given a remote name or nominal fetch URL appears to match the selected repository
    And pushurl or URL rewriting selects a different authority or repository
    When I run `colt release 1.2.3`
    Then the command fails before creating or changing a tag, executing local controls, or exposing credentials

  @LIFECYCLE-SECURITY-001 @LIFECYCLE-SECURITY-002 @LIFECYCLE-SECURITY-003
  Scenario: Reject or isolate hostile release Git configuration
    Given repository-local configuration requests malicious execution, transport, proxy, credential-helper, or pre-push hook behavior
    When I run `colt release 1.2.3`
    Then release Git ignores inherited, global, system, and unsafe repository-local controls and disables hooks including pre-push
    And the command fails before creating or changing a tag, executing local controls, or exposing credentials

  @CORE-GIT-004
  Scenario: HTTPS transport selects the authoritative HTTPS repository URL
    Given the transport preference resolves to HTTPS
    When Colt selects the clone/push target
    Then the target is the authoritative HTTPS URL for the selected provider repository
    And the managed repository configures the repository-local Colt credential helper

  @CORE-GIT-004
  Scenario: SSH transport selects the authoritative SSH repository URL
    Given the transport preference resolves to SSH
    When Colt selects the clone/push target
    Then the target is the authoritative SSH URL for the selected provider repository

  @CORE-GIT-009
  Scenario: Explicit transport preference overrides the default
    Given the product default transport is HTTPS
    And an explicit transport preference selects SSH
    When Colt selects the clone/push target
    Then the SSH target is used

  @CORE-GIT-009
  Scenario: Explicit choice overrides provider preference
    Given the product default transport is HTTPS
    And the provider preference is SSH
    And the explicit command choice is HTTPS
    When Colt selects the clone/push target
    Then the HTTPS target is used

  @CORE-GIT-009
  Scenario: Explicit choice overrides global preference
    Given the product default transport is HTTPS
    And the global preference is SSH
    And the explicit command choice is HTTPS
    When Colt selects the clone/push target
    Then the HTTPS target is used

  @CORE-GIT-009
  Scenario: Provider preference overrides global preference
    Given the product default transport is HTTPS
    And the global preference is HTTPS
    And the provider preference is SSH
    When Colt selects the clone/push target
    Then the SSH target is used

  @CORE-GIT-009
  Scenario: Provider preference overrides product default
    Given the product default transport is HTTPS
    And the provider preference is SSH
    When Colt selects the clone/push target
    Then the SSH target is used

  @CORE-GIT-009
  Scenario: Global preference overrides product default
    Given the product default transport is HTTPS
    And the global preference is SSH
    When Colt selects the clone/push target
    Then the SSH target is used

  @CORE-GIT-009
  Scenario: Unsupported transport fails before repository mutation
    Given the requested transport is unsupported for the selected provider
    When Colt selects the clone/push target
    Then the command fails before repository mutation

  @CORE-GIT-009 @LIFECYCLE-CLONE-004
  Scenario: Clone leaves ordinary Git usable without re-entering credentials
    Given a repository was cloned with HTTPS transport and the Colt credential helper
    When I run ordinary "git fetch" without invoking Colt
    Then authentication resolves through the Colt credential helper without re-entering credentials

  @LIFECYCLE-RELEASE-001
  Scenario: Preserve partial release state
    Given the local release tag was created and pushed over the configured Git transport
    When provider release creation fails
    Then Colt does not force, overwrite, or roll back the tag or release
    And the completed tag state and failed step are reported as partial completion
