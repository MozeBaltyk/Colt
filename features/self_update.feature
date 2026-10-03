Feature: Self-update
  Colt upgrades itself from the official release channel, or reports what an update would change.

  @UPDATE-CHECK-001
  Scenario: Checking reports the current and latest versions without changing anything
    Given the release channel advertises latest "v0.5.0"
    When I run `colt update --check`
    Then it reports the current version "v0.4.0" and the available version "v0.5.0"
    And the current binary is unchanged

  @UPDATE-APPLY-001
  Scenario: Update replaces the binary with the latest verified release
    Given the release channel advertises latest "v0.5.0" with a valid artifact
    When I run `colt update`
    Then the current binary is replaced with the new release

  @UPDATE-SAFETY-001
  Scenario: Update verifies integrity before replacement and never leaks the artifact
    Given the release channel advertises latest "v0.5.0" with a tampered artifact
    When I run `colt update`
    Then the command fails and the current binary is unchanged
    And the failure does not expose the downloaded artifact