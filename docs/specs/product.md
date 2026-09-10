# Colt Specification

## 1. Overview

Colt is a provider-agnostic Git project management CLI implemented in Go using Cobra.

It manages project workflows across GitHub and GitLab while delegating Git repository operations to the native `git` executable.

Initial scope:

- provider configuration and authentication;
- repository discovery;
- project creation;
- template-based bootstrapping;
- structured cloning;
- repository-local Git identity;
- project analysis and Helm analysis;
- Git tags and provider releases.

Operator syntax is in [02-usage.md](../procedures/02-usage.md). 
Unresolved work is tracked only in [TODO.md](../../TODO.md).

## 2. Design Principles

### Provider-agnostic commands

User-facing commands should remain provider-independent. GitHub/GitLab differences belong behind provider interfaces.

### Git remains Git

Use the installed `git` executable for Git operations such as init, clone, fetch, status, local config, tags, and pushes.

### Direct provider APIs

Communicate directly with GitHub and GitLab APIs from Go. `gh`, `glab`, and `curl` are not required runtime dependencies.

### Local Git identity

For repositories Colt manages, configure:

```bash
git config --local user.name "<name>"
git config --local user.email "<email>"
```

using the identity associated with the selected provider.

### Technology-agnostic templates

Project types should be configuration-driven. Adding a template should normally not require adding provider-specific Cobra logic.

## 3. Terminology

- **Provider:** a configured Git hosting account or instance, identified by an alias such as `personal` or `work`.
- **Provider type:** initially `github` or `gitlab`.
- **Project:** a Git repository managed or discovered by Colt.
- **Group:** a repository namespace, such as a GitLab group or GitHub organization/owner.
- **Template:** a Git repository used to bootstrap a project.
- **Analyzer:** a component that inspects a project without modifying it.

## 4. CLI

### Authentication

```text
colt auth add <github|gitlab>
```

Configure provider type, host, alias, owner/group, authentication, Git identity, and default visibility. Self-hosted GitLab must support a custom host.

Possible future commands:

```text
colt auth list
colt auth remove <provider>
colt auth status [provider]
```

### List

```text
colt list
```

List configured providers and repositories. Output must identify provider aliases to avoid ambiguity.

### Initialize blank project

```text
colt init blank <project> [--local]
```

Remote workflow:

1. Resolve provider.
2. Create project directory.
3. Initialize Git.
4. Configure local Git identity.
5. Create remote repository.
6. Configure remote.
7. Push when appropriate.

With `--local`, do not create or push a remote repository.

### Initialize templated project

```text
colt init general <project> [--local]
```

Resolve `general` through configuration:

```yaml
templates:
  general:
    repository: "https://github.com/MozeBaltyk/project-template"
```

The resulting project must not accidentally retain the template repository as its project remote.

### Clone project

```text
colt clone <project>
```

Locate, clone, preserve workspace structure, and apply the provider's local Git identity.

If a project name is ambiguous, Colt must require qualification or prompt interactively. A future canonical reference may be:

```text
<provider>:<group>/<project>
```

### Clone all

```text
colt clone all [group]
```

Retrieve repositories from the selected/configured provider, optionally filter by group, clone missing projects, preserve structure, and configure the correct local identity. Existing projects must not be destructively overwritten.

### Release

```text
colt release <project> <version>
```

Workflow:

1. Resolve project.
2. Verify local repository.
3. Verify acceptable working-tree state.
4. Validate version/tag.
5. Ensure tag does not already exist.
6. Create tag.
7. Push tag.
8. Create GitHub/GitLab release through the provider API.

Colt should fail before mutating remote state whenever preconditions are not satisfied.

## 5. Analyzer System

Conceptual interface:

```go
type Analyzer interface {
    Name() string
    Detect(ctx context.Context, path string) (bool, error)
    Analyze(ctx context.Context, path string) (*Analysis, error)
}
```

Analyzers must not modify projects.

### Helm Analyzer

Initial Helm detection should recognize standard chart structure such as `Chart.yaml`.

Potential output:

- chart name and version;
- application version;
- chart type;
- dependencies;
- values files;
- templates;
- subcharts;
- Kubernetes API usage.

## 6. Configuration

