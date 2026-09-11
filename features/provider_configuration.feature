Feature: Provider configuration and authentication
  Provider credentials are validated without storing or exposing token values.

  @CORE-PROVIDER-001 @CORE-PROVIDER-002 @CORE-CREDENTIAL-001
  Scenario Outline: Configure and authenticate a supported provider
    Given the environment variable "<token_env>" contains a valid token
    When I add a <type> provider named "<alias>" for host "<host>" and namespace "<namespace>" using token_env "<token_env>"
    Then Colt authenticates directly with "<host>"
    And the provider "<alias>" is configured with its visibility and Git identity

    Examples:
      | type   | alias        | host                   | namespace      | token_env       |
      | github | personal     | github.com             | octocat        | GITHUB_TOKEN    |
      | gitlab | public-lab   | gitlab.com             | platform       | GITLAB_TOKEN    |
      | gitlab | company-lab  | gitlab.company.example | infrastructure | COMPANY_GL_TOKEN |

  @CORE-PROVIDER-002
  Scenario: Authentication failure preserves prior configuration
    Given provider "work" has an existing valid configuration
    And its referenced token is invalid
    When I try to update and authenticate provider "work"
    Then the command fails with an authentication error
    And the prior provider configuration is unchanged

  @CORE-PROVIDER-001
  Scenario: Provider aliases are unique
    Given provider "work" is already configured
    When I try to add another provider named "work"
    Then the command fails with a duplicate alias error
    And the existing provider configuration is unchanged

  @CORE-PROVIDER-003
  Scenario: Self-hosted GitLab uses its configured base URL
    Given a GitLab provider uses host "gitlab.company.example"
    When Colt authenticates the provider
    Then the request uses the configured self-hosted GitLab base URL
    And no request is sent to GitLab.com

  @CORE-CREDENTIAL-001 @CORE-CREDENTIAL-002
  Scenario: Resolve a token by environment reference and redact it
    Given provider "work" has token_env "COMPANY_GL_TOKEN"
    And "COMPANY_GL_TOKEN" contains "secret-token-value"
    When provider authentication fails with verbose output enabled
    Then authentication used the value from "COMPANY_GL_TOKEN"
    And normal, verbose, and error output do not contain "secret-token-value"
    And normal Colt configuration and project files do not contain "secret-token-value"

  @CORE-CREDENTIAL-001
  Scenario Outline: Use the provider's default token environment variable
    Given a <type> provider has no token_env configured
    And environment variable "<token_env>" contains a valid token
    When Colt authenticates the provider
    Then authentication uses the value from "<token_env>"

    Examples:
      | type   | token_env    |
      | GitHub | GITHUB_TOKEN |
      | GitLab | GITLAB_TOKEN |

  @CORE-CREDENTIAL-003
  Scenario Outline: Reject an unusable token environment variable
    Given provider "work" has token_env "COMPANY_GL_TOKEN"
    And "COMPANY_GL_TOKEN" is <state>
    When Colt authenticates the provider
    Then the command fails before sending an authenticated request
    And no project or provider state is changed

    Examples:
      | state        |
      | unset        |
      | an empty value |
