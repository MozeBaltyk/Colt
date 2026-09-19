Feature: Interactive command input
  Colt prompts only for missing required input when standard input is an interactive terminal.

  @CORE-CLI-001 @CORE-CLI-003
  Scenario: Auth login prompts for required values in stable order and preserves defaults
    Given standard input is an interactive terminal
    And "GITHUB_TOKEN" contains "interactive-login-secret"
    When I run `colt auth login github personal`
    Then login prompts are exactly "namespace: git-name: git-email: " in that order
    And the supplied login values are saved
    And optional visibility and transport defaults are unchanged
    And output does not contain "interactive-login-secret"

  @CORE-CLI-001
  Scenario: Init prompts for its missing project positional argument
    Given a valid provider with namespace, defaults, and Git identity is selected
    And interactive terminal input supplies "demo"
    When I run `colt init --local`
    Then the prompt is exactly "project: "
    And "demo" is a new Git repository with no remote

  @CORE-CLI-001
  Scenario: Auth logout prompts for its missing alias positional argument
    Given provider "personal" has auth source "env" with token_env "GITHUB_TOKEN"
    And interactive terminal input supplies "personal"
    When I run `colt auth logout`
    Then the output starts with the exact prompt "alias: "
    And provider "personal" configuration is unchanged

  @CORE-CLI-002 @CORE-CLI-003
  Scenario Outline: Missing login input fails in stable order without prompting or mutation
    Given <mode>
    When I run `colt auth login`
    Then the missing-input error lists "provider, alias, namespace, git-name, git-email" in that order
    And no prompt or interactive authorization flow starts
    And no config, credential, provider, or Git mutation occurs

    Examples:
      | mode                                    |
      | I pass the root flag `--noninteractive` |
      | standard input is not a terminal        |

  @CORE-CLI-003
  Scenario Outline: Explicit invalid login input wins over missing-input handling
    Given standard input is an interactive terminal
    And I pass the root flag `--noninteractive`
    When I run `<command>`
    Then the command fails with an invalid-input error, not a missing-input error
    And no prompt or interactive authorization flow starts
    And no config, credential, provider, or Git mutation occurs

    Examples:
      | command                                                   |
      | colt auth login bitbucket                                 |
      | colt auth login github personal --visibility internal     |
      | colt auth login github personal --transport ftp           |
      | colt auth login github personal --credential file         |
      | colt auth login github personal --token-env BAD-NAME      |
      | colt auth login github personal --namespace ../unsafe     |

  @CORE-CLI-003 @CORE-CREDENTIAL-002
  Scenario: A reusable secret returned by the non-echoing reader is not displayed
    Given no provider token environment variable resolves
    And manual token entry supplies "prompted-fake-secret"
    When I run `colt auth login github personal --namespace octocat --git-name Test --git-email test@example.com`
    Then the command succeeds
    And no reusable credential value appears in output

  @blackbox @CORE-CLI-003 @CORE-CREDENTIAL-002
  Scenario: A real terminal does not echo a prompted reusable secret
    Given the real Colt binary is attached to a pseudo-terminal
    When the terminal token prompt receives "prompted-fake-secret"
    Then the terminal transcript does not contain "prompted-fake-secret"
