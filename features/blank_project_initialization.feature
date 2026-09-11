Feature: Blank project initialization
  A user initializes a safe local project and may create and push its remote.

  Background:
    Given a valid provider with namespace, defaults, and Git identity is selected
    And native git is available

  @INIT-001 @INIT-002 @INIT-004 @INIT-005 @CORE-IDENTITY-001
  Scenario: Initialize a local blank project
    When I run "colt init demo --local"
    Then "demo" is a new Git repository with no remote
    And it has exactly one initial commit
    And local Git user.name and user.email match the selected identity
    And global Git identity is unchanged
    And no provider mutation was requested

  @INIT-003 @CORE-SAFETY-001
  Scenario: Refuse an existing non-empty destination
    Given destination "demo" exists and contains user data
    When I run "colt init demo --local"
    Then the command fails before mutation
    And the existing destination is unchanged

  @INIT-001
  Scenario: Invalid input fails before mutation
    Given the project name is invalid for the selected provider
    When I run "colt init invalid/name"
    Then the command fails before creating a destination or remote repository

  @INIT-001 @CORE-GIT-001
  Scenario: Native git is unavailable
    Given native git is unavailable
    When I run "colt init demo --local"
    Then the command fails before mutation with an actionable native git error

  @INIT-006 @INIT-007
  Scenario Outline: Initialize and push a remote blank project
    Given the selected provider type is <type>
    And provider API authentication succeeds
    When I run "colt init demo"
    Then Colt creates repository "demo" in the selected namespace through the <type> HTTP API
    And the created repository URL is the only "origin"
    And the initial branch and commit are pushed with upstream tracking using native git

    Examples:
      | type   |
      | GitHub |
      | GitLab |

  @INIT-006
  Scenario: Remote initialization requires provider API authentication
    Given provider API authentication fails for the selected provider
    When I run "colt init demo"
    Then the command fails before creating a remote repository
    And SSH Git access alone does not satisfy the requirement

  @INIT-007
  Scenario Outline: Initial push may use either Git transport
    Given the remote repository was created through the provider HTTP API
    And the authoritative <transport> clone URL matches the selected provider, host, namespace, and repository
    When Colt pushes the initial branch and commit
    Then native git uses the <transport> target without persisting credentials in the remote URL

    Examples:
      | transport |
      | SSH       |
      | HTTPS     |

  @INIT-008 @CORE-CONFLICT-001 @CORE-SAFETY-001
  Scenario: Provider creation reports an authoritative race conflict
    Given the remote repository did not exist during preflight
    And another actor creates it before Colt's create request completes
    When I run "colt init demo"
    Then the provider conflict is authoritative
    And Colt does not adopt, replace, or delete the remote repository
    And Colt preserves completed local work and reports the conflict

  @INIT-008 @CORE-SAFETY-001
  Scenario: Refuse a known existing remote repository
    Given repository "demo" already exists in the selected namespace
    When I run "colt init demo"
    Then the command fails without changing that remote repository
    And Colt does not adopt the existing remote

  @INIT-009 @CORE-FAILURE-001
  Scenario: Push failure preserves partial state
    Given Colt created the local repository, initial commit, remote repository, and "origin"
    When pushing the initial branch fails
    Then the command returns failure and identifies the failed push
    And the local commit, "origin", and remote repository remain intact
    And the result reports local and remote state and a safe recovery action
