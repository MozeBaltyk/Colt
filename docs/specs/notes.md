> **Non-normative design input:** These notes have been incorporated into the
> linked [product and capability specifications](product.md). If they differ,
> the source-of-truth order in the product specification applies.

# Native Provider Integration

Colt MUST NOT delegate hosting-provider functionality to `gh`, `glab`, or `curl`.

GitHub and GitLab integrations MUST be implemented directly through their HTTP APIs. The native `git` executable remains the only required external executable for Git repository operations.

---

# Provider Resolution

Provider resolution MUST remain provider-independent. No provider type receives special fallback behavior.

Use the following precedence:

1. Explicit `--provider <alias>`.
2. Configured default provider.
3. The only configured provider, when exactly one exists.
4. Otherwise, fail and require explicit provider selection or configuration of a default.

Remove the GitHub-specific fallback rule.

---

# Separate Provider Concerns

Provider configuration currently combines account, namespace, Git identity, and repository defaults. These concepts SHOULD remain logically distinct even if the MVP stores them in a single configuration structure.

The architecture MUST NOT assume:

```text
1 account = 1 namespace = 1 identity
```

The design should allow future support for:

* one GitHub account accessing multiple organizations;
* one GitLab account accessing multiple groups or subgroups;
* one Git identity being used by multiple providers;
* namespace-specific defaults such as repository visibility;
* repositories outside a provider's default namespace.

Avoid introducing additional configuration objects until they are required by actual functionality.

---

# Namespace Terminology

Use `namespace` as the provider-independent term instead of `group`.

The common hierarchy is:

```text
provider
namespace
repository
```

Provider implementations translate native concepts into this model:

```text
GitHub owner/organization       → namespace
GitLab group/subgroup/namespace → namespace
```

Provider-specific terminology SHOULD remain inside provider implementations where possible.

---

# Consistent Provider Resolution

Absence of `--provider` SHOULD have consistent semantics across commands.

`colt list` MUST NOT implicitly switch from normal provider resolution to querying every configured provider.

Use explicit behavior:

```bash
colt list
colt list --provider work
colt list --all
```

`colt list` follows normal provider resolution. `--all` explicitly requests repositories from all configured providers.

---

# Bulk Repository Synchronization

Avoid overloading `clone` with the special project name `all`.

Instead of:

```bash
colt clone all [group]
```

use a distinct workspace synchronization operation:

```bash
colt clone <repository>
colt sync
colt sync --namespace infrastructure
```

`clone` operates on one repository. `sync` reconciles the local workspace with repositories available from the selected provider or namespace.

---

# Template Initialization

Template support SHOULD follow the basic initialization MVP rather than being required to validate the core provider architecture.

Prefer a single extensible initialization command:

```bash
colt init foo
colt init foo --template general
```

The first form creates a blank repository. The second initializes the repository from a configured template.

This avoids introducing separate commands such as:

```text
init blank
init general
init python
init helm
...
```

and provides a natural path toward custom templates.

Introduce an intermediate milestone for template functionality if necessary.

---

# Non-Destructive Operations

Detailed rollback and partial-failure requirements SHOULD be centralized instead of repeated across capability specifications.

Use the following shared rule:

> Colt operations are non-destructive by default. Colt never deletes or replaces existing local or remote state as automatic rollback. On partial failure, Colt preserves completed work and reports the resulting state.

Capability specifications SHOULD define additional failure behavior only where the shared rule is insufficient.

---

# Provider Conflict Handling

Preflight checks reduce avoidable failures but cannot guarantee that remote state remains unchanged between validation and mutation.

Use the following rule:

> Colt performs inexpensive preflight checks where useful, but provider mutations remain authoritative and must safely handle conflict responses.

Provider API conflict responses MUST therefore be handled correctly even when a previous existence check succeeded.

---

# Credential Management

Cross-platform operating-system credential-store integration is not required for the initial MVP.

Initially support credentials through:

1. Environment variables.
2. Configuration references to environment variables.

For example:

```yaml
providers:
  work:
    type: gitlab
    token_env: GITLAB_TOKEN
```

Tokens MUST NOT be stored directly in normal Colt configuration or exposed in output.

Native credential-store integration may be added later without changing the provider abstraction.

---

# Analyzer Scope

The analyzer specification is too detailed for its current roadmap position and SHOULD be reduced until the core project-management capabilities are established.

The initial analyzer design SHOULD describe its purpose and architectural boundaries rather than fully specifying Helm rendering, dependency traversal, image resolution, registry classification, size calculation, findings, coverage metrics, multiple reports, and graph generation.

Keep the canonical inventory concept:

```text
repository
    ↓
 analyzer
    ↓
inventory.yaml
    ↓
 report/rendering
```

`inventory.yaml` remains the source of truth. Reports are derived representations.

Detailed analyzer behavior SHOULD be specified when the analyzer becomes an active implementation milestone.

---

# Release Scope

The release workflow SHOULD initially implement the minimum reliable provider-independent operation.

Initial flow:

```text
validate repository
→ validate version/tag
→ create local tag
→ push tag
→ create provider release
```

Git and provider APIs remain authoritative for conflicts and race conditions.

The implementation MUST preserve completed state after partial failure according to the shared non-destructive operations rule. More advanced release functionality can be added after the basic workflow is established.

---

# Progressive Specification Detail

Specification detail SHOULD correspond to implementation priority.

Use approximately:

```text
Milestone 1 → normative and detailed
Milestone 2 → design-level specification
Milestone 3 → concise capability intent
Future      → goals and constraints only
```

Later milestones SHOULD NOT receive production-level acceptance criteria before their architecture and requirements have been validated through earlier implementation work.

---

# Core Product Definition

Use the following as the primary definition of Colt:

> Colt is a provider-independent Git project CLI that talks directly to hosting-provider APIs and uses native Git for repository operations.

The architecture is:

```text
                         Colt CLI
                            │
        ┌───────────────────┼──────────────────┐
        │                   │                  │
     Config              Git Ops          Provider Ops
        │                   │                  │
        │                  git         ┌────────┴────────┐
        │                              │                 │
        │                         GitHub API         GitLab API
        │
     identity
     providers
     workspace
```

Provider-independent operations SHOULD depend on a common provider interface:

```text
Provider
├── Authenticate()
├── GetRepository()
├── CreateRepository()
├── ListRepositories()
├── CreateRelease()
└── CloneURL()
```

Provider-specific implementations provide that interface:

```text
GitHubProvider
GitLabProvider
```

Provider functionality MUST NOT be implemented by shelling out to provider-specific command-line tools:

```text
exec("gh ...")    // prohibited
exec("glab ...")  // prohibited
exec("curl ...")  // prohibited
```

Using the native `git` executable for Git repository operations remains intentional. Colt owns the hosting-provider integration while Git remains responsible for Git repository mechanics.

This boundary provides provider autonomy without requiring Colt to reimplement Git.
