#!/bin/sh
set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
project_root=$(dirname "$script_dir")
dist_dir=${1:-dist}

fail() {
    printf 'verify-release: %s\n' "$*" >&2
    exit 1
}

command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v unzip >/dev/null 2>&1 || fail "unzip is required"
[ -d "$dist_dir" ] || fail "release directory not found: $dist_dir"

for source_dir in data image docs; do
    [ -d "$project_root/$source_dir" ] && [ ! -L "$project_root/$source_dir" ] || \
        fail "required source directory not found: $project_root/$source_dir"
done

unsafe_source=$(find "$project_root/data" "$project_root/image" "$project_root/docs" \
    ! -type f ! -type d -print -quit) || fail "cannot inspect required source directories"
[ -z "$unsafe_source" ] || fail "source tree contains a non-regular entry: $unsafe_source"

hardlinked_source=$(find "$project_root/data" "$project_root/image" "$project_root/docs" \
    -type f -links +1 -print -quit) || fail "cannot inspect source hard links"
[ -z "$hardlinked_source" ] || fail "source tree contains a hard-linked file: $hardlinked_source"

archive_count=$(find "$dist_dir" -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l | tr -d ' ')
[ "$archive_count" = 6 ] || fail "expected exactly 6 release archives, found $archive_count"

