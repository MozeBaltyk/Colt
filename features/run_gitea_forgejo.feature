Feature: Run / deploy a permanent Gitea or Forgejo server
   Provisions systemd-managed podman containers for self-hosted
   Gitea/Forgejo. Host-level; orthogonal to provider API auth.
   M7 prerequisite for M4 local-provider sync (Colt mirror / colt sync).
   Planned — scenarios do not represent current command availability.

   @planned @RUN-001 @RUN-002 @RUN-003
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

   @planned @RUN-005
   Scenario: Prompt for password when omitted and stdin is a terminal
    Given stdin is a terminal
    And the prompt reads "App password for personal:"
    When I run `colt run gitea personal` and enter "S3cret!"
    Then the deployment succeeds and the env file contains GITEA_PASSWORD=S3cret!

   @planned @RUN-005
   Scenario: Noninteractive mode without password fails
    Given standard input is not a terminal
    When I run `colt run gitea personal`
    Then the command fails with a missing-password error
    And no unit files are created

   @planned @RUN-006 @RUN-004
   Scenario: --replace redeploys an existing deployment
    Given a deployment named "personal" is active
    When I run `colt run gitea personal --replace --password N3w!`
    Then the old app container is stopped and removed
    And new units are written with the new password
    And the deployment is active

   @planned @RUN-006
   Scenario: Deploy without --replace on existing name fails safely
    Given a deployment named "personal" is active
    When I run `colt run gitea personal --password X`
    Then the command fails
    And the prior deployment remains active unchanged

   @planned @RUN-009
   Scenario: Override app image
    When I run `colt run gitea personal --password X --image docker.io/gitea/gitea:1.21.4-rootless`
    Then container-personal-app.service contains --image docker.io/gitea/gitea:1.21.4-rootless

   @planned @RUN-007
   Scenario: Status reports deployment state
    Given a deployment named "personal" is active
    When I run `colt run status personal`
    Then output contains "active", the image ref, ports 3000 and 2222, and volume paths

   @planned @RUN-008
   Scenario: Stop and start cycle preserves data
    Given a deployment named "personal" is active
    When I run `colt run stop personal`
    Then container-personal-app.service is inactive
    And I run `colt run start personal`
    Then the app unit is active again
    And named volumes are intact

   @planned @RUN-008
   Scenario: rm removes units and containers but keeps volumes
    Given a deployment named "personal" is active
    When I run `colt run rm personal`
    Then units are disabled and removed
    And containers are removed
    And volumes personal-data, personal-config, personal-db still exist

   @planned @RUN-008
   Scenario: rm --volumes removes everything
    Given a deployment named "personal" is active
    When I run `colt run rm personal --volumes`
    Then volumes personal-data, personal-config, personal-db are removed

   @planned @RUN-010
   Scenario: Missing podman fails at the lowest layer
    Given podman is not on PATH
    When I run `colt run gitea personal --password X`
    Then the command fails
    And the error names podman as the missing runtime
    And no units are created

   @planned @forgejo
   Scenario: Deploy forgejo uses postgres
    Given podman and systemd are available
    When I run `colt run forgejo personal --password X`
    Then container-personal-db.service uses the postgres image
    And the env file sets DB_TYPE=postgres
