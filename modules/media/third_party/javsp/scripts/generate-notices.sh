#!/usr/bin/env bash
# Generate THIRD_PARTY_NOTICES.md content from the linked javsp dependency graph.
set -euo pipefail

if ! command -v jq >/dev/null 2>&1; then
  echo "generate-notices.sh requires jq" >&2
  exit 1
fi

cd "$(dirname "${BASH_SOURCE[0]}")/.."
modules_file="$(mktemp)"
trap 'rm -f "$modules_file"' EXIT

go list -deps -json ./cmd/javsp |
  jq -r '
    select(.Module != null and .Module.Path != "github.com/yuukiy/javsp-go") |
    [.Module.Path, .Module.Version, .Module.Dir] | @tsv
  ' |
  LC_ALL=C sort -u >"$modules_file"

if [[ ! -s "$modules_file" ]]; then
  echo "no non-standard-library modules found" >&2
  exit 1
fi

printf '%s\n\n' '# Third-Party Notices'
printf '%s\n\n' 'This file is generated from `go list -deps -json ./cmd/javsp` by `scripts/generate-notices.sh`. It records every non-standard-library module linked by that command and reproduces each module-root license, notice, or copyright file verbatim.'

printf '%s\n\n' '## Pigo `facefinder` cascade'
printf '%s\n' '- Linked module: `github.com/esimov/pigo` `v1.4.6` (runtime package: `github.com/esimov/pigo/core`).'
printf '%s\n' '- Package source: https://github.com/esimov/pigo/tree/v1.4.6'
printf '%s\n' '- Model: `data/facefinder`, copied unchanged from `cascade/facefinder`.'
printf '%s\n' '- Model source: https://github.com/esimov/pigo/blob/v1.4.6/cascade/facefinder'
printf '%s\n' '- Model SHA-256: `d8014993e7298c7b1865d1f8b855d6dbf4ec5c808bf879e2091ab6837abf90cd`.'
printf '%s\n\n' '- The Pigo module license is reproduced with the complete linked-module notices below.'

printf '%s\n\n' '## Linked Go module notices'

found_pigo=0
while IFS=$'\t' read -r module_path version module_dir; do
  if [[ "$module_path" == 'github.com/esimov/pigo' ]]; then
    found_pigo=1
  fi

  notice_files=()
  while IFS= read -r notice_file; do
    notice_files+=("$notice_file")
  done < <(
      find "$module_dir" -maxdepth 1 -type f \
        \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -iname 'COPYRIGHT*' \) \
        -print | LC_ALL=C sort
    )
  if [[ ${#notice_files[@]} -eq 0 ]]; then
    echo "no module-root license, notice, or copyright file for $module_path $version" >&2
    exit 1
  fi

  printf '### `%s` — `%s`\n\n' "$module_path" "$version"
  printf '%s' 'Module-root notice files reproduced below:'
  for notice_file in "${notice_files[@]}"; do
    printf ' `%s`' "${notice_file##*/}"
  done
  printf '\n\n'
  for notice_file in "${notice_files[@]}"; do
    printf '#### `%s`\n\n```text\n' "${notice_file##*/}"
    sed 's/\r$//' "$notice_file"
    printf '\n```\n\n'
  done
done <"$modules_file"

if [[ "$found_pigo" -ne 1 ]]; then
  echo 'linked dependency list did not contain github.com/esimov/pigo' >&2
  exit 1
fi
