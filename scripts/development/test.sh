#!/usr/bin/env bash
set -euo pipefail

# Test dispatcher for the Development justfile group. Run from the repo root:
#   just test-unit            (preferred)
#   bash scripts/development/test.sh unit integration
#
# Layers (run in the given order; default: unit bdd):
#   unit         fast Go tests: parsers, config, command generation, API mapping
#   integration  tagged Colt/Gitea vertical flow plus live backend probes
#                (needs CONTAINER_TOOL, default podman, and registry access)
#   bdd          deterministic godog scenarios with local fixtures only
#   blackbox     real colt binary smoke-test in a sandbox (build tag: bdd)
#   coverage     regenerate .local/bdd-coverage.md (deterministic output)
#
# Environment honored throughout: CONTAINER_TOOL, GITEA_IMAGE, FORGEJO_IMAGE,
# GITEA_PORT, FORGEJO_PORT, COLT_IT_KEEP, GODOG_FORMAT.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

layers=("$@")
if [ "${#layers[@]}" -eq 0 ]; then
    layers=(unit bdd)
fi

for layer in "${layers[@]}"; do
    echo "--- test layer: $layer ---"
    case "$layer" in
        unit)
            go test ./internal/... ./cmd/... -count=1
            ;;
        integration)
            go test -tags integration ./integration/ -count=1 -timeout 15m
            ;;
        bdd)
            go test ./tests/bdd/... -count=1
            ;;
        blackbox)
            go test -tags bdd ./tests/bdd/... -run 'TestBlackbox' -count=1
            ;;
        coverage)
            go test ./tests/bdd/ -run '^TestBDDRequirementIndex$' -count=1 -args -update-bdd-coverage
            ;;
        *)
            echo "unknown test layer: $layer (want: unit integration bdd blackbox coverage)" >&2
            exit 2
            ;;
    esac
done
