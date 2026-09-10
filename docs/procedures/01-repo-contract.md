# Repo Contract — Simpl-inventory template

> Contract for LLM/agent contributors. Read this before making any change.
> It is the normative summary of the project's invariants; the human-facing
> version lives in `README.md` ("Principles"). When the two disagree, this file
> and `just test` win.

## 1. What this repo is

A reusable project template ("boilerplate"). It delivers, in order of
importance:

1. a `justfile` entrypoint,
2. grouped `scripts/<group>/` task scripts,
3. an Execution Environment image (`Containerfile`),
4. a Helm chart that deploys that environment two ways.

Everything else is scaffolding around those four.

## 2. Hard invariants (must not regress)

These are verified by `just test` → `scripts/utility/test.sh`. Never merge a
change that fails them.

1. **`justfile` is the entrypoint.** Every project action is a recipe. No other
   blessed way to build, run, or deploy. The justfile must parse
   (`just --list`).

2. **Recipes are grouped and delegate to scripts.** Each recipe belongs to a
   group and invokes a script under `./scripts/<group_name>/`. One directory per
   group. No ad-hoc shell logic lives in the justfile body; no scripts live
   outside `scripts/`.

3. **Execution Environment via `Containerfile`.** The repo-root `Containerfile`
   defines a reproducible tool image. `build`/`connect`/`deploy` give the same
   interface on the host (WSL) and inside the container/pod.

4. **Helm chart is the deployment definition.** `./helm` renders the same pod
   spec in two modes — `Pod` (local `podman play kube`) and `Deployment`
   (Kubernetes) — switched by `deployAs`. Both must render.

## 3. Directory map

```
.
├── justfile              # entrypoint — all recipes defined here
├── Containerfile         # Execution Environment image
├── README.md             # human-facing docs + "Principles"
├── .gitignore            # ignores .local/**
├── .local/               # workflow scratch — NEVER committed
├── scripts/
│   ├── ee/
│   │   └── manage.sh     # build / push / deploy / destroy / redeploy / connect
│   └── utility/
│       ├── check-tools.sh# verifies CLI tools in the EE image
│       └── test.sh       # verifies this contract's invariants
├── helm/                 # deployment definition (Chart + values + templates)
│   ├── Chart.yaml
│   ├── values.yaml       # deployAs, image, command, workspace
│   └── templates/
│       ├── _podspec.tpl  # shared pod spec
│       ├── pod.yaml      # Pod mode (podman play kube)
│       └── deployment.yaml # Deployment mode (real k8s)
├── config/
│   └── site.yaml         # mounted into the pod at /workspace/config
└── docs/
    └── procedures/
        └── 01-repo-contract.md   # this file
```

## 4. The justfile recipes

| Recipe      | Group                 | Delegates to                         | Runs on |
|-------------|-----------------------|--------------------------------------|---------|
| `build`     | Execution Environment | `scripts/ee/manage.sh build`         | host    |
| `push`      | Execution Environment | `scripts/ee/manage.sh push`          | host    |
| `deploy`    | Execution Environment | `scripts/ee/manage.sh deploy`        | host    |
| `destroy`   | Execution Environment | `scripts/ee/manage.sh destroy`       | host    |
| `redeploy`  | Execution Environment | `scripts/ee/manage.sh redeploy`      | host    |
| `connect`   | Execution Environment | `scripts/ee/manage.sh connect`       | host    |
| `check-tools` | Utility             | `scripts/utility/check-tools.sh`     | container (`_cmd`) |
| `test`      | Utility               | `scripts/utility/test.sh`            | host    |

Private helpers (not user-facing): `default`, `_kubeconfig`, `_cmd`.

### The `_cmd` bridge (principle 3 in action)

`_cmd <group>/<script>.sh` runs a script identically on host and inside the
container:

- **inside the container/pod** (k8s env, service-account token, or no
  podman/docker on `PATH`) → run `bash /workspace/scripts/<script>` directly;
- **on the host** → `podman run --rm -v <project>:/workspace …` then run the
  script inside.

The project is always mounted at `/workspace` — locally (via `_cmd`/`connect`)
and in the pod (via the chart's `workspace` volume).

## 5. Environment variables

Defined in `justfile`; overridable via `.env` (dotenv-load) or environment.

| Var              | Default            | Meaning                                |
|------------------|--------------------|----------------------------------------|
| `REGISTRY_URL`   | `""`               | Target registry for `push`            |
| `EE_IMAGE`       | `localhost/toolkit`| Execution Environment image name       |
| `EE_VERSION`     | `latest`           | Image tag                              |
| `CONTAINER_TOOL` | `sudo podman`      | Container runtime used by `_cmd`       |
| `KUBECONFIG`     | `$HOME/.kube/config` | kubeconfig mounted into containers   |

Helm values mirror these (`helm/values.yaml`): `deployAs`, `image.*`,
`command`, `workspace.*`.

## 6. Naming and style

- Unified service identity: **`toolkit`** (image, chart, release, pod name,
  container name). Do not introduce a second name for the same thing.
- Shell scripts: `#!/usr/bin/env bash`, `set -euo pipefail`, functions + a
  subcommand `case` dispatcher (see `scripts/ee/manage.sh`).
- `manage.sh` functions call `sudo podman` directly; the justfile does not
  re-wrap them in sudo.

## 7. When you change something

1. If you add a recipe → add it under an existing group, have it call a script
   in `scripts/<group>/`, and document it here (§4) and in README.
2. If you touch `scripts/` → each `.sh` must stay `bash -n` clean.
3. If you touch the Helm chart → confirm both `Pod` and `Deployment` render.
4. If you touch the image → keep `Containerfile` at the repo root.
5. Before finishing, run **`just test`** and make it pass.

## 8. Anti-goals (do NOT do)

- Don't put executable logic directly in a justfile recipe body.
- Don't create scripts outside `scripts/<group>/`.
- Don't commit anything under `.local/`.
- Don't build a second entrypoint that bypasses the justfile.
