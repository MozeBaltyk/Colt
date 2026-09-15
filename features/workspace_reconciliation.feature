Feature: Planned declarative workspace reconciliation
   Milestone 4 behavior is specified but not implemented or released.
   Requires M7 (colt run gitea|forgejo) for local-provider sync scenarios.

   @planned @WORKSPACE-MANIFEST-001
   Scenario: Expand bounded repository selections deterministically
     Given a strict data-only YAML document has a top-level workspace mapping with repository selections
     And each repository selection names a provider alias and namespace
     And one selection includes named repositories while another omits include
     When Colt resolves the desired workspace
     Then the first selection contains only its include list
     And the second contains all visible repositories in its selection
     And every desired repository identity and local path is unique and deterministic

   @planned @WORKSPACE-MANIFEST-001
   Scenario: Reject an invalid workspace manifest
     Given workspace YAML has a parser violation such as multiple documents, a disallowed node or field, duplicate key, alias or anchor, merge key, or custom tag
     And it has a resource-limit or path violation such as excessive bytes, depth, or collection size, or a duplicate, absolute, traversal, or escaping path
     When Colt parses the manifest
     Then parser violations fail before expansion or repository resolution
     And path violations fail before local reads or writes

   @planned @WORKSPACE-STATUS-001
   Scenario: Report workspace status without mutation
     Given desired repositories are present, missing locally, absent remotely, and mismatched
     When I run `colt status`
     Then deterministic results categorize them as present, to-clone, absent remotely, and remote/config mismatch
     And no local, remote, or configuration state is changed

   @planned @WORKSPACE-SYNC-001
   Scenario: Reconcile independent repositories
     Given desired repositories include an existing repository, two missing repositories, an inconsistent repository, and a remotely absent repository
     And one missing repository cannot be cloned
     When I run `colt sync`
     Then the other missing repository is cloned
     And existing, inconsistent, remotely absent, and failed repositories are summarized
     And completed clones are retained

   @planned @WORKSPACE-DRYRUN-001
   Scenario: Dry-run computes the same plan without mutation
     Given reconciliation requires remote metadata reads and a missing repository clone
     When I run `colt sync --dry-run`
     Then Colt reports the same plan as `colt sync`
     And remote metadata may be read
     And no local, remote, or configuration state is changed

   @planned @WORKSPACE-SAFETY-001
   Scenario Outline: Workspace commands preserve existing repositories and paths
     Given a derived path initially validates beneath the workspace root
     And an ancestor is swapped to an escaping symlink between validation and an actual read or write
     And the workspace has an unrelated non-empty path and a dirty or diverged repository with a mismatched origin
     When I run `<command>`
     Then root-relative no-follow access fails closed without outside-root access or mutation
     And Colt does not overwrite, prune, reset, or delete existing paths
     And Colt does not rewrite the repository origin
     And each inconsistency is reported

     Examples:
       | command             |
       | colt status         |
       | colt sync           |
       | colt sync --dry-run |

    @planned @MIRROR-001
    Scenario: Mirror lists source repositories, clones, creates target, pushes, cleans up
      Given M7 has deployed a local Gitea or Forgejo instance via `colt run`
      And the source provider namespace has three repositories
      When I run `colt mirror <source> <local-alias> --namespace <ns>`
      Then all three source repositories are listed
      And each repository is cloned into a temporary directory
      And the repository is created on the target provider
      And all branches and tags are pushed to the target
      And the temporary directories are removed

    @planned @MIRROR-002
    Scenario: Mirror independent failures do not abort unrelated mirrors
      Given M7 has deployed a local Gitea or Forgejo instance
      And the source provider namespace has two repositories
      And one repository cannot be cloned
      When I run `colt mirror <source> <local-alias>`
      Then the other repository is mirrored successfully
      And the failed repository is reported
      And completed mirrors are retained

    @planned @MIRROR-003
    Scenario Outline: Mirror target conflicts fail safely without --replace
      Given M7 has deployed a local Gitea or Forgejo instance
      And the target provider already has repository "demo"
      When I run `colt mirror <source> <local-alias>`
      Then the command fails without overwriting the existing target
      And the existing target repository is unchanged

      Examples:
        | source      |
        | github      |
        | gitlab      |

    @planned @MIRROR-003
    Scenario: Mirror --replace explicitly enables replacement
      Given M7 has deployed a local Gitea or Forgejo instance
      And the target provider already has repository "demo"
      When I run `colt mirror <source> <local-alias> --replace`
      Then the target repository is replaced
      And unrelated state is not silently overwritten

    @planned @MIRROR-004
    Scenario: Mirror is one-way and source and target remain independent
      Given M7 has deployed a local Gitea or Forgejo instance
      And the source provider has repository "demo"
      When I run `colt mirror <source> <local-alias>`
      Then the mirror completes successfully
      And a later change on the source does not automatically propagate to the target
      And another explicit `colt mirror` invocation is required

    @planned @MIRROR-005
    Scenario: Mirror target remotes are credential-free
      Given M7 has deployed a local Gitea or Forgejo instance
      And the source provider namespace has repository "demo"
      When I run `colt mirror <source> <local-alias>`
      Then the target remote URL contains no credentials
      And the target provider URL is used
      And the source provider URL is not retained as the target push remote
