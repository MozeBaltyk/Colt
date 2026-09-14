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

   @planned @WORKSPACE-SYNC-001
   Scenario: Mirror a repository to a local Gitea/Forgejo instance
     Given M7 has deployed a local Gitea or Forgejo instance via `colt run`
     And the local instance is configured as a provider alias
     When I run `colt mirror <source> <local-alias>`
     Then the repository is cloned from the source provider and pushed to the local instance
     And the local instance contains the same branches and commits
     And the local origin is updated to the local instance URL
