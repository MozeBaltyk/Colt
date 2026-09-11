# Project Health Specification

**Milestone 5 is planned and not implemented.** Its scenarios describe intended acceptance behavior, not current command or release availability.

``` text
colt check [--all]
```

Without `--all`, Colt checks the current repository. With `--all`, it checks the repositories managed by the declared workspace.

Health policy is concise, declarative YAML:

``` yaml
policy:
  repository:
    require: [README.md, LICENSE]
    default_branch: main
    allowed_visibility: [private, internal]
```

| ID                  | Planned requirement                                                                                                                                                                                                                                                       | Acceptance specification                                          |
|:--------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------|
| `HEALTH-CHECK-001`  | `colt check [--all]` **MUST** check repository-local Git identity, expected origin/provider, remote existence and default branch, worktree cleanliness, required-file policy, and visibility policy for the selected scope.                                               | [`project_health.feature`](../../features/project_health.feature) |
| `HEALTH-POLICY-001` | Health policy **MUST** strictly parse only the documented data-only fields. Invalid or unknown input **MUST** fail deterministically, and required-file paths **MUST** remain repository-relative and confined beneath the repository root.                               | [`project_health.feature`](../../features/project_health.feature) |
| `HEALTH-OUTPUT-001` | Findings **MUST** be deterministic. Exit status **MUST** be 0 when healthy, 1 for policy or drift findings, and 2 for operational or configuration errors.                                                                                                                | [`project_health.feature`](../../features/project_health.feature) |
| `HEALTH-SAFETY-001` | Checks **MUST** be diagnostic only and **MUST NOT** auto-fix, execute repository content, or mutate source/provider state. Output and errors **MUST** be bounded and redacted and **MUST NOT** expose credentials, provider response bodies, or repository file contents. | [`project_health.feature`](../../features/project_health.feature) |

Auto-remediation is deferred. Implementation-level filesystem race hardening should be specified when this milestone becomes active.
