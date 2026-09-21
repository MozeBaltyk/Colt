Feature: Declarative workspace reconciliation
   Milestone 4 behavior is exercised with deterministic provider fakes.
   Live provider mirroring remains gated integration coverage.

   @WORKSPACE-MANIFEST-001
   Scenario: Expand bounded repository selections deterministically
     Given a strict data-only YAML document has a top-level workspace mapping with repository selections
     And each repository selection names a provider alias and namespace
     And one selection includes named repositories while another omits include
     When Colt resolves the desired workspace
     Then the first selection contains only its include list
     And the second contains all visible repositories in its selection
     And every desired repository identity and local path is unique and deterministic

   @WORKSPACE-MANIFEST-001
   Scenario: Reject parser-invalid workspace YAML
     Given workspace YAML has a duplicate mapping key
     When Colt parses the manifest
     Then the manifest fails before provider resolution or local access

   @WORKSPACE-MANIFEST-001
   Scenario: Reject an unsafe derived workspace path
     Given valid workspace YAML includes a traversal repository name
     When Colt parses the manifest
     Then the manifest fails before provider resolution or local access

   @WORKSPACE-STATUS-001
   Scenario: Report workspace status without mutation
     Given desired repositories are present, missing locally, absent remotely, and mismatched
     When I execute `colt status`
     Then deterministic results categorize them as present, to-clone, absent remotely, and remote/config mismatch
     And no local, remote, or configuration state is changed

   @WORKSPACE-SYNC-001
   Scenario: Reconcile independent repositories
     Given desired repositories include an existing repository, two missing repositories, an inconsistent repository, and a remotely absent repository
     And one missing repository cannot be cloned
     When I execute `colt sync`
     Then the other missing repository is cloned
     And existing, inconsistent, remotely absent, and failed repositories are summarized
     And completed clones are retained

   @WORKSPACE-DRYRUN-001
   Scenario: Dry-run computes the same plan without mutation
     Given reconciliation requires remote metadata reads and a missing repository clone
     When I execute `colt sync --dry-run`
     Then Colt reports the same plan as `colt sync`
     And remote metadata may be read
     And no local, remote, or configuration state is changed

   @WORKSPACE-SAFETY-001
   Scenario Outline: Workspace commands preserve existing repositories and paths
     Given a derived path initially validates beneath the workspace root
     And the workspace root is swapped to an escaping symlink before root-relative access
     And the workspace has an unrelated non-empty path and a dirty or diverged repository with a mismatched origin
     When I execute `<command>`
     Then root-relative no-follow access fails closed without outside-root access or mutation
     And Colt does not overwrite, prune, reset, or delete existing paths
     And Colt does not rewrite the repository origin
     And each inconsistency is reported

     Examples:
       | command             |
       | colt status         |
       | colt sync           |
       | colt sync --dry-run |

    @MIRROR-001
    Scenario: Mirror lists source repositories, clones, creates target, pushes, cleans up
      Given a configured fake local Gitea or Forgejo target
      And the source provider namespace has three repositories
      When I execute `colt mirror github local --namespace source`
      Then all three source repositories are listed
      And each repository is cloned into a temporary directory
      And the repository is created on the target provider
       And a mirror push of all branches and tags is requested for the target
      And the temporary directories are removed

    @MIRROR-002
    Scenario: Mirror independent failures do not abort unrelated mirrors
      Given a configured fake local Gitea or Forgejo target
      And the source provider namespace has two repositories
      And one repository cannot be cloned
      When I execute `colt mirror github local`
      Then the other repository is mirrored successfully
      And the failed repository is reported
      And completed mirrors are retained

    @MIRROR-003
    Scenario Outline: Mirror target conflicts fail safely without --replace
      Given a configured fake local Gitea or Forgejo target
      And the target provider already has repository "demo"
      When I execute `colt mirror <source> local`
      Then the command fails without overwriting the existing target
      And the existing target repository is unchanged

      Examples:
        | source      |
        | github      |
        | gitlab      |

    @MIRROR-003
    Scenario: Mirror --replace explicitly enables replacement
      Given a configured fake local Gitea or Forgejo target
      And the target provider already has repository "demo"
      When I execute `colt mirror github local --replace`
      Then the target repository is replaced
      And unrelated state is not silently overwritten

    @MIRROR-004
    Scenario: Mirror is one-way and source and target remain independent
      Given a configured fake local Gitea or Forgejo target
      And the source provider has repository "demo"
      When I execute `colt mirror github local`
      Then the mirror completes successfully
       And the command performs no background propagation after it returns
      And another explicit `colt mirror` invocation is required

    @MIRROR-005
    Scenario: Mirror target remotes are credential-free
      Given a configured fake local Gitea or Forgejo target
      And the source provider namespace has repository "demo"
      When I execute `colt mirror github local`
      Then the target remote URL contains no credentials
      And the target provider URL is used
      And the source provider URL is not retained as the target push remote
