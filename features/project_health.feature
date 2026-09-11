Feature: Planned project health diagnostics
  Milestone 5 behavior is specified but not implemented or released.

  @planned @HEALTH-CHECK-001
  Scenario Outline: Check repository health at the requested scope
    Given health policy is valid for the current repository and managed workspace
    When I run `<command>`
    Then Colt checks <scope> for local identity, expected origin and provider, remote existence and default branch, worktree cleanliness, required README and LICENSE files, and visibility

    Examples:
      | command          | scope                              |
      | colt check       | the current repository             |
      | colt check --all | every managed workspace repository |

  @planned @HEALTH-POLICY-001
  Scenario Outline: Reject unsafe health policy or path access
    Given data-only policy is limited to require, default_branch, and allowed_visibility
    And it <violation>
    When I run `colt check`
    Then policy validation or repository-root-relative no-follow access fails closed
    And no file outside the repository is read or mutated

    Examples:
      | violation                                                                   |
      | contains an unknown field                                                   |
      | has an absolute required-file path                                          |
      | has a traversing required-file path                                         |
      | has a clean required-file path whose ancestor is swapped to an escaping symlink between validation and the actual read |

  @planned @HEALTH-OUTPUT-001
  Scenario Outline: Return CI-compatible health status
    Given a health check produces <result>
    When the check completes
    Then findings are reported deterministically
    And the exit status is <status>

    Examples:
      | result                             | status |
      | no findings                        | 0      |
      | policy or drift findings           | 1      |
      | an operational or configuration error | 2  |

  @planned @HEALTH-SAFETY-001
  Scenario: Diagnose without execution, mutation, or disclosure
    Given hooks, repository files, credential environment values, provider response bodies, and child-process output are available
    When I run `colt check`
    Then Colt does not execute or auto-fix repository content
    And no source, provider, or configuration state is changed
    And output contains only allowlisted metadata and findings with bounded redacted diagnostics
    And it omits credential values, provider response bodies, repository file contents, and raw child-process output
