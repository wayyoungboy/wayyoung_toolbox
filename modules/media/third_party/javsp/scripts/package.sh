#!/bin/sh
set -eu
LC_ALL=C
export LC_ALL

fail() {
    printf 'package: %s\n' "$*" >&2
    exit 1
}

validate_version() {
    candidate=$1
    [ -n "$candidate" ] || fail "VERSION must not be empty"
    case "$candidate" in
        */*) fail "VERSION must not contain /" ;;
        *\\*) fail "VERSION must not contain \\" ;;
        *..*) fail "VERSION must not contain .." ;;
        *[!A-Za-z0-9._+-]*) fail "VERSION contains a character outside A-Z, a-z, 0-9, dot, underscore, plus, and hyphen" ;;
    esac
}

if [ "$#" -eq 2 ] && [ "$1" = --validate-version ]; then
    validate_version "$2"
    exit 0
fi

[ "$#" -eq 5 ] || {
    printf '%s\n' 'usage: scripts/package.sh --validate-version VERSION' >&2
    printf '%s\n' '       scripts/package.sh VERSION GOOS GOARCH BINARY DIST_DIR' >&2
    exit 2
}

version=$1
validate_version "$version"

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
project_root=$(dirname "$script_dir")

goos=$2
goarch=$3
binary=$4
dist_dir=$5

case "$goos/$goarch" in
    darwin/amd64|darwin/arm64|linux/amd64|linux/arm64|windows/amd64|windows/arm64) ;;
    *) fail "unsupported target $goos/$goarch" ;;
esac

[ -f "$binary" ] && [ ! -L "$binary" ] || {
    printf 'package: binary not found: %s\n' "$binary" >&2
    exit 1
}

for required_file in config.yml README.md LICENSE THIRD_PARTY_NOTICES.md; do
    [ -f "$project_root/$required_file" ] && [ ! -L "$project_root/$required_file" ] || {
        printf 'package: required regular file not found: %s\n' "$required_file" >&2
        exit 1
    }
done
for required_dir in data image docs; do
    [ -d "$project_root/$required_dir" ] && [ ! -L "$project_root/$required_dir" ] || {
        printf 'package: required directory not found: %s\n' "$required_dir" >&2
        exit 1
    }
done

unsafe_source=$(find \
    "$project_root/config.yml" "$project_root/README.md" "$project_root/LICENSE" \
    "$project_root/THIRD_PARTY_NOTICES.md" "$project_root/data" "$project_root/image" \
    "$project_root/docs" ! -type f ! -type d -print -quit) || {
    printf '%s\n' 'package: cannot inspect source tree' >&2
    exit 1
}
[ -z "$unsafe_source" ] || {
    printf 'package: source tree contains a non-regular entry: %s\n' "$unsafe_source" >&2
    exit 1
}

hardlinked_source=$(find \
    "$project_root/config.yml" "$project_root/README.md" "$project_root/LICENSE" \
    "$project_root/THIRD_PARTY_NOTICES.md" "$project_root/data" "$project_root/image" \
    "$project_root/docs" -type f -links +1 -print -quit) || {
    printf '%s\n' 'package: cannot inspect source hard links' >&2
    exit 1
}
[ -z "$hardlinked_source" ] || {
    printf 'package: source tree contains a hard-linked file: %s\n' "$hardlinked_source" >&2
    exit 1
}

top_dir="javsp-go_${version}_${goos}_${goarch}"
stage_dir="$dist_dir/$top_dir"
rm -rf "$stage_dir"
mkdir -p "$stage_dir"

executable=javsp
[ "$goos" != windows ] || executable=javsp.exe
cp "$binary" "$stage_dir/$executable"
chmod 755 "$stage_dir/$executable"
cp "$project_root/config.yml" "$project_root/README.md" "$project_root/LICENSE" \
    "$project_root/THIRD_PARTY_NOTICES.md" "$stage_dir/"
cp -R "$project_root/data" "$project_root/image" "$project_root/docs" "$stage_dir/"

case "$goos" in
    windows)
        archive_name="$top_dir.zip"
        (cd "$dist_dir" && zip -qr "$archive_name" "$top_dir")
        ;;
    *)
        archive_name="$top_dir.tar.gz"
        (cd "$dist_dir" && tar -czf "$archive_name" "$top_dir")
        ;;
esac

rm -rf "$stage_dir"
printf 'created %s/%s\n' "$dist_dir" "$archive_name"
