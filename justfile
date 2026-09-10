set shell := ["bash", "-eu", "-o", "pipefail", "-c"]
set dotenv-load

# Global configuration (override via .env or environment).
registry_url := env_var_or_default("REGISTRY_URL", "")
ee_image := env_var_or_default("EE_IMAGE", "localhost/toolkit")
ee_version := env_var_or_default("EE_VERSION", "latest")
container_tool := env_var_or_default("CONTAINER_TOOL", "sudo podman")
registry_port := env_var_or_default("REGISTRY_PORT", "5000")

# ── Global ────────────────────────────────────────────────────────
[private]
default:
    @just --list --unsorted

[private]
_kubeconfig := if env_var_or_default("KUBECONFIG", "") != "" { env_var("KUBECONFIG") } else { env_var("HOME") + "/.kube/config" }

# Run scripts/<group>/<script>.sh in the EE/toolkit environment: launch the
# container on the host, or run directly when already inside it (k8s pod, or a
# locally-launched toolkit container where no podman/docker is on PATH).
[private]
_cmd command:
    if [ -n "${KUBERNETES_SERVICE_HOST:-}" ] || [ -f /run/secrets/kubernetes.io/serviceaccount/token ] || { ! command -v podman >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; }; then \
        bash /workspace/scripts/{{ command }}; \
    else \
        KUBE_ARGS=(); if [ -f "{{ _kubeconfig }}" ]; then KUBE_ARGS=(-v "{{ _kubeconfig }}:/root/.kube/config:ro,z" -e KUBECONFIG=/root/.kube/config); fi; \
        {{ container_tool }} run --rm \
        -v "{{ justfile_directory() }}:/workspace:z" \
        "${KUBE_ARGS[@]}" \
        -e REGISTRY_URL={{ registry_url }} \
        -w /workspace \
        {{ ee_image }}:{{ ee_version }} \
        bash /workspace/scripts/{{ command }}; \
    fi

# ── Development — cmd/simpl-inventory + internal/ ─────────────────

# Build the binary. [This is just an example]
[group('Development')]
compile:
    @CGO_ENABLED=0 go build -buildvcs=false -o bin/example ./cmd/example

# Run check the developer/support environment.
[group('Development')]
test:
    bash "{{ justfile_directory() }}/scripts/utility/test.sh"

# Run every active deterministic BDD scenario (local fixtures only).
[group('Development')]
test-bdd:
    

# Verify all CLI tools are present in the EE image. Presence check only: runs
# the image with a read-only workspace and no Kubernetes credential mount.
[group('Development')]
check-tools:
    @if [ -n "${KUBERNETES_SERVICE_HOST:-}" ] || [ -f /run/secrets/kubernetes.io/serviceaccount/token ] || { ! command -v podman >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; }; then \
        bash /workspace/scripts/utility/check-tools.sh; \
    else \
        {{ container_tool }} run --rm -v "{{ justfile_directory() }}:/workspace:ro,z" -w /workspace {{ ee_image }}:{{ ee_version }} bash /workspace/scripts/utility/check-tools.sh; \
    fi

# ── Execution Environment — scripts/ee/ ───────────────────────────

# Build the Execution Environment container image.
[group('Execution Environment')]
build:
    REGISTRY_URL="{{ registry_url }}" EE_IMAGE="{{ ee_image }}" EE_VERSION="{{ ee_version }}" bash "{{ justfile_directory() }}/scripts/ee/manage.sh" build

# Push the Execution Environment image to the private registry.
[group('Execution Environment')]
push:
    REGISTRY_URL="{{ registry_url }}" EE_IMAGE="{{ ee_image }}" EE_VERSION="{{ ee_version }}" bash "{{ justfile_directory() }}/scripts/ee/manage.sh" push

# Deploy the toolkit via the Helm chart (podman play kube).
[group('Execution Environment')]
deploy:
    REGISTRY_URL="{{ registry_url }}" EE_IMAGE="{{ ee_image }}" EE_VERSION="{{ ee_version }}" bash "{{ justfile_directory() }}/scripts/ee/manage.sh" deploy

# Tear down the deployed toolkit pod.
[group('Execution Environment')]
destroy:
    REGISTRY_URL="{{ registry_url }}" EE_IMAGE="{{ ee_image }}" EE_VERSION="{{ ee_version }}" bash "{{ justfile_directory() }}/scripts/ee/manage.sh" destroy

# Destroy then redeploy.
[group('Execution Environment')]
redeploy:
    REGISTRY_URL="{{ registry_url }}" EE_IMAGE="{{ ee_image }}" EE_VERSION="{{ ee_version }}" bash "{{ justfile_directory() }}/scripts/ee/manage.sh" redeploy

# Launch the EE toolkit container and drop into an interactive shell.
[group('Execution Environment')]
connect:
    REGISTRY_URL="{{ registry_url }}" EE_IMAGE="{{ ee_image }}" EE_VERSION="{{ ee_version }}" bash "{{ justfile_directory() }}/scripts/ee/manage.sh" connect
