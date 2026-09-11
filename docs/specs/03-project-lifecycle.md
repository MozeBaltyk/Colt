# Project Lifecycle Specification

This later milestone is design-level and uses normal provider resolution and the
shared identity, safety, conflict, and partial-failure rules in [shared
core](00-core.md). It does not define production acceptance criteria.

## Repository Operations

```text
colt list
colt list --provider <alias>
colt list --all
colt clone <repository> [--provider <alias>]
colt sync [--namespace <namespace>] [--provider <alias>]
```

`colt list` and `colt list --provider` query one provider using normal provider
resolution. Only explicit `--all` queries all configured providers. Results
identify provider alias, namespace, repository, visibility, and clone URL.

`colt clone` clones one unambiguously resolved repository into the managed
workspace and applies its repository-local identity. `colt sync` reconciles
missing workspace repositories from the selected provider, optionally limited
to a namespace. Neither command alters existing destinations implicitly;
independent sync failures are summarized.

## Minimum Release Flow

The initial provider-independent release flow is:

```text
validate repository
-> validate version and tag
-> create local tag
-> push tag
-> create provider release
```

Normal provider resolution applies. Native Git and provider APIs remain
authoritative for conflicts. Advanced release notes, changelog integration,
signed tags, and rollback automation are deferred.