version=
for archive in "$dist_dir"/javsp-go_*.tar.gz "$dist_dir"/javsp-go_*.zip; do
    [ -f "$archive" ] || continue
    name=${archive##*/}
    case "$name" in
        *.tar.gz) base=${name%.tar.gz} ;;
        *.zip) base=${name%.zip} ;;
        *) fail "unsupported archive: $name" ;;
    esac
    case "$base" in
        javsp-go_*_darwin_amd64) target_suffix=_darwin_amd64 ;;
        javsp-go_*_darwin_arm64) target_suffix=_darwin_arm64 ;;
        javsp-go_*_linux_amd64) target_suffix=_linux_amd64 ;;
        javsp-go_*_linux_arm64) target_suffix=_linux_arm64 ;;
        javsp-go_*_windows_amd64) target_suffix=_windows_amd64 ;;
        javsp-go_*_windows_arm64) target_suffix=_windows_arm64 ;;
        *) fail "invalid archive name: $name" ;;
    esac
    archive_version=${base#javsp-go_}
    archive_version=${archive_version%"$target_suffix"}
    [ -n "$archive_version" ] || fail "archive version is empty: $name"
    if [ -z "$version" ]; then
        version=$archive_version
    elif [ "$version" != "$archive_version" ]; then
        fail "archives contain multiple versions: $version and $archive_version"
    fi
done
[ -n "$version" ] || fail "no release archives found"

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

verify_member() {
    member=$1
    archive_name=$2
    if ! grep -Fqx "$member" "$members_file"; then
        fail "$archive_name is missing $member"
    fi
    extracted_member="$extract_dir/$member"
    [ -f "$extracted_member" ] && [ ! -L "$extracted_member" ] || \
        fail "$archive_name member is not a regular file: $member"
}

verify_tree() {
    source_dir=$1
    top_dir=$2
    archive_name=$3
    source_list="$tmp_dir/source-$source_dir"
    find "$project_root/$source_dir" -type f -print >"$source_list.unsorted" || \
        fail "cannot enumerate $project_root/$source_dir"
    LC_ALL=C sort "$source_list.unsorted" >"$source_list" || \
        fail "cannot sort $project_root/$source_dir"
    while IFS= read -r source_file; do
        relative_file=${source_file#"$project_root"/}
        verify_member "$top_dir/$relative_file" "$archive_name"
    done <"$source_list"
}

verify_archive() {
    archive=$1
    os=$2
    arch=$3
    extension=$4
    executable=$5
    top_dir="javsp-go_${version}_${os}_${arch}"
    archive_name="${top_dir}.${extension}"
    [ -f "$archive" ] || fail "missing archive: $archive_name"

    members_file="$tmp_dir/${os}-${arch}.members"
    types_file="$tmp_dir/${os}-${arch}.types"
    extract_dir="$tmp_dir/${os}-${arch}.extract"
    mkdir -p "$extract_dir"
    case "$extension" in
        tar.gz)
            tar -tzf "$archive" >"$members_file" || fail "cannot list $archive_name"
            tar -tvzf "$archive" >"$types_file" || fail "cannot inspect $archive_name"
            invalid_entry=$(awk 'substr($0, 1, 1) != "-" && substr($0, 1, 1) != "d" { print; exit }' "$types_file")
            [ -z "$invalid_entry" ] || fail "$archive_name contains a non-regular or link entry: $invalid_entry"
            tar -xzf "$archive" -C "$extract_dir" || fail "cannot extract $archive_name"
            ;;
        zip)
            unzip -Z1 "$archive" >"$members_file" || fail "cannot list $archive_name"
            unzip -Z -l "$archive" >"$types_file" || fail "cannot inspect $archive_name"
            invalid_entry=$(awk '$2 ~ /^[0-9][0-9.]*$/ && $3 == "unx" && substr($1, 1, 1) != "-" && substr($1, 1, 1) != "d" { print; exit }' "$types_file")
            [ -z "$invalid_entry" ] || fail "$archive_name contains a non-regular or link entry: $invalid_entry"
            unzip -q "$archive" -d "$extract_dir" || fail "cannot extract $archive_name"
            ;;
        *) fail "unsupported archive format: $extension" ;;
    esac

    [ -s "$members_file" ] || fail "$archive_name is empty"
    if grep -Eq '(^/|(^|/)\.\.(/|$))' "$members_file"; then
        fail "$archive_name contains an unsafe path"
    fi
    if awk -F/ -v top="$top_dir" '$1 != top { exit 1 }' "$members_file"; then
        :
    else
        fail "$archive_name contains files outside $top_dir"
    fi
    duplicate_member=$(LC_ALL=C sort "$members_file" | uniq -d | sed -n '1p')
    [ -z "$duplicate_member" ] || fail "$archive_name contains duplicate member: $duplicate_member"

    archived_link=$(find "$extract_dir" ! -type f ! -type d -print -quit) || \
        fail "cannot inspect extracted $archive_name"
    [ -z "$archived_link" ] || fail "$archive_name contains a non-regular extracted entry: $archived_link"
    archived_hardlink=$(find "$extract_dir" -type f -links +1 -print -quit) || \
        fail "cannot inspect extracted hard links in $archive_name"
    [ -z "$archived_hardlink" ] || fail "$archive_name contains a hard-linked file: $archived_hardlink"

    verify_member "$top_dir/$executable" "$archive_name"
    verify_member "$top_dir/config.yml" "$archive_name"
    verify_member "$top_dir/README.md" "$archive_name"
    verify_member "$top_dir/LICENSE" "$archive_name"
    verify_member "$top_dir/THIRD_PARTY_NOTICES.md" "$archive_name"
    verify_tree data "$top_dir" "$archive_name"
    verify_tree image "$top_dir" "$archive_name"
    verify_tree docs "$top_dir" "$archive_name"

    case "$os" in
        windows)
            if grep -Fqx "$top_dir/javsp" "$members_file"; then
                fail "$archive_name contains incorrectly named executable javsp"
            fi
            ;;
        *)
            if grep -Fqx "$top_dir/javsp.exe" "$members_file"; then
                fail "$archive_name contains incorrectly named executable javsp.exe"
            fi
            ;;
    esac
}

verify_archive "$dist_dir/javsp-go_${version}_darwin_amd64.tar.gz" darwin amd64 tar.gz javsp
verify_archive "$dist_dir/javsp-go_${version}_darwin_arm64.tar.gz" darwin arm64 tar.gz javsp
verify_archive "$dist_dir/javsp-go_${version}_linux_amd64.tar.gz" linux amd64 tar.gz javsp
verify_archive "$dist_dir/javsp-go_${version}_linux_arm64.tar.gz" linux arm64 tar.gz javsp
verify_archive "$dist_dir/javsp-go_${version}_windows_amd64.zip" windows amd64 zip javsp.exe
verify_archive "$dist_dir/javsp-go_${version}_windows_arm64.zip" windows arm64 zip javsp.exe

printf 'verified 6 release archives for version %s\n' "$version"
