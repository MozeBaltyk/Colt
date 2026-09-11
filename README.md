# Colt

> A provider-independent Git project CLI.

Colt talks directly to GitHub and GitLab HTTP APIs for hosting operations and
uses the native `git` executable for repository operations. The specifications
describe planned behavior; a command is available only when code and release
notes say so.

## First MVP

The first MVP configures and authenticates GitHub.com, GitLab.com, and
self-hosted GitLab providers, then initializes a blank project:

```text
colt auth login <github|gitlab> <alias> \
  --namespace <namespace> --git-name <name> --git-email <email> \
  [--host <host>] [--base-url <https-url>] [--visibility <visibility>] \
  [--token-env <environment-variable>] [--default] [--replace]
colt auth status
colt init <project> [--local] [--provider <alias>]
```

Configuration uses Go's `os.UserConfigDir()`. On Linux, this is
`$XDG_CONFIG_HOME/colt/config.yaml` when `XDG_CONFIG_HOME` is set, otherwise
`~/.config/colt/config.yaml`. `COLT_CONFIG` overrides the complete path. Provider
tokens remain in the referenced environment variables and are never written
there.

`--namespace` is the repository owner: a GitHub username or organization (for
example `octocat` or `acme`) or a GitLab group/subgroup full path (for example
`platform/tools`).

Colt initializes Git, applies the selected provider's repository-local identity,
and creates an initial commit. Unless `--local` is used, it creates the remote
repository through the provider API, adds `origin`, and pushes.

Provider resolution is always provider-neutral, in this exact order:

1. explicit `--provider <alias>`;
2. the configured default provider;
3. the only configured provider of any type;
4. otherwise, fail and require an explicit or configured default provider.

Credentials come from environment variables, including configuration references
such as `token_env`. Colt never requires `gh`, `glab`, or `curl`, and never
changes global Git identity. Provider-independent ownership is called a
**namespace**.

## Later Milestones

Templates follow blank initialization. Repository listing, cloning, workspace
synchronization, and a minimum release flow follow templates. Analyzer work is
currently limited to future intent around a canonical `inventory.yaml` and
derived reports.

## Specifications

- [Product definition and specification order](docs/specs/product.md)
- [Shared active-MVP requirements](docs/specs/00-core.md)
- [First MVP: blank project initialization](docs/specs/01-project-init.md)
- [Intermediate: template initialization](docs/specs/02-template-init.md)
- [Later: project lifecycle](docs/specs/03-project-lifecycle.md)
- [Future: analyzer](docs/specs/04-analyzer.md)
- [Roadmap](docs/specs/90-roadmap.md)
- [Non-normative design notes](docs/specs/notes.md)
- [MVP acceptance specifications](features/)
