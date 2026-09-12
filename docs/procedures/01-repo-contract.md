# Colt repository contract

> Normative contributor summary. Product behavior is specified in `docs/specs/`
> and `features/`; this file describes how the repository is worked on.

## What this repository is

Colt is a Go CLI for working consistently with repositories across supported Git
hosting providers. The product lives in `cmd/colt` and `internal`, with behavior
specified in `docs/specs` and exercised by unit and BDD tests.

The root `justfile` is a convenience task index, not a product boundary or the
only valid entrypoint. Recipes delegate substantial work to `scripts/` so the
underlying commands remain usable directly and in automation.

## Required product gate

Run this before merging:

```sh
just test
```

It runs the default Colt suite (`unit` and deterministic product BDD tests), then
the lightweight repository check. The equivalent direct commands are:

```sh
bash scripts/development/test.sh
bash scripts/development/test_core_template.sh
```

`go vet ./...` is an additional CI gate. Container-backed integration tests are
deliberately not part of the default gate.

## Test lanes

- **Unit:** colocated Go tests under `internal/`; no network or containers.
- **BDD:** active scenarios in `features/`, driven by `tests/bdd/...` with local
  fixtures. This is Colt product behavior.
- **Blackbox:** builds and invokes the real Colt binary in an isolated local
  sandbox; no containers.
- **Integration:** runs the real Colt-to-Gitea and Colt-to-Forgejo initialization/push flows and optional Gitea/Forgejo backend probes in ephemeral containers. CI runs both vertical scenarios; the full lane also runs the characterization probes. Run the full lane explicitly with `just test-integration`.

See `tests/README.md` for commands, tags, and test layout.

## Repository map

```text
cmd/colt/       CLI entrypoint
internal/       Colt product packages and unit tests
features/       executable behavior specifications
tests/bdd/      BDD suite, fixtures, steps, and blackbox test
integration/    live Gitea and Forgejo vertical tests and optional backend probes
scripts/        direct development and optional environment tasks
Containerfile   optional development Execution Environment image
helm/           optional chart for that environment
justfile        convenience recipes
```

## Optional Execution Environment and Helm support

The `Containerfile`, `scripts/ee/manage.sh`, and `helm/` chart provide an optional
tooling environment. They are not the Colt application or required to run its Go
tests. The chart supports `Pod` and `Deployment` rendering through `deployAs`;
if it is changed, render both modes before merging:

```sh
helm template toolkit ./helm
helm template toolkit ./helm --set deployAs=Deployment
```

Relevant settings remain overridable through `.env` or the environment:
`REGISTRY_URL`, `EE_IMAGE` (default `localhost/toolkit`), `EE_VERSION` (default
`latest`), `CONTAINER_TOOL` (default `sudo podman`), and `KUBECONFIG`.

## Change rules

1. Keep product requirements, feature scenarios, and implementation aligned.
2. Put substantial recipe logic in the existing `scripts/<group>/` area.
3. Keep shell scripts valid under `bash -n`; do not embed credentials.
4. Do not commit `.local/` scratch output.
5. Run `just test`; run the optional lane relevant to anything else changed.
