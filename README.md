# Colt

> One CLI companion for all your Git projects, wherever they live, across their entire lifecycle.

Colt is a Git project management CLI written in Go. It provides a consistent workflow for creating, cloning, analyzing, and releasing projects across GitHub and GitLab.

Colt uses provider APIs for hosting operations and the native `git` executable for Git operations. It configures a repository-local Git identity so projects do not have to rely on the user's global Git configuration.

## Goals

- Provide one workflow across GitHub and GitLab.
- Support GitHub.com, GitLab.com, and self-hosted GitLab instances.
- Support public and private repositories.
- Support multiple Git hosting providers/accounts.
- Keep Git identities isolated per provider and repository.
- Bootstrap projects from configurable Git templates.
- Preserve provider/group/project structure when cloning repositories.
- Analyze existing projects and detect supported technologies.
- Keep the core technology-agnostic and extensible.

## Features

- GitHub and GitLab support.
- Self-hosted GitLab support.
- Public and private repositories.
- Multiple provider configurations.
- Repository-local `user.name` and `user.email`.
- No dependency on the global Git identity.
- Configurable project templates.
- Local-only project creation.
- Blank and template-based project creation.
- Structured repository cloning.
- Bulk cloning by group.
- Git tagging and provider releases.
- Existing-project analysis.
- Helm project/chart analysis.
- Native Go API integration.
- Native `git` executable for Git operations.
- No required dependency on `gh`, `glab`, or `curl`.

## Commands

```text
colt auth add <github|gitlab>
    Add and configure a Git hosting provider/account.

colt list
    List configured providers and their repositories.

colt init blank <project> [--local]
    Create a new empty project.

colt init general <project> [--local]
    Create a new project from the configured "general" template.

colt clone <project>
    Clone a project while preserving its provider/group/project
    directory structure.

colt clone all [group]
    Clone all repositories, optionally restricted to a group.

colt release <project> <version>
    Tag and release a project using the specified version.

colt analyze
```

`--local` creates the project locally without creating or pushing a remote repository.

## Architecture

Colt separates Git hosting operations from Git repository operations.

```text
colt
 ├── provider API clients
 │    ├── GitHub API
 │    └── GitLab API
 │
 └── git executable
      ├── clone
      ├── fetch
      ├── push
      ├── tag
      └── config
```

Provider-specific behavior is exposed through a common internal interface. Colt intentionally does not require `gh` or `glab`.

## Configuration

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

`personal` and `work` are provider aliases. Authentication secrets should not be stored directly in this configuration file; tokens should come from a secure credential store or supported environment variables.

## Repository-local Git Identity

Whenever Colt creates or clones a repository, it applies the Git identity associated with the selected provider:

```ini
[user]
    name = John Doe
    email = john.doe@company.com
```

This is written to the repository's local `.git/config`, allowing personal and work projects to safely use different identities without changing global Git configuration.

## Project Templates

Project types are aliases mapped to reusable Git repositories:

```yaml
templates:
  general:
    repository: "https://github.com/MozeBaltyk/project-template"
```

Running:

```bash
colt init general my-project
```

bootstraps `my-project` from the configured `general` template. Additional project types can be added through configuration.

## Project Analysis

Colt can analyze existing projects to identify supported technologies and configuration. The initial analyzer scope includes Helm projects/charts, with an extensible analyzer architecture for future technologies.

## Release Workflow

```bash
colt release my-project v1.2.0
```

The expected workflow is:

1. Locate and validate the project.
2. Verify the Git working tree.
3. Validate that the requested tag does not already exist.
4. Create the Git tag.
5. Push the tag.
6. Create the corresponding GitHub or GitLab release.

## Status

Colt is being redesigned as a Go/Cobra application. The initial focus is a small, reliable core for provider configuration, project initialization, cloning, Git identity management, analysis, and releases.
