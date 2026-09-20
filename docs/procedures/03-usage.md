# Operator guide

## Build and Install

## Syntax

## Workflow

On a Linux deployment host, run `sudo colt run status` to list all deployments
found in Colt's protected state directory or systemd units. The concise list
marks managed, retained/partial, and `legacy (read-only)` installs and shows
app, database, and network unit states. Use `sudo colt run status <name>` for
details. Removal retains volumes unless the destructive `--volumes` flag is
explicitly supplied (`--volume` is a deprecated alias).

Remote `colt init <project>` clones to
`<user-home>/<namespace>/<project>`. Use `--destination <path>` to replace the
complete clone path; relative paths resolve from the current directory and the
parent directory must already exist. Colt accepts a missing or empty ordinary
destination, but refuses non-empty directories, files, and symbolic links.
`--destination` is remote-only and cannot be combined with `--local`; local
initialization remains `<current-directory>/<project>`.

Set the provider's conventional token variable (for example `GITHUB_TOKEN`) or
an explicit `--token-env` for automation, then run `colt auth login`. Environment
tokens are authenticated but never persisted by Colt. With no resolved
environment token and no explicit `--token-env`, login requires a terminal and
reads a hidden token. A configured variable that is missing or empty fails with
no prompt and no fallback. Colt authenticates before storing a manual token in
the OS credential facility.

`--replace` never enrolls or overwrites a stored credential. An existing stored
reference is reused only when the host and credential ID remain unchanged;
changes that would create or orphan stored state are refused.

If secure storage is unavailable, Colt asks before writing the separate
plaintext `credentials` file. Answering no, EOF, or running without a terminal
leaves both credentials and configuration unchanged. The fallback is protected
by filesystem permissions, not encryption.
Plaintext fallback is disabled on Windows until Colt can validate user-only
Windows ACLs. Windows Credential Manager remains available for secure storage.

`colt auth logout <alias>` is offline and removes only that alias's stored
credential from secure and consented fallback stores. It preserves provider
configuration, environment variables, Git identity, and SSH state. `--revoke`
is unsupported and changes nothing.

## Generated State

- `config.yaml`: provider metadata and non-secret credential references.
- `credentials`: optional versioned plaintext fallback, mode 0600 on Unix.
- `credentials.lock`: transient 0600 exclusive-create mutation lock. Colt never
  follows a pre-existing lock path, removes its lock before reporting success,
  and fails with a bounded busy error when another or stale lock already exists.

## External Tools

Native `git` is the only external executable. Secure credentials use native OS
APIs (Secret Service, macOS Keychain, or Windows Credential Manager); command-
backed keyring implementations are disabled. macOS Keychain requires a
CGO-enabled Colt build; a CGO-disabled macOS build fails secure storage closed.

## Configuration

The default is the platform user configuration directory under `colt/`.
`COLT_CONFIG` overrides the complete `config.yaml` path and relocates the
optional sibling `credentials` file with it.
