# Colt

> One CLI companion for all your Git projects, across providers and throughout their lifecycle.

Colt aims to be a provider-independent project manager for Git repositories.

With one CLI, you can connect to multiple Git hosting providers, clone repositories, work with an entire namespace, initialize projects from your own templates, and eventually mirror projects between providers.

Colt talks directly to supported Git hosting providers through their HTTP APIs for hosting operations, while leaving repository operations to native `git`. GitHub, GitLab, Gitea, and Forgejo are currently supported.

The project is being built progressively. Some of the core functionality is already implemented, some M1 requirements are still marked `@unimplemented`, and the rest is organized into planned milestones.

## First MVP

The first MVP configures and authenticates GitHub.com, GitLab.com, self-hosted
GitLab, and self-hosted Gitea providers, then initializes a blank project:

```text
colt auth login <github|gitlab|gitea|forgejo> <alias> \
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

`--namespace` is the repository owner in provider-native terms: a GitHub, Gitea, or Forgejo user/organization (for example `octocat` or `acme`) or a GitLab group/subgroup full path (for example `platform/tools`).

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

## Planned Milestones

Milestone 1 is partially implemented: environment authentication, blank
initialization, and HTTPS Git transport work, while interactive persistence,
plaintext fallback, and logout remain explicitly `@unimplemented`. The real
Colt-to-Gitea and Colt-to-Forgejo initialization and push paths are exercised in container-backed CI.
Later planned
work adds lifecycle list/clone/release primitives (M2), parameterized data-only
templates (M3), declarative workspace `status`/`sync` (M4), diagnostic project
health (M5), and a read-only analyzer (M6). Colt is not a wrapper or replacement
command surface for `gh` or `glab`.

## Specifications

- [Product definition and specification order](docs/specs/product.md)
- [Shared active-MVP requirements](docs/specs/00-core.md)
- [First MVP: blank project initialization](docs/specs/01-project-init.md)
- [Planned M2: project lifecycle](docs/specs/02-project-lifecycle.md)
- [Planned M3: template initialization](docs/specs/03-template-init.md)
- [Planned M4: workspace reconciliation](docs/specs/04-workspace.md)
- [Planned M5: project health](docs/specs/05-project-health.md)
- [Planned M6: analyzer](docs/specs/06-analyzer.md)
- [Roadmap](docs/specs/90-roadmap.md)
- [Acceptance specifications](features/)
