# Colt

**One CLI for all your Git projects — across providers, through their whole lifecycle.**

Colt is a provider-independent companion for Git repositories. It talks to GitHub.com, GitLab, Gitea, and Forgejo through their HTTP APIs for *hosting* operations (auth, repos, releases), and leaves all *repository* work to your native `git`. Clone, initialize from templates, reconcile a declared workspace, mirror between providers, run health checks — or stand up your own self-hosted Gitea/Forgejo server.

- **Provider-neutral** — the same commands for GitHub, GitLab, Gitea, and Forgejo.
- **Native `git` under the hood** — Colt never reimplements Git; it orchestrates it.
- **No wrappers** — never invokes `gh`, `glab`, or `curl`, and never touches your global Git identity.
- **Credentials stay safe** — tokens live in your OS keychain/credential store (or a referenced env var), never in URLs or `.git/config`.

---

## Install

Prebuilt binaries for Linux, macOS, and Windows (amd64/arm64) are published on [GitHub Releases](https://github.com/MozeBaltyk/Colt/releases).

```bash
# latest release → ~/.local/bin (override with COLT_INSTALL_DIR)
curl -fsSL https://raw.githubusercontent.com/MozeBaltyk/Colt/main/install.sh | bash

# pin a version
curl -fsSL https://raw.githubusercontent.com/MozeBaltyk/Colt/main/install.sh | bash -s -- v0.1.0
```

Or build from source (Go 1.27+):

```bash
CGO_ENABLED=0 go build -o colt ./cmd/colt
```

> Tip: a CGO-enabled build enables the macOS Keychain backend; without CGO you still get the cross-platform credential store and (on Linux) a consent-gated plaintext fallback.

---

## Quick start

```bash
# 1. Connect a provider. Example: your personal GitHub account.
colt auth login github personal \
  --namespace octocat \
  --git-name "Octo Cat" \
  --git-email octo@example.com \
  --default

# 2. Initialize a project (creates the remote, clones it locally, pushes an initial commit).
colt init my-new-project

# 3. See what's configured.
colt auth status
colt list
```

`colt init` puts the remote under `<cwd>/<namespace>/<project>` and configures everything for you. To work locally without a remote:

```bash
colt init demo --local
```

---

## Providers & configuration

Configuration lives at `~/.config/colt/config.yaml` on Linux (respecting `$XDG_CONFIG_HOME`), and `COLT_CONFIG` overrides the whole path. `colt auth login` writes it for you, but you can hand-edit it too:

```yaml
providers:
  personal:                      # alias you use in every command
    type: github
    host: github.com
    base_url: https://api.github.com
    namespace: octocat           # owner / org / group
    visibility: private
    git_name: Octo Cat
    git_email: octo@example.com
    transport: https             # https (default) or ssh
    auth:
      source: env                # or: stored
      token_env: GITHUB_TOKEN
    default: true                # used when --provider is omitted
```

**When do you need an alias?** Provider resolution is always, in order: explicit `--provider <alias>` → the `default:` provider → the only provider → otherwise error. Most commands take `--provider` so you can pick on the fly.

**Credential sources.** `env` reads from the named variable (no fallthrough). `stored` keeps the token in your OS secure store (`keyring`/`git-credential`-style backends), falling back to a separate, consent-warned `credentials` plaintext file only when no secure backend exists. Secrets are never written into `config.yaml` or git config — only a non-secret credential ID is stored.

**Git transport.** `https` authenticates through a credential helper that calls `colt` itself (keep `colt` on `PATH`). `ssh` uses your existing SSH agent/keys — Colt never reads private keys.

**Custom CA (self-hosted HTTPS).** For GitLab/Gitea/Forgejo with a private CA, either set `SSL_CERT_FILE=<ca-bundle>` or pass `--ca-cert <ca-bundle>` (a global flag on every command):

```bash
colt --ca-cert ~/ca.crt list
```

---

## Commands

### Providers — `colt auth`

```bash
colt auth login <github|gitlab|gitea|forgejo> <alias> \
  --namespace <owner|group> --git-name <name> --git-email <email> \
  [--host <host>] [--base-url <url>] [--visibility <private|public>] \
  [--token-env <var> | --credential stored] [--transport https|ssh] \
  [--default] [--replace]

colt auth status [alias] [--repository <project>] [--offline]
colt auth logout <alias> [--revoke]
```

- `auth login` validates the credential against the provider before saving.
- `auth status` shows every configured provider plus connection/transport checks.
- `auth logout` removes the stored credential; `--revoke` also attempts provider-side revocation.

### Initialize — `colt init`

```bash
colt init <project> [--provider <alias>] [--transport https|ssh]
colt init <project> --local
colt init <project> --template <name>[@<version>] [--set key=value]...
colt init <project> --destination <path>
```

`--local` creates only the local repo. `--template` materializes a configured template scaffold. `--destination` overrides the clone path.

### Templates — `colt template`

```bash
colt template list
colt template show <name>[@<version>]
```

Templates are local, versioned directory sources (`{{parameter}}` substitution, exact `sha256:` digests). See [M3 spec](docs/specs/03-template-init.md).

### List & clone — `colt list` / `colt clone`

```bash
colt list [--provider <alias> | --all] [--namespace <owner|group>]
colt clone <repository> [--provider <alias>] [--transport https|ssh]
```

`list --namespace` scopes to one owner/group (for GitLab this targets that group directly, avoiding huge listings).

### Releases — `colt release`

```bash
colt release <version> [--provider <alias>] [--transport https|ssh]
```

Creates a local tag, pushes it, then creates the provider release.

### Workspace — `colt status` / `colt sync`

Declare the repos you want checked out under `workspace:` in `config.yaml`:

```yaml
workspace:
  repositories:
    - provider: personal
      namespace: octocat
      # include: [api, web]   # optional; omit = every repo in the namespace
    - provider: work
      namespace: platform/tools
```

```bash
colt status          # what's present, missing, absent, or mismatched
colt sync [--dry-run] # clone exactly what's missing (never prunes or rewrites)
```

### Mirror — `colt mirror`

One-way copy of a namespace (or a single repo) from one provider to another — handy for GitHub/GitLab → your own Gitea/Forgejo.

```bash
colt mirror <source-alias> <target-alias> \
  [--namespace <source-ns>] [--target-namespace <target-ns>] \
  [--repository <name>] [--replace]
```

- `--namespace` overrides the source namespace; `--target-namespace` overrides where it lands.
- `--repository` mirrors a single repo instead of the whole namespace.
- `--replace` force-pushes onto existing target repos (never deletes them); without it, an existing target repo fails safely.
- Mirroring is one-way and not a continuous sync — re-run to pick up new changes.

### Health — `colt check`

```bash
colt check [--all]
```

Diagnostic-only checks for every selected repository. Configure expectations under `policy.repository`:

```yaml
policy:
  repository:
    require: [main]            # branches that must exist
    default_branch: main
    allowed_visibility: [private, internal]
```

Exit codes: `0` healthy, `1` findings, `2` configuration/operational error.

### Self-hosted server — `colt run`

Deploy and manage your own Gitea or Forgejo server as a systemd unit (rootless):

```bash
colt run gitea internal --external-url https://git.example.com
colt run status
colt run start internal
colt run stop internal
colt run rm internal [--volumes]
```

---

## Development

```bash
just test              # unit + acceptance (BDD) suites
just test-bdd          # deterministic fake-backed acceptance only
just test-integration  # real Gitea/Forgejo vertical slices (needs podman/docker)
just compile           # build bin/colt
```

Releases are cut by pushing a `v*` tag — [the release workflow](.github/workflows/release.yml) runs the full test suite, cross-compiles binaries, and publishes them to GitHub Releases. CI runs unit + BDD + race + black-box checks on every push/PR.

---

## Docs & specs

- [Product definition](docs/specs/product.md) · [Roadmap](docs/specs/90-roadmap.md)
- [Shared core](docs/specs/00-core.md) · [Init](docs/specs/01-project-init.md) · [Lifecycle](docs/specs/02-project-lifecycle.md)
- [Templates](docs/specs/03-template-init.md) · [Workspace](docs/specs/04-workspace.md) · [Health](docs/specs/05-project-health.md)
- [Acceptance specifications](features/)

---

Colt supports GitHub.com, self-hosted GitLab, Gitea, and Forgejo. It is not a wrapper around `gh`/`glab`, and it never changes your global Git identity.