Feature: Project health diagnostics

  @HEALTH-CHECK-001
  Scenario Outline: Check repository health at the requested scope
    Given a healthy repository and managed workspace with project health policy
    When I run the health command `<command>`
    Then every selected repository is checked through Git and the provider API
    And the health check has no findings and exit code 0

    Examples:
      | command          |
      | colt check       |
      | colt check --all |

  @HEALTH-POLICY-001
  Scenario: Reject unknown health policy before repository access
    Given health policy contains an unknown field
    When I run the health command `colt check`
    Then health configuration fails with exit code 2 before Git or provider access

  @HEALTH-OUTPUT-001
  Scenario Outline: Return deterministic CI-compatible health status
    Given a health check produces <result>
    When I run the health command `colt check`
    Then health findings are sorted and the exit code is <status>

    Examples:
      | result            | status |
      | no findings       | 0      |
      | drift findings    | 1      |
      | operational error | 2      |

  @HEALTH-SAFETY-001
  Scenario: Diagnose hostile repository configuration without mutation or disclosure
    Given a health repository has executable Git configuration and secret-bearing inputs
    When I run the health command `colt check`
    Then health inspection executes nothing, preserves repository, provider, and configuration state, and emits only bounded redacted findings
