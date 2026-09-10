#!/usr/bin/env bash
set -euo pipefail

# Verify the template's core principles are intact. Run from the repo root:
#   just test            (preferred)
#   bash scripts/utility/test.sh

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

fail=0
check() {
    local desc="$1"; shift
    if "$@" >/dev/null 2>&1; then
        printf '  ok    %s\n' "$desc"
    else
        printf '  FAIL  %s\n' "$desc"
        fail=1
    fi
}

echo "template principles"
echo

echo "[1] justfile is the entrypoint"
check "justfile present" test -f justfile
check "justfile parses"  just --list --unsorted

echo
echo "[2] grouped recipes trigger scripts under scripts/<group>/"
check "scripts/ dir present" test -d scripts
for d in ee utility; do
    check "scripts/$d has scripts" test -n "$(find "scripts/$d" -name '*.sh' -print -quit 2>/dev/null)"
done

echo
echo "[3] Execution Environment via Containerfile"
check "Containerfile present" test -f Containerfile

echo
echo "[4] Helm chart renders for Pod and Deployment"
check "helm renders Pod"        helm template toolkit ./helm
check "helm renders Deployment" helm template toolkit ./helm --set deployAs=Deployment

echo
echo "[5] every shell script is valid bash"
while IFS= read -r f; do
    check "bash -n $f" bash -n "$f"
done < <(find scripts -name '*.sh' | sort)

echo
if [ "$fail" -eq 0 ]; then
    echo "PASS"
else
    echo "FAIL"
    exit 1
fi
