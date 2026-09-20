Feature: Parameterized template initialization

  @TEMPLATE-SOURCE-001
  Scenario: Inspect and select a configured immutable template version
    Given template "service" version "2" is configured and immutable
    When I list templates, show "service@2", and select it for initialization
    Then configured metadata and the same pinned source are resolved deterministically
    And no template content is materialized or executed by list or show

  @TEMPLATE-PARAM-001
  Scenario: Resolve declared string parameters
    Given a template declares a required string "owner" and a default string "license"
    When I initialize interactively with `--set owner=platform`
    Then Colt uses the supplied owner and default license
    And Colt prompts only for unresolved declared parameters

  @TEMPLATE-PARAM-001
  Scenario Outline: Reject unknown or missing parameters before mutation
    Given template "service" declares its accepted string parameters
    When noninteractive initialization has <problem>
    Then the command fails before creating a destination or remote

    Examples:
      | problem                    |
      | an unknown parameter       |
      | a missing required value   |

  @TEMPLATE-SAFETY-001
  Scenario Outline: Reject each executable or unsafe template class
    Given a template source has Git metadata and remotes and configured credentials are available
    And its content has <unsafe class>
    When Colt validates the template
    Then source Git metadata, history, remotes, and configured credential material are not imported
    And unsafe class <unsafe class> fails before materialization or execution
    And nothing is written outside the destination

    Examples:
      | unsafe class             |
      | an arbitrary expression  |
      | a malformed expression   |
      | an executable hook       |
      | an unsafe path           |
      | a colliding path         |
      | an unsafe symlink        |
      | a special file           |

  @TEMPLATE-SAFETY-001 @TEMPLATE-INIT-001
  Scenario: Initialize a fresh repository from validated data
    Given a configured template and all declared parameters are valid
    When I run `colt init demo --template service@2 --set owner=platform --local`
    Then interpolation is deterministic and data-only
    And "demo" has fresh Git history, no inherited remote, and one commit of validated content
    And the template source is unchanged

  @TEMPLATE-INIT-001
  Scenario: Preserve template initialization partial state
    Given validated template content was committed before a later step failed
    When Colt reports the initialization failure
    Then completed local work is preserved and reported under shared initialization safety rules
