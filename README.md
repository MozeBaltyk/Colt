# Project-template

A project template (boilerplate) that bootstraps a consistent, transportable
way to build, run, and deploy — from a single `justfile` entrypoint.

It brings, out of the box:

- a **`justfile`** task runner as the single entrypoint;
- **grouped scripts** under `scripts/<group>/`;
- a **reproducible Execution Environment** (`Containerfile`) with pinned CLI tools;
- a **Helm chart** that deploys that environment two ways (Podman / Kubernetes);
- **self-checks** that guard the template's own invariants.

---

## Quick start

```bash
just build        # build the Execution Environment image
just connect      # open an interactive shell inside it
just check-tools  # verify the bundled CLI tools are present
just test         # verify the template's principles are intact
```

All recipes are discoverable with `just` (or `just --list`).

---

## Justfile — the entrypoint

Every project action is a recipe. Recipes are **grouped** and **delegate to a
script** in `./scripts/<group_name>/` — the justfile holds no ad-hoc shell
logic.

| Recipe        | Group                 | Script                                  | Runs on |
|---------------|-----------------------|-----------------------------------------|---------|
| `build`       | Execution Environment | `scripts/ee/manage.sh build`            | host    |
| `push`        | Execution Environment | `scripts/ee/manage.sh push`             | host    |
| `deploy`      | Execution Environment | `scripts/ee/manage.sh deploy`           | host    |
| `destroy`     | Execution Environment | `scripts/ee/manage.sh destroy`          | host    |
| `redeploy`    | Execution Environment | `scripts/ee/manage.sh redeploy`         | host    |
| `connect`     | Execution Environment | `scripts/ee/manage.sh connect`          | host    |
| `check-tools` | Utility               | `scripts/utility/check-tools.sh`        | container |
| `test`        | Utility               | `scripts/utility/test.sh`               | host    |

Private helpers (not user-facing): `default`, `_kubeconfig`, `_cmd`.

The `_cmd` bridge is what makes a script run the **same on the host and inside
the container**: inside the container/pod it runs the script directly, on the
host it launches the Execution Environment image and runs it there. The project
is always mounted at `/workspace` in both cases.

### Environment variables

Defined in the justfile, overridable via `.env` or the environment
(`set dotenv-load`).

| Variable         | Default             | Meaning                                  |
|------------------|---------------------|------------------------------------------|
| `REGISTRY_URL`   | *(empty)*           | Target registry for `just push`          |
| `EE_IMAGE`       | `localhost/toolkit` | Execution Environment image name         |
| `EE_VERSION`     | `latest`            | Image tag                                |
| `CONTAINER_TOOL` | `sudo podman`       | Container runtime used by `_cmd`         |
| `KUBECONFIG`     | `$HOME/.kube/config`| kubeconfig mounted into containers       |

---

## Execution Environment

A `Containerfile` at the repo root defines a reproducible image
(`localhost/toolkit` by default) that bundles the CLI tools needed to operate
the project, so a developer on WSL gets the same tooling as inside the
container or a pod.

Tools are **version-pinned** in the `Containerfile` for reproducibility:

- `kubectl`, `helm`, `helmfile`, `yq`, `just`
- `skopeo`, `ansible`, `sshpass`, `git`
- `jq`, `psql` (postgresql-client), `redis-cli` (redis-tools), `dig` (dnsutils)
- base utilities: `curl`, `wget`, `gnupg`, `tar`, `gzip`, `unzip`, `openssh-client`

`just check-tools` verifies the tools are present and functional at runtime.

---

## Helm Chart

`./helm` is the deployment definition for the Execution Environment image. It
renders the **same pod spec** in two modes, switched by `deployAs`:

- **`Pod`** (default) — rendered and run locally via `podman play kube`
  (`just deploy`), using the podman `emptyDir.path` extension to mount the repo
  at `/workspace`;
- **`Deployment`** — for a real Kubernetes cluster (adds replicas/selector;
  drop the podman-only bits and provide the project via a PVC/ConfigMap).

This keeps the Execution Environment identical across local and Kubernetes
deployments while changing only the runtime platform.

---

## Self-checks

| Command           | What it verifies                                        |
|-------------------|---------------------------------------------------------|
| `just test`       | The template's principles (structure, chart)            |
| `just check-tools`| The bundled CLI tools are present in the environment     |

`just test` is the guard for the template's own invariants — run it before
finishing any change.

---

## Principles

These invariants must hold for the template to stay a valid, reusable
boilerplate. `just test` verifies them.

1. **`justfile` is the entrypoint.** Every project action is a recipe; there is
   no other blessed way to build, run, or deploy.

2. **Recipes are grouped and delegate to scripts.** Each recipe belongs to a
   group and triggers a script under `./scripts/<group_name>/`. One directory
   per group — no ad-hoc shell in the justfile, no scripts outside `scripts/`.

3. **Execution Environment via `Containerfile`.** A reproducible image is
   defined by the repo-root `Containerfile`. `build`/`connect`/`deploy` give the
   same interface on the host (WSL) and inside the container/pod.

4. **Helm chart is the deployment definition.** `./helm` renders the same pod
   spec two ways — `Pod` (local, `podman play kube`) and `Deployment`
   (Kubernetes) — switched by `deployAs`.

---

## Layout

```txt
.
├── justfile                  # entrypoint — all recipes
├── Containerfile             # Execution Environment image
├── README.md                 # this file
├── .gitignore                # ignores .local/**
├── .local/                   # workflow scratch — never committed
├── scripts/
│   ├── ee/manage.sh          # build / push / deploy / destroy / redeploy / connect
│   └── utility/
│       ├── check-tools.sh    # verify CLI tools
│       └── test.sh           # verify principles
├── helm/                     # deployment definition (Chart + values + templates)
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/            # _podspec.tpl, pod.yaml, deployment.yaml
├── config/site.yaml          # mounted into the pod at /workspace/config
└── docs/procedures/01-repo-contract.md   # LLM/agent contributor contract
```

---

## Project status

This is a template to kickstart almost any project. Use it wisely.
