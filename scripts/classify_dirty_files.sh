#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "${repo_root}" ]]; then
  echo "Not inside a git repository" >&2
  exit 1
fi

cd "$repo_root"
status_lines="$(git status --short)"

if [[ -z "${status_lines}" ]]; then
  echo "No dirty files. Working tree is clean."
  exit 0
fi

artifacts=()
generated=()
source=()

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  path="${line:3}"

  if [[ "$path" =~ ^tmp/ ]] || [[ "$path" =~ \.(log|out|err|exe)$ ]] || [[ "$path" == ".coverage" ]] || [[ "$path" =~ ^\.pytest_cache/ ]]; then
    artifacts+=("$line")
  elif [[ "$path" =~ ^go-engine/sqlc/ ]] || [[ "$path" =~ ^dist/ ]] || [[ "$path" =~ ^build/ ]]; then
    generated+=("$line")
  else
    source+=("$line")
  fi
done <<< "$status_lines"

print_group() {
  local name="$1"
  shift
  local items=("$@")
  echo
  echo "[$name]"
  if [[ ${#items[@]} -eq 0 ]]; then
    echo "(none)"
    return
  fi
  for item in "${items[@]}"; do
    echo "$item"
  done
}

print_group "INTENTIONAL_SOURCE_REVIEW" "${source[@]}"
print_group "GENERATED_REVIEW" "${generated[@]}"
print_group "ARTIFACT_REVIEW" "${artifacts[@]}"
