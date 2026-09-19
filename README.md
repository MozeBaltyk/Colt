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
colt auth logout <alias>
colt init <project> [--local] [--provider <alias>] [--destination <path>]
colt list [--provider <alias> | --all]
colt clone <repository> [--provider <alias>] [--transport https|ssh]
colt release <version> [--provider <alias>] [--transport https|ssh]
```

Configuration uses Go's `os.UserConfigDir()`. On Linux, this is
`$XDG_CONFIG_HOME/colt/config.yaml` when `XDG_CONFIG_HOME` is set, otherwise
`~/.config/colt/config.yaml`. `COLT_CONFIG` overrides the complete path.
Environment tokens remain in the referenced environment variables and are
never copied. A configured `--token-env` that is missing or empty fails without
falling through. Only when no explicit token variable was configured and no
conventional token resolves does `auth login` prompt without echoing,
authenticate the token, and store it in the native OS credential facility. If
that facility is unavailable, Colt writes the separate `credentials` plaintext
file only after an explicit warning and confirmation; non-interactive commands
fail rather than prompt or silently fall back. Plaintext fallback is currently
disabled on Windows pending user-only ACL validation. Provider config contains
only the non-secret credential ID.

`--namespace` is the repository owner in provider-native terms: a GitHub, Gitea, or Forgejo user/organization (for example `octocat` or `acme`) or a GitLab group/subgroup full path (for example `platform/tools`).

With `--local`, Colt initializes Git directly and creates an initial commit.
Otherwise it creates the remote through the provider API, clones it under
`<user-home>/<namespace>/<project>`, applies repository-local identity, and
pushes the initial commit. `--destination <path>` overrides that complete remote
clone path; a relative path is resolved from the current directory and its
parent must already exist. The flag is rejected with `--local`, whose current
directory behavior is unchanged. Existing non-empty, file, and symlink
destinations are always refused.

Provider resolution is always provider-neutral, in this exact order:

1. explicit `--provider <alias>`;
2. the configured default provider;
3. the only configured provider of any type;
4. otherwise, fail and require an explicit or configured default provider.

Credentials resolve from an explicit environment variable with no fallthrough,
otherwise from the conventional variable, native secure storage, then an
already-created consented plaintext fallback. macOS Keychain support requires a
CGO-enabled build; Windows Credential Manager remains supported without the
Windows plaintext fallback.
`auth logout` removes the selected stored credential from both local stores,
preserving config, environment, SSH state, and unrelated credentials. With
`--revoke`, Colt also attempts provider-side revocation first, reporting remote
supported / unsupported / failed independently of the local removal outcome.
Colt never requires `gh`, `glab`, or `curl`, and never changes global Git identity.
Provider-independent ownership is called a **namespace**.

HTTPS repositories use a Git credential helper that invokes `colt` by name.
Keep the trusted Colt executable on `PATH`; an absolute helper path is not used
because portable shell-safe quoting across Git's supported platforms is not available.

## Planned Milestones

Milestone 1 includes environment and manual-token authentication, secure
persistence with consent-only plaintext fallback, local-only logout, blank
initialization, and HTTPS Git transport. Provider-side revocation is
implemented, reporting remote supported/unsupported/failed independently of
local credential removal. Production native persistence acceptance is gated
behind the `integration` lane, characterized by a native OS keyring round-trip
test that skips when no native credential facility is available. The real
Colt-to-Gitea and Colt-to-Forgejo initialization and push paths are exercised in container-backed CI.
Milestone 2 core list/clone/release primitives are available with authoritative
transport validation, partial-release reporting, race-safe clone destination
confinement, and hostile repository-local Git configuration rejection.
Later planned work adds
parameterized data-only templates (M3), declarative workspace `status`/`sync` (M4), diagnostic project
health (M5), and a read-only analyzer (M6). Colt is not a wrapper or replacement
command surface for `gh` or `glab`.

## Specifications

- [Product definition and specification order](docs/specs/product.md)
- [Shared active-MVP requirements](docs/specs/00-core.md)
- [First MVP: blank project initialization](docs/specs/01-project-init.md)
- [M2: project lifecycle](docs/specs/02-project-lifecycle.md)
- [Planned M3: template initialization](docs/specs/03-template-init.md)
- [Planned M4: workspace reconciliation](docs/specs/04-workspace.md)
- [Planned M5: project health](docs/specs/05-project-health.md)
- [Planned M6: analyzer](docs/specs/06-analyzer.md)
- [Roadmap](docs/specs/90-roadmap.md)
- [Acceptance specifications](features/)
