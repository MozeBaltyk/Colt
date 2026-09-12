Feature: Provider expansion
   Gitea implements the common authentication and project initialization contracts.
   Forgejo implements the same contracts through its own independent adapter.

  @integration @gitea @CORE-PROVIDER-010 @CORE-PROVIDER-007 @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002 @INIT-006 @INIT-007
  Scenario: Colt initializes and pushes a project to Gitea over trusted HTTPS
    Given an ephemeral Gitea provider serving trusted HTTPS
    When the real Colt binary initializes project "demo" through that provider
    Then Gitea contains the initial commit on branch "main"
    And the local origin and Gitea credential helper are configured without storing the token

  @integration @forgejo @CORE-PROVIDER-010 @CORE-PROVIDER-007 @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002 @INIT-006 @INIT-007
  Scenario: Colt initializes and pushes a project to Forgejo over trusted HTTPS
    Given an ephemeral Forgejo provider serving trusted HTTPS
    When the real Colt binary initializes project "demo" through that provider
    Then Forgejo contains the initial commit on branch "main"
    And the local origin and Forgejo credential helper are configured without storing the token

  @planned @CORE-PROVIDER-010
  Scenario: Forgejo resolves through the common resolution contract
    Given a configured Forgejo provider on an independent host
    When a command selects it by explicit alias, configured default, or sole-provider fallback
    Then normal provider resolution applies unchanged and never prefers a provider type

  @planned @CORE-PROVIDER-010
  Scenario: Forgejo is an independent adapter, not a Gitea alias
    Given a Forgejo provider and a Gitea provider are both configured
    When provider behavior is selected
    Then each type dispatches to its own adapter
    And neither type is accepted as a value for the other

  @planned @CORE-PROVIDER-007 @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002 @CORE-CREDENTIAL-004
  Scenario: Forgejo reuses the common credential contract
    Given a configured Forgejo provider with an environment credential source
    When Colt authenticates the provider
    Then resolution order, redaction, and the no-secrets-in-config rules apply unchanged
    And Forgejo authorization stays inside the Forgejo adapter

  @planned @INIT-006 @INIT-007 @INIT-008 @INIT-009
  Scenario: Forgejo initializes remote projects through the common flow
    Given a configured Forgejo provider with valid API authentication
    When I run "colt init demo"
    Then the repository is created through the Forgejo HTTP API
    And origin selection, conflict handling, and partial-failure behavior apply unchanged

  @planned @LIFECYCLE-LIST-001 @LIFECYCLE-CLONE-001 @LIFECYCLE-RELEASE-001
  Scenario Outline: Expanded adapters serve future lifecycle operations per supported capability
    Given a configured <type> provider
    When Colt lists, clones, or releases through it
    Then deterministic results, authoritative clone targets, and ordered release behavior apply unchanged
    And any capability the platform lacks is reported distinctly per `CORE-PROVIDER-011`

    Examples:
      | type    |
      | gitea   |
      | forgejo |
