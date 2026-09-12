Feature: Planned future provider types
  Gitea and Forgejo are planned future adapters, not implemented providers.
  Every scenario here is @planned: none of this behavior is available, and no
  scenario may assume Gitea- or Forgejo-specific protocols, endpoints, scopes,
  token formats, or error mappings. When the adapters land, they satisfy the
  same common contracts through new provider-specific code, without changing
  the core project, credential, workspace, or Git transport models.

  @planned @CORE-PROVIDER-010
  Scenario Outline: Future provider types resolve through the common resolution contract
    Given a configured <type> provider on an independent host
    When a command selects it by explicit alias, configured default, or sole-provider fallback
    Then normal provider resolution applies unchanged and never prefers a provider type

    Examples:
      | type    |
      | gitea   |
      | forgejo |

  @planned @CORE-PROVIDER-010
  Scenario: Multiple installations of one future type coexist by host
    Given a Gitea provider on "code.example.invalid" and another on "git.company.example"
    And a Forgejo provider on "forge.example.invalid"
    When commands address each alias
    Then provider identity remains type plus host plus alias for each
    And credentials, clone targets, and redirect checks never cross hosts

  @planned @CORE-PROVIDER-010
  Scenario: Forgejo is an independent adapter, not a Gitea alias
    Given a Forgejo provider and a Gitea provider are both configured
    When provider behavior is selected
    Then each type dispatches to its own adapter
    And neither type is accepted as a value for the other

  @planned @CORE-PROVIDER-007 @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002 @CORE-CREDENTIAL-004
  Scenario Outline: Future adapters reuse the common credential contract
    Given a configured <type> provider with an environment credential source
    When Colt authenticates the provider
    Then resolution order, redaction, and the no-secrets-in-config rules apply unchanged
    And the authorization mechanism itself stays inside the <type> adapter

    Examples:
      | type    |
      | gitea   |
      | forgejo |

  @planned @INIT-006 @INIT-007 @INIT-008 @INIT-009
  Scenario Outline: Future adapters initialize remote projects through the common flow
    Given a configured <type> provider with valid API authentication
    When I run "colt init demo"
    Then the repository is created through the <type> HTTP API
    And origin selection, conflict handling, and partial-failure behavior apply unchanged

    Examples:
      | type    |
      | gitea   |
      | forgejo |

  @planned @LIFECYCLE-LIST-001 @LIFECYCLE-CLONE-001 @LIFECYCLE-RELEASE-001
  Scenario Outline: Future adapters serve lifecycle operations per supported capability
    Given a configured <type> provider
    When Colt lists, clones, or releases through it
    Then deterministic results, authoritative clone targets, and ordered release behavior apply unchanged
    And any capability the platform lacks is reported distinctly per `CORE-PROVIDER-011`

    Examples:
      | type    |
      | gitea   |
      | forgejo |
