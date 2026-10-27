Feature: Declarative workspace reconciliation
   Milestone 4 behavior is exercised with deterministic provider fakes.
   Live provider mirroring remains gated integration coverage.

   @WORKSPACE-MANIFEST-001
   Scenario: Expand bounded named workspaces deterministically
     Given a strict data-only YAML document has a top-level workspaces mapping with named paths and repository selections
     And each repository selection names a provider alias and namespace
     And one selection includes named repositories while another omits include
     When Colt resolves the desired workspaces
     Then the first selection contains only its include list
     And the second contains all visible repositories in its selection
     And every desired repository identity and local path is unique and deterministic

   @WORKSPACE-MANIFEST-001
   Scenario: Reject the removed singular workspace format
     Given configuration uses the former singular workspace mapping
     When Colt parses the manifest
     Then migration to a named workspace is required before provider or local access

   @WORKSPACE-SELECTION-001
   Scenario: Reconcile all named workspaces or one explicit workspace
     Given two named workspaces have independent paths and missing repositories
     When I execute `colt sync`
     Then both workspaces are reconciled in deterministic name order
     And every result identifies its workspace
     When I execute `colt sync --workspace factory`
     Then only the factory workspace is reconciled

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

   @WORKSPACE-SYNC-001
   Scenario: Sync an ad-hoc namespace and save its flat workspace
     Given another provider namespace exposes two repositories
     When I sync that namespace to an explicit path and save it as workspace factory
     Then both repositories are cloned directly beneath the explicit path
     And the absolute path and namespace selection are saved under workspace factory
     When I remove one repository and execute saved `colt sync` from another directory
     Then the missing repository is restored in the saved workspace

   @WORKSPACE-DRYRUN-001
   Scenario: Dry-run computes the same plan without mutation
     Given reconciliation requires remote metadata reads and a missing repository clone
     When I execute `colt sync --dry-run`
     Then Colt reports the same plan as `colt sync`
     And remote metadata may be read
     And no local, remote, or configuration state is changed

   @WORKSPACE-REFRESH-001
   Scenario: Fetch present repositories without changing their worktrees
     Given a workspace has two present repositories and one missing repository
     And one present repository cannot be fetched
     When I execute `colt sync --fetch`
     Then the other present repository is fetched with pruning
     And the missing repository is cloned without a redundant fetch
     And the failed fetch is summarized without changing either worktree

   @WORKSPACE-PULL-001
   Scenario: Pull present repositories with safe fast-forward-only semantics
     Given a workspace has clean, dirty, diverged, and detached present repositories
     When I execute `colt sync --pull`
     Then the clean repository is pulled from its matching origin branch
     And dirty, diverged, and detached repositories are not changed
     And independent pull failures are summarized

   @WORKSPACE-REFRESH-001 @WORKSPACE-PULL-001
   Scenario Outline: Refresh dry-run reports intent without Git mutation
     Given a workspace has a present repository
     When I execute `colt sync <mode> --dry-run`
     Then Colt reports `<action>` for the present repository
     And no local, remote, or configuration state is changed

     Examples:
       | mode    | action      |
       | --fetch | would-fetch |
       | --pull  | would-pull  |

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

    @MIRROR-006
    Scenario: Mirror --target-namespace redirects creation to another namespace
      Given a configured fake local Gitea or Forgejo target
      And the source provider namespace has repository "demo"
      When I execute `colt mirror github local --target-namespace other-group`
      Then the target repository is created in namespace other-group
      And the mirror push targets the other-group namespace

    @MIRROR-007
    Scenario: Mirror --repository mirrors only the named repository
      Given a configured fake local Gitea or Forgejo target
      And the source provider namespace has three repositories
      When I execute `colt mirror github local --repository repo2`
      Then only repository repo2 is mirrored
      And the other repositories are not mirrored