```yaml
providers:
  personal:
    type: github
    host: github.com
    owner: mozebaltyk
    visibility: private
    git:
      name: "John Doe"
      email: "123456+mozebaltyk@users.noreply.github.com"

  work:
    type: gitlab
    host: gitlab.company.com
    group: infrastructure
    visibility: private
    git:
      name: "John Doe"
      email: "john.doe@company.com"

templates:
  general:
    repository: "https://github.com/MozeBaltyk/project-template"
```

Use the operating system's conventional user configuration directory where practical. Do not store plaintext access tokens in normal configuration by default.

## 7. Credentials

Keep credentials separate from configuration.

Preferred mechanisms:

1. OS credential/keyring storage.
2. Environment variables for automation/CI.
3. Additional secure credential backends later.

Never print tokens in normal or debug output.

## 8. Provider Interface

```go
type Provider interface {
    CurrentUser(ctx context.Context) (*User, error)
    CreateRepository(ctx context.Context, req CreateRepositoryRequest) (*Repository, error)
    GetRepository(ctx context.Context, ref RepositoryRef) (*Repository, error)
    ListRepositories(ctx context.Context, filter RepositoryFilter) ([]Repository, error)
    CreateRelease(ctx context.Context, repo Repository, req CreateReleaseRequest) (*Release, error)
}
```

Initial implementations:

```text
internal/provider/github
internal/provider/gitlab
```

Cobra commands depend on the abstraction, not concrete clients.

## 9. Git Service

```go
type GitService interface {
    Init(ctx context.Context, path string) error
    Clone(ctx context.Context, remote, destination string) error
    SetIdentity(ctx context.Context, path, name, email string) error
    AddRemote(ctx context.Context, path, name, remote string) error
    Status(ctx context.Context, path string) (*Status, error)
    Tag(ctx context.Context, path, version string) error
    Push(ctx context.Context, path string) error
    PushTag(ctx context.Context, path, version string) error
}
```

Raw `exec.Command("git", ...)` calls should be centralized in the Git package.

## 10. Suggested Package Structure

```text
cmd/
  root.go
  auth.go
  auth_add.go
  list.go
  init.go
  init_blank.go
  init_template.go
  clone.go
  clone_all.go
  release.go

internal/
  analyzer/
    analyzer.go
    helm/
  config/
  credentials/
  git/
  project/
  provider/
    github/
    gitlab/
  release/
  workspace/
```

Cobra commands should remain thin; business logic belongs under `internal/`.

## 11. Workspace Layout

Recommended layout:

```text
<workspace>/
  <host>/
    <group-or-owner>/
      <project>/
```

Example:

```text
~/Projects/
  github.com/
    mozebaltyk/
      project-template/
  gitlab.company.com/
    infrastructure/
      terraform-modules/
```

## 12. Error Handling

Errors should be actionable, for example:

```text
provider "work" is not configured
repository "api" exists on multiple providers; qualify the project
Git executable was not found
repository has uncommitted changes
tag "v1.2.0" already exists
template "general" is not configured
authentication for provider "personal" failed
```

Never silently select a provider for an ambiguous project.

## 13. Output

Default output should be concise and human-readable. Consider future `--json`, `--verbose`, and `--dry-run` modes. Secrets must always be redacted.

## 14. Runtime Dependencies

Required:

```text
git
```

Not required:

```text
gh
glab
curl
ansible
just
```

## 15. MVP

- Go/Cobra foundation.
- Configuration loading.
- GitHub provider.
- GitLab provider.
- Provider authentication.
- Repository-local Git identity.
- `colt list`.
- `colt init blank`.
- `colt init general`.
- `--local`.
- `colt clone`.
- `colt clone all`.
- `colt release`.
- Template mapping.
- Initial Helm analyzer.

## 16. Out of Scope for Initial MVP

- Replacing the Git executable.
- `gh`/`glab` integration as required dependencies.
- Ansible-specific collection generation.
- Ansible Galaxy publishing.
- Arbitrary CI/CD generation.
- Plugin marketplace.
- GUI.
- Repository hosting.

## 17. Future Considerations

- Custom named templates.
- Template variables and version pinning.
- Additional analyzers.
- Provider-qualified project references.
- JSON output.
- Shell completion.
- Dry-run support.
- SSH/HTTPS clone preference.
- Release-note generation.
- Changelog integration.
- Signed Git tags.
- Additional Git hosting providers.
- Project health checks.
