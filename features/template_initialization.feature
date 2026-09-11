Feature: Planned parameterized template initialization
  Milestone 3 behavior is specified but not implemented or released.

  @planned @TEMPLATE-SOURCE-001
  Scenario: Inspect and select a configured immutable template version
    Given template "service" version "2" is configured and immutable
    When I list templates, show "service@2", and select it for initialization
    Then configured metadata and the same pinned source are resolved deterministically
    And no template content is materialized or executed by list or show

  @planned @TEMPLATE-PARAM-001
  Scenario: Resolve declared string parameters
    Given a template declares a required string "owner" and a default string "license"
    When I initialize interactively with `--set owner=platform`
    Then Colt uses the supplied owner and default license
    And Colt prompts only for unresolved declared parameters

  @planned @TEMPLATE-PARAM-001
  Scenario Outline: Reject unknown or missing parameters before mutation
    Given template "service" declares its accepted string parameters
    When noninteractive initialization has <problem>
    Then the command fails before creating a destination or remote

    Examples:
      | problem                    |
      | an unknown parameter       |
      | a missing required value   |

  @planned @TEMPLATE-SAFETY-001
  Scenario: Reject executable or unsafe template data
    Given a template source has Git metadata and remotes and configured credentials are available
    And its content has an arbitrary expression, hook, unsafe path, colliding path, unsafe symlink, or special file
    When Colt validates the template
    Then source Git metadata, history, remotes, and configured credential material are not imported
    And the command fails before materialization or execution
    And nothing is written outside the destination

  @planned @TEMPLATE-SAFETY-001 @TEMPLATE-INIT-001
  Scenario: Initialize a fresh repository from validated data
    Given a configured template and all declared parameters are valid
    When I run `colt init demo --template service@2 --set owner=platform --local`
    Then interpolation is deterministic and data-only
    And "demo" has fresh Git history, no inherited remote, and one commit of validated content
    And the template source is unchanged

  @planned @TEMPLATE-INIT-001
  Scenario: Preserve template initialization partial state
    Given validated template content was committed before a later step failed
    When Colt reports the initialization failure
    Then completed local work is preserved and reported under shared initialization safety rules
