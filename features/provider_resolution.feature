Feature: Provider resolution
  Commands select one provider without favoring a provider type.

  @CORE-RESOLVE-001
  Scenario: Explicit provider has highest precedence
    Given providers "personal" and "work" are configured
    And "personal" is the configured default
    When I run a command with provider "work"
    Then Colt selects provider "work"

  @CORE-RESOLVE-001
  Scenario: Configured default is used without an explicit provider
    Given providers "personal" and "work" are configured
    And "work" is the configured default
    When I run a command without a provider option
    Then Colt selects provider "work"

  @CORE-RESOLVE-001
  Scenario Outline: The sole provider is selected regardless of type
    Given only one <type> provider named "<alias>" is configured
    And no default provider is configured
    When I run a command without a provider option
    Then Colt selects provider "<alias>"

    Examples:
      | type   | alias    |
      | github | personal |
      | gitlab | work     |

  @CORE-RESOLVE-001
  Scenario: Multiple providers without a default are ambiguous
    Given providers "personal" and "work" are configured
    And no default provider is configured
    When I run a command without a provider option
    Then the command fails without selecting a provider
    And the error requires an explicit provider or one configured default

  @CORE-RESOLVE-002
  Scenario Outline: Invalid higher-precedence selection does not fall through
    Given providers "personal" and "work" are configured
    And "personal" is otherwise selectable
    When provider selection encounters "<condition>"
    Then the command fails without selecting "personal"

    Examples:
      | condition                      |
      | an unknown explicit alias      |
      | multiple configured defaults   |
      | an invalid selected provider   |
