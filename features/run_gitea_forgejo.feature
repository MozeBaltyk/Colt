@orchestration
Feature: Deterministic orchestration of a Gitea or Forgejo deployment
   Provisions systemd-managed podman containers for self-hosted
   Gitea/Forgejo. Host-level; orthogonal to provider API auth.
   M7 prerequisite for M4 local-provider sync (Colt mirror / colt sync).
   These scenarios use an in-memory host, not real Podman or systemd.
   Resource and activation assertions describe simulated orchestration only.

   @RUN-001 @RUN-002 @RUN-003 @RUN-004
   Scenario: Deploy gitea with defaults
    Given podman and systemd are available on the host
    And no existing deployment named "personal"
    When I run `colt run gitea personal --password S3cret!`
    Then the three units are created under /etc/systemd/system/
      | unit |
      | podman-network-personal-net.service |
      | container-personal-db.service |
      | container-personal-app.service |
    And the podman network personal-net exists
    And volumes personal-data, personal-config, personal-db exist
    And all three units are enabled and active
    And the env file /etc/colt/run/personal/env has mode 0600
    And no password appears in any unit file as a literal

   @RUN-011 @RUN-012
   Scenario: Gitea startup is health gated
    Given podman and systemd are available on the host
    And no existing deployment named "personal"
    When I run `colt run gitea personal --password S3cret!`
    Then the database unit declares an engine-appropriate health command
    And the app unit waits for the database to become healthy

   @RUN-005
   Scenario: Generate application key when omitted on a terminal
    Given stdin is a terminal
    When I run `colt run gitea personal`
    Then a private application key is generated

   @RUN-005
   Scenario: Noninteractive mode generates an application key
    Given standard input is not a terminal
    When I run `colt run gitea personal`
    Then a private application key is generated

   @RUN-006 @RUN-004
   Scenario: --replace redeploys an existing deployment
    Given a deployment named "personal" is active
    When I run `colt run gitea personal --replace`
    Then the old app container is stopped and removed
    And replacement preserves the application key
    And the deployment is active

   @RUN-006
   Scenario: Deploy without --replace on existing name fails safely
    Given a deployment named "personal" is active
    When I run `colt run gitea personal --password X`
    Then the command fails
    And the prior deployment remains active unchanged

   @RUN-009
   Scenario: Override app image
    When I run `colt run gitea personal --password X --image docker.io/gitea/gitea:1.21.4-rootless`
    Then container-personal-app.service uses image docker.io/gitea/gitea:1.21.4-rootless

   @RUN-007
   Scenario: Status reports deployment state
    Given a deployment named "personal" is active
    When I run `colt run status personal`
    Then output contains "active", the image ref, ports 3000 and 2222, and volume paths

   @RUN-007
   Scenario: Status discovers managed and legacy partial deployments
    Given a deployment named "personal" is active
    And a legacy partial deployment named "gateau" has no network unit
    When I run `colt run status`
    Then status lists "gateau" before "personal" with gateau legacy read-only and its network absent

   @RUN-008 @RUN-015
   Scenario: Stop and start cycle preserves data
    Given a deployment named "personal" is active
    When I run `colt run stop personal`
    Then container-personal-app.service is inactive
    And I run `colt run start personal`
    Then the app unit is active again
    And named volumes are intact

   @RUN-008
   Scenario: rm removes units and containers but keeps volumes
    Given a deployment named "personal" is active
    When I run `colt run rm personal`
    Then units are disabled and removed
    And containers are removed
    And volumes personal-data, personal-config, personal-db still exist

   @RUN-008
   Scenario: rm --volumes removes everything
    Given a deployment named "personal" is active
    When I run `colt run rm personal --volumes`
    Then volumes personal-data, personal-config, personal-db are removed

   @RUN-010
   Scenario: Missing podman fails at the lowest layer
    Given podman is not on PATH
    When I run `colt run gitea personal --password X`
    Then the command fails
    And the error names podman as the missing runtime
    And no units are created

   @forgejo @RUN-001 @RUN-011 @RUN-012
   Scenario: Deploy forgejo uses postgres
    Given podman and systemd are available on the host
    When I run `colt run forgejo personal --password X`
    Then container-personal-db.service uses the postgres image
    And the env file sets DB_TYPE=postgres
    And the database unit declares an engine-appropriate health command
    And the app unit waits for the database to become healthy

   @RUN-014
   Scenario Outline: Advertise an externally terminated HTTPS URL
    Given podman and systemd are available on the host
    And no existing deployment named "personal"
    When I run `colt run <type> personal --password X --external-url https://git.example.com`
    Then the <type> deployment advertises https://git.example.com/ while listening over container HTTP
    And output contains the browser URL and a shell-safe <type> onboarding template
    And no token or administrator password is printed

    Examples:
      | type    |
      | gitea   |
      | forgejo |

   @RUN-014
   Scenario: Invalid external URL fails before host mutation
    When I run `colt run gitea personal --password X --external-url http://git.example.com`
    Then the command fails with an invalid-external-URL error
    And no units, networks, or volumes are created

   @RUN-013
   Scenario: Non-root deployment fails before host mutation
    Given the current user is not root
    When I run `colt run gitea personal --password X`
    Then the command fails with an actionable error suggesting sudo
    And no units, networks, or volumes are created
