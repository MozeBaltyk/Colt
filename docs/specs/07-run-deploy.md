# Run / Deploy Specification

`colt run` provisions a permanent self-hosted Gitea or Forgejo server as
podman containers managed by systemd on the local host.

## Scope boundary

This is a **host deployment** primitive, outside provider API work. It
owns systemd units, podman containers, networks, and volumes. It does
not replace `colt auth login` — the deployed Gitea/Forgejo instance is
later configured as a provider via `colt auth login gitea|forgejo <alias>`.

``` text
colt run <gitea|forgejo> <name> [--replace] [--image <ref>] [--password <secret>] [--external-url <https-url>]
colt run status [<name>]
colt run stop <name>
colt run start <name>
colt run rm <name> [--volumes] # deprecated alias: --volume
```

`<name>` is the deployment name. It becomes the systemd unit suffix,
podman container name, network name, and volume prefix. Because those host
resources are intentionally not type-namespaced, names MUST be unique across
Gitea and Forgejo deployments on a host. Colt stores the type as deployment
metadata and rejects cross-type replacement.

## Deployment units (per name)

| Unit | Purpose |
|------|---------|
| `podman-network-<name>-net.service` | podman network, oneshot, remains after exit |
| `container-<name>-db.service` | database container (MariaDB for gitea, PostgreSQL for forgejo) |
| `container-<name>-app.service` | app container, depends on db + network |

All three are `WantedBy=multi-user.target` so the deployment survives reboot.

## Requirements

| ID | Requirement | Verification |
|:---|:---|:---|
| `RUN-001` | `colt run <type> <name>` MUST create the three systemd units under `/etc/systemd/system/` with 0644 permissions. | Inspection / `colt run status` |
| `RUN-002` | MUST create a podman network `<name>-net` and uniquely owned volumes `<name>-data-<owner>`, `<name>-config-<owner>`, `<name>-db-<owner>` via podman. All resources carry `org.colt.deployment=<owner>`. | Orchestration tests; isolated runtime lane pending |
| `RUN-003` | MUST enable and start the network, db, then app units in that order. | `systemctl is-active` |
| `RUN-004` | MUST NOT hardcode passwords in unit files. Secrets MUST live in an `EnvironmentFile=` pointed at a 0600 file (e.g. `/etc/colt/run/<name>/env`). | File permission inspection |
| `RUN-005` | Generate application `security.SECRET_KEY` and DB password initially. Preserve both on replacement. Legacy `--password` overrides the initial application key only; a different replacement key MUST fail before stopping services. No prompt or administrator provisioning. | Key-preservation and rotation-rejection tests |
| `RUN-006` | `--replace` MUST stop/remove owned runtime resources before recreating, retaining volumes, credentials and omitted image/URL options. Also resumes retained deployments. Without it, an existing deployment MUST fail. | Orchestration re-deploy test |
| `RUN-007` | `colt run status` MUST discover, deduplicate, sort, and concisely list valid deployment names from deployment directories and Colt unit filenames, including partial and read-only legacy installs; an empty host reports `no deployments found`. `colt run status <name>` MUST report ownership/lifecycle, each present unit's actual systemd state (including failed/activating), absent units, best-effort type/image and volume availability without reading env secrets or failing solely on incomplete legacy state. | Output and orchestration tests |
| `RUN-008` | `rm` MUST retain protected ownership, credentials and metadata unless `--volumes`. A later `--replace` or `rm --volumes` MUST work; missing resources/units are tolerated, unfamiliar ones rejected. Never force-remove networks/volumes or their consumers. | Retained/partial lifecycle orchestration tests |
| `RUN-009` | `--image` overrides the default image tag. | Unit inspection |
| `RUN-010` | On any systemd/podman/permission failure, MUST report the failing layer and stop, leaving prior deployment intact unless `--replace`. A failed fresh deployment MUST best-effort remove only resources created by that attempt; replacement is not transactional. | Failure-path test |
| `RUN-011` | Database container MUST declare an image-supported `--health-cmd` (`healthcheck.sh --connect --innodb_initialized` / `pg_isready -U forgejo -d forgejo`). | Unit inspection / container health check |
| `RUN-012` | App unit MUST wait for the database to become healthy before starting the app container. | Unit inspection / startup order test |
| `RUN-013` | `colt run` requires root (writes to `/etc/systemd/system/`). Non-root execution MUST fail with an actionable error suggesting `sudo`. | Non-root execution test |
| `RUN-014` | `--external-url` MUST accept only a clean absolute root-path HTTPS URL, configure the application’s advertised web/SSH host while retaining container HTTP, persist/report it, and print a token-free manual onboarding template. Invalid values MUST fail before mutation. | Rendering / output / failure-path test |
| `RUN-015` | Lifecycle success MUST be based on verified state, not successful command dispatch: `stop` requires every exact unit inactive and its owned app/DB containers absent; `start` clears exact-unit failed state before ordered startup, retains readiness checks, and requires every unit active. | Fake-host stop/start state tests |

## Image defaults

