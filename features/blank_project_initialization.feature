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

  @INIT-002 @INIT-005 @CORE-CREDENTIAL-003
  Scenario: Local initialization does not require provider API authentication
    Given the selected provider uses an unavailable stored API credential
    When I run "colt init demo --local"
    Then "demo" is a new Git repository with no remote
    And it has exactly one initial commit
    And no provider API credential is read
    And no provider client, credential helper, or push is invoked

  @INIT-001 @CORE-IDENTITY-001 @CORE-SAFETY-001
  Scenario: Missing selected Git identity fails preflight
    Given the selected provider has no Git identity
    When I run "colt init demo --local"
    Then the command fails before mutation
    And preflight state is unchanged with no credential, provider, or Git operation

  @INIT-004 @CORE-SAFETY-001
  Scenario: Reject a relative destination whose parent does not exist
    Given destination parent "missing" does not exist
    When I run "colt init demo --destination missing/demo"
    Then the command fails before mutation
    And the missing parent is not created
    And no unrelated filesystem state is modified

   @INIT-001 @INIT-007 @CORE-SAFETY-001
   Scenario: Unsupported transport fails preflight
    Given the selected provider transport is unsupported
    When I run "colt init demo"
    Then the command fails before mutation
    And preflight state is unchanged with no credential, provider, or Git operation

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
    And no provider client is constructed

  @INIT-006 @INIT-007
  Scenario Outline: Initialize and push a remote blank project
    Given the selected provider type is <type>
    When I run "colt init demo"
    Then Colt creates repository "demo" in the selected namespace through the <type> HTTP API
    And it is cloned locally as "example-namespace/demo"
    And the created repository URL is the only "origin"
    And the initial commit is submitted once through the configured Git runner

    Examples:
      | type   |
      | GitHub |
      | GitLab |

  @INIT-006 @INIT-010 @CORE-SAFETY-001
  Scenario Outline: Override configured repository visibility for one remote initialization
    Given the selected provider default visibility is "<default>"
    When I run "colt init demo --visibility <override>"
    Then Colt creates repository "demo" with visibility "<override>"
    And the selected provider default visibility remains "<default>"

    Examples:
      | default | override |
      | private | public   |
      | public  | private  |

  @INIT-006 @INIT-010
  Scenario: Use configured repository visibility when init has no override
    Given the selected provider default visibility is "public"
    When I run "colt init demo"
    Then Colt creates repository "demo" with visibility "public"

  @INIT-001 @INIT-010 @CORE-SAFETY-001
  Scenario Outline: Reject an invalid or inapplicable visibility override before mutation
    When I run "<command>"
    Then the command fails before mutation
    And preflight state is unchanged with no credential, provider, or Git operation

    Examples:
      | command                                      |
      | colt init demo --visibility internal         |
      | colt init demo --visibility public --local   |

  @INIT-003 @INIT-006 @CORE-GIT-003
  Scenario: GitHub canonical owner casing is accepted without weakening clone authority
    Given GitHub is configured as "mozebaltyk" but returns canonical owner "MozeBaltyk"
    When I run "colt init demo"
    Then it is cloned locally as "mozebaltyk/demo"
    And the "origin" URL is "https://github.com/MozeBaltyk/demo.git" with no credential in the URL

  @INIT-003
  Scenario: Select an explicit remote clone destination
    Given the selected provider type is GitLab
    And custom destination parent "projects" exists
    When I run "colt init demo --destination projects/renamed"
    Then it is cloned locally as "projects/renamed"

  @INIT-006
  Scenario: Remote initialization requires provider API authentication
    Given provider API authentication fails for the selected provider
    When I run "colt init demo"
    Then the command fails before creating a remote repository
    And SSH Git access alone does not satisfy the requirement
    And no local repository, origin, helper, or push is attempted

  @INIT-007 @CORE-GIT-003 @CORE-GIT-005 @CORE-GIT-008
  Scenario: HTTPS initialization configures the Colt credential helper locally
    Given the selected provider type is GitHub
    And the transport preference resolves to HTTPS
    When I run "colt init demo"
    Then the "origin" URL is "https://github.com/example-user/demo.git" with no credential in the URL
    And local Git configuration contains "credential.helper = colt"
    And ".git/config" contains no reusable credential value
    And ordinary Git invokes the Colt credential helper to resolve credentials for a later push

  @INIT-007 @CORE-GIT-003 @CORE-GIT-010
  Scenario: SSH initialization uses the provider-authoritative SSH URL
    Given the selected provider type is GitHub
    And the transport preference resolves to SSH
    When I run "colt init demo"
    Then the "origin" URL is "git@github.com:example-user/demo.git"
    And the SSH user is "git", not the provider account name
    And native Git handles SSH authentication with no provider token or Colt credential helper injection

  @INIT-004 @CORE-IDENTITY-001
  Scenario: Colt initialization sets repository-local Git identity
    Given a valid provider with namespace, defaults, and Git identity is selected
    When I run "colt init demo --local"
    Then local Git user.name is "Example User"
    And local Git user.email is "user@example.invalid"

  @INIT-008 @CORE-CONFLICT-001 @CORE-SAFETY-001
  Scenario: Provider creation reports an authoritative race conflict
    Given the remote repository did not exist during preflight
    And another actor creates it before Colt's create request completes
    When I run "colt init demo"
    Then the provider conflict is authoritative
    And Colt does not adopt, replace, or delete the remote repository
    And Colt leaves no local clone and reports the conflict

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

  @INIT-011
  Scenario: Local initialization reports only applicable ordered work
    When I run "colt init demo --local"
    Then the initialization report has the operation title followed by these successful steps in order:
      | Preflight                    |
      | Initialize local repository  |
      | Set repository-local identity |
      | Create initial commit        |
    And the initialization report contains no remote, clone, helper, or push step

  @INIT-009 @INIT-011 @CORE-FAILURE-001
  Scenario: Push failure reports progress and evidence-based recovery once
    Given Colt created the local repository, initial commit, remote repository, and "origin"
    When pushing the initial branch fails
    Then completed initialization steps precede the failed push step
    And the result reports the preserved local commit and remote repository
    And the result gives a bounded cause and a safe push recovery command
    And no reusable credential or authorization header appears in the report

  @INIT-011 @CORE-CREDENTIAL-002
  Scenario: Initialization report decoration follows the output terminal
    Given command output is not a terminal
    When I run "colt init demo --local"
    Then initialization status text and symbols are meaningful without color
    And the initialization report contains no ANSI color

  @INIT-003 @CORE-SAFETY-001
  Scenario: Accept an existing empty destination
    Given destination "demo" exists and is empty
    When I run "colt init demo --local"
    Then "demo" is a new Git repository with no remote
    And it has exactly one initial commit
    And no provider client is constructed

  @INIT-003 @CORE-SAFETY-001
  Scenario Outline: Refuse non-directory destinations without changing them
    Given destination "demo" is a <kind>
    When I run "colt init demo --local"
    Then the command fails before changing the <kind>
    And no provider client is constructed

    Examples:
      | kind         |
      | regular file |
      | symlink      |

  @INIT-001 @CORE-CREDENTIAL-003 @CORE-SAFETY-001
  Scenario: Missing remote credential fails before any mutation or client construction
    Given the selected provider credential is missing
    When I run "colt init demo"
    Then the command fails before mutation
    And no mutating Git operation or provider client construction occurs

  @INIT-006 @INIT-007 @CORE-GIT-003 @CORE-SAFETY-001
  Scenario Outline: Provider success with an invalid selected clone target fails safely
    Given the selected transport is <transport>
    And provider creation succeeds with a <target> selected clone target
    When I run "colt init demo"
    Then clone target validation fails before creating a local clone
    And no origin, credential helper, or push is attempted

    Examples:
      | transport | target           |
      | HTTPS     | missing          |
      | HTTPS     | wrong repository |
      | SSH       | missing          |
      | SSH       | wrong repository |

  @INIT-007 @CORE-GIT-005
  Scenario: Colt credential helper configuration coexists locally and is idempotent
    Given a native repository has an existing local credential helper
    When the Colt credential helper is configured twice
    Then the existing helper remains and the Colt helper appears exactly once locally
    And credential.useHttpPath is true in repository-local configuration
    And global Git configuration is unchanged

  @CORE-GIT-009
  Scenario: Explicit transport preference overrides the product default
    Given the product default transport is HTTPS
    And an explicit transport preference selects SSH
    When I run "colt init demo --transport ssh"
    Then the SSH target is used

  @CORE-GIT-009
  Scenario: Unsupported transport fails before repository mutation
    Given the requested transport is invalid
    When I run "colt init demo --transport ftp"
    Then the command fails before creating a destination or remote
