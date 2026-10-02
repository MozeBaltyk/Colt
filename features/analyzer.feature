Feature: Analyzer
  Colt inventories a repository read-only and renders reports from a canonical inventory.yaml.

  @ANALYZER-COLLECT-001 @ANALYZER-SCHEMA-001 @ANALYZER-SAFETY-001
  Scenario: Inventory detects ecosystems without executing repository content
    Given an analyzed repository containing manifest files
    And an executable script that would fail if executed
    When I run `colt analyze --format yaml`
    Then the inventory lists the go, node, and rust ecosystems with their manifest files
    And no repository content was executed

  @ANALYZER-REPORT-001 @ANALYZER-SAFETY-001
  Scenario: The inventory file is the canonical source of every report
    Given an analyzed repository containing a secret marker file
    And the repository origin is "https://user:secret-token@git.example.com/ns/demo.git"
    When I run `colt analyze --format yaml --output inventory.yaml`
    Then the inventory does not expose the repository secret or the origin credential
    And the written inventory reproduces the yaml view