| Type | App image | DB image |
|------|-----------|----------|
| gitea | `docker.io/gitea/gitea:1-rootless` | `docker.io/library/mariadb:11` |
| forgejo | `codeberg.org/forgejo/forgejo:16.0.5-rootless` | `docker.io/library/postgres:16` |

`--image` overrides only the app image and is preserved when omitted on
replacement. It MUST implement the selected product's rootless image contract:
UID/GID 1000, HTTP 3000, embedded SSH 2222, `/var/lib/gitea` data and
`/etc/gitea` configuration. Arbitrary/standard images are **not supported**;
Colt validates reference syntax, not the contents of a custom image.
Forgejo explicitly sets `GITEA_APP_INI=/etc/gitea/app.ini` (v16's default
otherwise places configuration under the data directory).

The official [package API](https://codeberg.org/api/v1/packages/forgejo/container/forgejo/16.0.5-rootless)
confirmed this exact published tag. Its
[versioned Dockerfile](https://codeberg.org/forgejo/forgejo/src/tag/v16.0.5/Dockerfile.rootless)
and [setup script](https://codeberg.org/forgejo/forgejo/src/tag/v16.0.5/docker/rootless/usr/local/bin/docker-setup.sh)
confirm the user, paths, ports and configuration override. This is source/registry
verification, **not a successful runtime acceptance test**.

Back up all volumes and `/etc/colt/run/<name>` before upgrades; choose explicit
rootless release tags/digests, follow upstream sequential-major upgrade guidance,
and test restoration in isolation. Forgejo's default is release-pinned, not
digest-pinned. Gitea and database defaults remain mutable major tags; full
reproducibility and a validated upgrade matrix remain outstanding.

## Port mapping (defaults)

- App: `127.0.0.1:3000:3000` (web), `2222:2222` (SSH, all interfaces).
- Ports are fixed, not configurable. Only **one active deployment per host**;
  different names are not simultaneous-instance support.
- DB: no host port, container-only on the `<name>-net` network.

## External connectivity

Without `--external-url`, Colt reports the local browser endpoint
`http://127.0.0.1:3000/`. This does not claim that the local HTTP endpoint is
suitable for Colt provider API authentication, which continues to require
HTTPS.

`--external-url https://git.example.com` configures Gitea/Forgejo to advertise
that HTTPS root URL and hostname while the container itself continues serving
HTTP on host loopback port 3000. TLS termination, certificates, DNS, and forwarding
to `127.0.0.1:3000` belong to a **same-host** reverse proxy such as Caddy or nginx.
Off-host/container-network proxies and direct LAN HTTP are not supported by this
binding; no implicit public plaintext fallback is provided. Colt does not manage
the proxy or firewall. Restrict the publicly bound SSH port as appropriate.

**Colt SSH transport is unsupported for these deployments.** Direct Git clients
can use `ssh://git@host:2222/owner/repo.git`, but Colt's existing validator/push
boundary only supports SCP-style URLs on the default SSH port. Use Colt's HTTPS
transport through the proxy; do not bypass repository/authority validation.

After deployment, administrator setup and access-token creation remain manual.
Colt prints a shell-safe `colt auth login` template with placeholders for the
unknown namespace and Git identity; it never creates or prints a token or
administrator password and does not run the onboarding command automatically.

## Environment files (security)

``` text
/etc/colt/run/<name>/env   # 0600, root:root
  DB_TYPE=mysql|postgres
  DB_HOST=<name>-db:3306|5432
  DB_NAME=gitea|forgejo
  DB_USER=gitea|forgejo
  DB_PASSWD=<secret>
  GITEA__security__SECRET_KEY=<secret>   # Gitea
  FORGEJO__security__SECRET_KEY=<secret> # Forgejo
  GITEA__server__ROOT_URL / FORGEJO__server__ROOT_URL=<external-url>
  GITEA__server__DOMAIN / FORGEJO__server__DOMAIN=<external-host>
  GITEA__server__SSH_DOMAIN / FORGEJO__server__SSH_DOMAIN=<external-host>
  GITEA__server__SSH_PORT / FORGEJO__server__SSH_PORT=2222
  GITEA__server__SSH_LISTEN_PORT / FORGEJO__server__SSH_LISTEN_PORT=2222
  GITEA__server__PROTOCOL / FORGEJO__server__PROTOCOL=http
```

The app unit uses `EnvironmentFile=/etc/colt/run/<name>/env`. The db
unit passes `MARIADB_ROOT_PASSWORD` / `POSTGRES_PASSWORD` through the same
file or a separate db env file with restricted access.

For both application types, `--password` supplies `security.SECRET_KEY`; it
does not create an initial administrator. Administrator creation and provider
onboarding remain explicit post-deployment operations.

Omit `--password` in normal use: it is a backward-compatible initial encryption
key override, not a login password. A generated key is never printed. Replacement
preserves it (including quoted characters) and the DB password. Rotation is
unsupported, even with `--replace`; losing/changing the key can make encrypted
database values unreadable. Supplying secrets on a CLI can expose them in process
arguments/history. Do not delete the protected deployment directory when retaining
volumes.

## Health checks

DB container MUST declare an image-supported `--health-cmd`
(`healthcheck.sh --connect --innodb_initialized` /
`pg_isready -U forgejo -d forgejo`). The app unit MUST wait for the DB
container to exist and become healthy before
starting the app container, covering initial image-pull/container-creation
latency in `ExecStartPre`.

Deployment and `start` have a five-minute overall timeout and additionally require
owned containers, healthy DB, running app, and HTTP 200 from the local
`/api/healthz` endpoint before success. This checks local service readiness, not
external DNS/TLS, administrator provisioning, or SSH. Services remain Type=simple;
systemd active alone is not application readiness. CID-file-based stop and
synchronous `ExecStopPost` removal avoid deleting a foreign same-name container
and prevent asynchronous `--rm` restart races. Failed cleanup intentionally fails
closed rather than deleting an unknown container to make a restart work.
Pre-start cleanup selects exited remnants by **both** the exact container-name
filter and ownership label, non-force, covering a reboot that cleared `/run` CID
files. A still-running remnant fails closed; CLI removal can stop the owned ID.

Preflight checks executables/root, a running (possibly degraded) systemd manager,
and rootful Podman info before replacing services. It does not guarantee port
availability, image compatibility, disk space, or every Podman flag. These still
require runtime testing. On failure, inspect the exact app/DB units with
`systemctl status` and `journalctl -u`; logs may include secrets, so redact before
sharing. Raw subprocess stderr is deliberately not copied into CLI errors.

## Recovery and removal

`rm` stops owned units, removes owned containers/network without forcing shared
resources, and leaves 0700 `/etc/colt/run/<name>` with 0600 env, metadata and owner
records. Resume using `colt run <type> <name> --replace`; later explicit
`colt run rm <name> --volumes` purges verified-owned volumes and recovery records.
**The latter is destructive and requires an operator's deliberate choice.**
The deprecated singular `--volume` spelling is an alias for the same explicit
destructive boolean; omission of either spelling always retains volumes.
Partial removals can be retried; missing objects are not adopted or recreated
during removal. If a volume is missing, redeploy refuses a mixed old/new database:
restore a complete backup or deliberately finish purging before a fresh deploy.

Labels use a random deployment identity; volumes include it in their physical
names because Podman volumes have no immutable ID. Containers/networks are
inspected with label+ID in one response and removed by ID. Unit ownership markers
must match. These checks guard accidental collisions/competing clients, not a
malicious root administrator deliberately forging labels or modifying root-owned
units/CID files. Do not manipulate the same deployment concurrently outside Colt.
A per-name directory lock serializes Colt mutations. After a process crash,
confirm no operation is running before manually removing the empty
`/etc/colt/run/<name>.lock` directory.

Fresh-deploy cleanup uses an independent one-minute context even when deployment
was canceled. Failed stop/removal keeps protected recovery state. Replacement is
**not transactional**: it retains credentials/volumes but may leave services
stopped or partially rendered. Retry with a compatible image; database schema
upgrades may require restoring backups, not simply selecting an older image.

Legacy Gitea/image-only metadata and older units remain readable by `status`,
including `gateau`. No destructive automatic legacy migration: without a valid
ownership record, `start`, `stop`, `rm`, and `--replace` fail closed. Back up the
legacy DB, repositories, configuration and keys, then arrange a manual migration
to a new deployment name on an isolated host. Never invent an owner record or
relabel resources merely to bypass this guard.

## Validation lanes

The active `@orchestration` BDD scenarios and fake-host unit tests exercise
rendering, ordering, simulated resources, cancellation/recovery, and errors. They
do **not** prove image availability, actual systemd/Podman semantics, persistence,
health commands, SSH, or production readiness.

`internal/app/run_integration_test.go` is separately gated with the
`colt_integration` build tag, `COLT_RUN_ISOLATED_VM=destroy-disposable-vm`, and an
operator-provisioned `/etc/colt/disposable-integration-vm` containing exactly
`destroy-disposable-vm` plus newline. It refuses existing Colt deployments,
`gateau` paths, or occupied ports. **Destructive: run only after approval inside
a disposable Linux systemd VM, never on a workstation/shared/production host.**
The lane creates a Git commit, config sentinel and DB row for both products;
checks them across stop/start, replace, retained removal/redeploy; then purges
only its owned test resources. Default `go test ./...` cannot execute this lane.
It has not been executed here. Full API onboarding, encrypted application values,
actual SSH access, reboot, foreign consumers and restart stress still require
isolated acceptance testing before declaring this production-ready.

## Notes

- `colt run` requires root (writes to `/etc/systemd/system/`). If not
  root, fail with an actionable error suggesting `sudo`.
- This is a host-level primitive; `CORE-PROVIDER-001` (provider API auth)
  is orthogonal — configure the deployed instance with `colt auth login`
  after deployment.
- Volumes persist across `colt run rm` unless `--volumes`; this is the
  deliberate default so `stop`/`start` cycles are safe.
- Deployment metadata stores the non-secret type, app image, and optional
  external URL. Existing Gitea `image=` metadata and older generated units
  remain readable.
