#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
registry="$repo_root/proto/registry.yaml"

if [[ ! -f "$registry" ]]; then
  echo "proto registry is missing: $registry" >&2
  exit 1
fi

actual=$(mktemp)
declared=$(mktemp)
trap 'rm -f "$actual" "$declared"' EXIT

find "$repo_root/proto" -type f -name '*.proto' -printf '%P\n' | sort >"$actual"
sed -n 's/^[[:space:]]*- source: //p' "$registry" | sort >"$declared"

if [[ -n $(comm -23 "$actual" "$declared") ]]; then
  echo "proto sources missing from registry:" >&2
  comm -23 "$actual" "$declared" >&2
  exit 1
fi

if [[ -n $(comm -13 "$actual" "$declared") ]]; then
  echo "registry entries with no proto source:" >&2
  comm -13 "$actual" "$declared" >&2
  exit 1
fi

if [[ -n $(uniq -d "$declared") ]]; then
  echo "duplicate proto registry sources:" >&2
  uniq -d "$declared" >&2
  exit 1
fi

canonical_count=$(sed -n 's/^[[:space:]]*status: canonical[[:space:]]*$/canonical/p' "$registry" | wc -l)
source_count=$(wc -l <"$actual")
if [[ "$canonical_count" -ne "$source_count" ]]; then
  echo "every proto source must have exactly one canonical registry status: sources=$source_count canonical=$canonical_count" >&2
  exit 1
fi

if rg -q '^[[:space:]]*status: legacy[[:space:]]*$|^[[:space:]]*migration_wave:' "$registry"; then
  echo "legacy proto registry entries remain after canonical migration" >&2
  exit 1
fi

echo "proto registry covers $source_count canonical workflow sources"
