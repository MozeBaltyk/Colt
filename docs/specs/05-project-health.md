# Project Health Specification

**Milestone 5 is implemented.** Acceptance uses deterministic fake provider clients; live provider coverage is not claimed.

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

| ID                  | Requirement                                                                                                                                                                                                                                                               | Acceptance specification                                          |
|:--------------------|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------|
| `HEALTH-CHECK-001`  | `colt check [--all]` **MUST** check repository-local Git identity, expected origin/provider, remote existence and default branch, worktree cleanliness, required-file policy, and visibility policy for the selected scope.                                               | [`project_health.feature`](../../features/project_health.feature) |
| `HEALTH-POLICY-001` | Health policy **MUST** strictly parse only the documented data-only fields. Invalid or unknown input **MUST** fail deterministically, and required-file paths **MUST** remain repository-relative and confined beneath the repository root.                               | [`project_health.feature`](../../features/project_health.feature) |
| `HEALTH-OUTPUT-001` | Findings **MUST** be deterministic. Exit status **MUST** be 0 when healthy, 1 for policy or drift findings, and 2 for operational or configuration errors.                                                                                                                | [`project_health.feature`](../../features/project_health.feature) |
| `HEALTH-SAFETY-001` | Checks **MUST** be diagnostic only and **MUST NOT** auto-fix, execute repository content, or mutate source/provider state. Output and errors **MUST** be bounded and redacted and **MUST NOT** expose credentials, provider response bodies, or repository file contents. | [`project_health.feature`](../../features/project_health.feature) |

`policy` is optional for compatibility with older configurations, but `colt check` requires it. When present, `policy` contains exactly `repository`, and `repository` contains exactly the three fields shown above. All three fields are required. `require` accepts at most 256 unique paths, each at most 1024 bytes and 32 slash-separated segments. Paths must be clean, non-empty, repository-relative slash paths; absolute paths, backslashes, controls, empty/`.`/`..`/`.git` segments, and normalization changes are rejected. `default_branch` is a valid Git branch name of at most 255 bytes. `allowed_visibility` contains one to three unique values from `private`, `internal`, and `public`.

Checks open one `os.Root` per repository, reject linked ancestors, and verify the opened regular file is the object inspected. Git inspection validates local configuration first, disables optional locks and executable/configurable helpers where relevant, and emits no child output. Findings use only bounded allowlisted labels and are sorted. Auto-remediation remains deferred.
