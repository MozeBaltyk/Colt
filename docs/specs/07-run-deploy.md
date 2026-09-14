# Run / Deploy Specification

`colt run` provisions a permanent self-hosted Gitea or Forgejo server as
podman containers managed by systemd on the local host.

## Scope boundary

This is a **host deployment** primitive, outside provider API work. It
owns systemd units, podman containers, networks, and volumes. It does
not replace `colt auth login` — the deployed Gitea/Forgejo instance is
later configured as a provider via `colt auth login gitea|forgejo <alias>`.

``` text
colt run <gitea|forgejo> <name> [--replace] [--image <ref>] [--password <secret>]
colt run status <name>
colt run stop <name>
colt run start <name>
colt run rm <name>
```

`<name>` is the deployment name. It becomes the systemd unit suffix,
podman container name, network name, and volume prefix. A deployment is
identified by `(type, name)` — the same identity shape as provider
aliases (`CORE-PROVIDER-010`).

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
| `RUN-002` | MUST create a podman network `<name>-net` and named volumes `<name>-data`, `<name>-config`, `<name>-db` via podman. | `podman network/volume ls` |
| `RUN-003` | MUST enable and start the network, db, then app units in that order. | `systemctl is-active` |
| `RUN-004` | MUST NOT hardcode passwords in unit files. Secrets MUST live in an `EnvironmentFile=` pointed at a 0600 file (e.g. `/etc/colt/run/<name>/env`). | File permission inspection |
| `RUN-005` | `--password` supplies the app password; DB password is derived/auto-generated if omitted. Interactive prompt when stdin is a terminal and neither is supplied. | Interaction test |
| `RUN-006` | `--replace` MUST stop and remove the existing deployment before recreating. Without it, an existing deployment MUST fail. | Re-deploy test |
| `RUN-007` | `colt run status <name>` MUST report unit active/inactive, image, ports, and volume paths. | Output test |
| `RUN-008` | `colt run stop/start/rm` MUST control the deployment lifecycle. `rm` MUST NOT remove volumes unless `--volumes`. | Lifecycle test |
| `RUN-009` | `--image` overrides the default image tag. | Unit inspection |
| `RUN-010` | On any systemd/podman/permission failure, MUST report the failing layer and stop, leaving prior deployment intact unless `--replace`. | Failure-path test |

## Image defaults

| Type | App image | DB image |
|------|-----------|----------|
| gitea | `docker.io/gitea/gitea:1-rootless` | `docker.io/library/mariadb:11` |
| forgejo | `docker.io/forgejo/forgejo:latest` | `docker.io/library/postgres:16` |

`--image` overrides only the app image; DB image follows type unless a
future flag adds it.

## Port mapping (defaults)

- App: `3000:3000` (web), `2222:2222` (SSH) — both overridable later.
- DB: no host port, container-only on the `<name>-net` network.

## Environment files (security)

``` text
/etc/colt/run/<name>/env   # 0600, root:root
  DB_TYPE=mysql|postgres
  DB_HOST=<name>-db
  DB_NAME=gitea|forgejo
  DB_USER=gitea|forgejo
  DB_PASSWD=<secret>
  GITEA_PASSWORD / FORGEJO_PASSWORD=<secret>
```

The app unit uses `EnvironmentFile=/etc/colt/run/<name>/env`. The db
unit passes `MYSQL_ROOT_PASSWORD` / `POSTGRES_PASSWORD` through the same
file or a separate db env file with restricted access.

## Health checks

DB container MUST declare a `--health-cmd` appropriate to the engine
(`mysqladmin ping` / `pg_isready`). The app unit MUST wait for healthy
before starting the app container (same pattern as user-supplied template,
implemented via `podman health` check in `ExecStartPre`).

## Notes

- `colt run` requires root (writes to `/etc/systemd/system/`). If not
  root, fail with an actionable error suggesting `sudo`.
- This is a host-level primitive; `CORE-PROVIDER-001` (provider API auth)
  is orthogonal — configure the deployed instance with `colt auth login`
  after deployment.
- Volumes persist across `colt run rm` unless `--volumes`; this is the
  deliberate default so `stop`/`start` cycles are safe.
