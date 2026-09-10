#!/usr/bin/env bash
set -euo pipefail

# Verify the toolkit's CLI tools are present. Runs inside the toolkit image
# via `just check-tools`.

tools=(kubectl helm helmfile yq just jq skopeo psql redis-cli dig ansible sshpass git)

missing=()
for t in "${tools[@]}"; do
  if command -v "${t}" >/dev/null 2>&1; then
    printf '  ok    %s\n' "${t}"
  else
    printf '  MISS  %s\n' "${t}" >&2
    missing+=("${t}")
  fi
done

if [ "${#missing[@]}" -ne 0 ]; then
  echo "error: ${#missing[@]} tool(s) missing: ${missing[*]}" >&2
  exit 1
fi

echo "All ${#tools[@]} tools present."