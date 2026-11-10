#!/usr/bin/env bash
# Regenerate (default) or check (--check) the deterministic mock scenario fixtures.
# Offline only: no provider keys, network calls or real phone calls.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
args=(-out docs/fixtures)
for arg in "$@"; do
  case "$arg" in
    --check) args+=(-check) ;;
    --seed=*) args+=(-seed "${arg#--seed=}") ;;
    *) echo "usage: scripts/scenarios/generate.sh [--check] [--seed=N]" >&2; exit 2 ;;
  esac
done
exec go run ./packages/go/mock/cmd/scenarios "${args[@]}"
