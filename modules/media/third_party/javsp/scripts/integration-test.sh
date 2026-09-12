#!/bin/sh
# Recovery-safe real integration harness for the local ATID-687 fixture.
set -eu

usage() {
	cat <<'EOF'
Usage:
  scripts/integration-test.sh
  scripts/integration-test.sh --restore-only
  scripts/integration-test.sh --validate-only
  scripts/integration-test.sh --validate-xml PATH

Full runs require JAVSP_TEST_PROXY to contain a socks5:// or socks5h:// URL.
--restore-only proves fixture restoration without network access.
--validate-only checks physical fixture roots and manifests without mutation.
--validate-xml validates one ordinary XML file with required xmllint only.
EOF
}

MODE=full
XML_TARGET=
case "$#" in
0)
	;;
1)
	case "$1" in
	--restore-only) MODE=restore-only ;;
	--validate-only) MODE=validate-only ;;
	-h|--help) usage; exit 0 ;;
	*) usage >&2; exit 2 ;;
	esac
	;;
2)
	if [ "$1" = --validate-xml ]; then
		MODE=validate-xml
		XML_TARGET=$2
	else
		usage >&2
		exit 2
	fi
	;;
*)
	usage >&2
	exit 2
	;;
esac

die() {
	printf '%s\n' "integration-test: $*" >&2
	exit 1
}

require_xmllint() {
	XMLLINT=$(command -v xmllint || true)
	[ -n "$XMLLINT" ] || die 'requires xmllint for XML validation'
}

if [ "$MODE" = validate-xml ]; then
	require_xmllint
	[ -f "$XML_TARGET" ] || die "XML validation target is not a regular file: $XML_TARGET"
	[ ! -L "$XML_TARGET" ] || die "XML validation target must not be a symlink: $XML_TARGET"
	"$XMLLINT" --noout "$XML_TARGET" || die "XML validation failed: $XML_TARGET"
	printf 'XML-VALIDATION PASS: %s\n' "$XML_TARGET"
	exit 0
fi

SCRIPT_PATH=$0
while [ -L "$SCRIPT_PATH" ]; do
	LINK_DIR=$(CDPATH= cd -P -- "$(dirname -- "$SCRIPT_PATH")" && pwd -P)
	LINK_TARGET=$(readlink "$SCRIPT_PATH") || die "cannot resolve script symlink: $SCRIPT_PATH"
	case "$LINK_TARGET" in
	/*) SCRIPT_PATH=$LINK_TARGET ;;
	*) SCRIPT_PATH=$LINK_DIR/$LINK_TARGET ;;
	esac
done
[ -f "$SCRIPT_PATH" ] || die "script path is not a regular file: $SCRIPT_PATH"
SCRIPT_DIR=$(CDPATH= cd -P -- "$(dirname -- "$SCRIPT_PATH")" && pwd -P)
PROJECT_ROOT=$(CDPATH= cd -P -- "$SCRIPT_DIR/.." && pwd -P)
BACKUP_ROOT=$PROJECT_ROOT/run_test_back
WORK_ROOT=$PROJECT_ROOT/run_test
OUTPUT_ROOT=$PROJECT_ROOT/integration-output
WORK_INPUT=run_test
CONFIG_PATH=$PROJECT_ROOT/config.yml
BIN_PATH=$PROJECT_ROOT/bin/javsp
PRIMARY_NAME=ATID-687-FHD.mp4
LIVE_TIMEOUT_SECONDS=${JAVSP_INTEGRATION_TIMEOUT_SECONDS:-900}
CLEANUP_NEEDED=1
EXPECT_PRISTINE_WORK=1
MANIFEST_DIR=

physical_root() {
	role=$1
	path=$2
	[ -e "$path" ] || die "missing $role root: $path"
	[ ! -L "$path" ] || die "$role root must not be a symlink: $path"
	[ -d "$path" ] || die "$role root is not a directory: $path"
	(CDPATH= cd -P -- "$path" && pwd -P)
}

path_contains() {
	child=$1
	parent=$2
	case "$child" in
	"$parent"|"$parent"/*) return 0 ;;
	*) return 1 ;;
	esac
}

require_distinct_non_nested() {
	left_name=$1
	left_path=$2
	right_name=$3
	right_path=$4
	if path_contains "$left_path" "$right_path" || path_contains "$right_path" "$left_path"; then
		die "$left_name and $right_name roots must be distinct and non-nested: $left_path / $right_path"
	fi
}

file_size() {
	if size=$(stat -f '%z' "$1" 2>/dev/null); then
		printf '%s\n' "$size"
		return 0
	fi
	stat -c '%s' "$1"
}

sha256() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
		return 0
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
		return 0
	fi
	die 'requires shasum or sha256sum'
}

manifest_lines() {
	if [ -z "$1" ]; then
		printf '0\n'
	else
		printf '%s\n' "$1" | wc -l | tr -d '[:space:]'
	fi
}

entry_manifest() {
	root=$1
	invalid=$(find "$root" -mindepth 1 -maxdepth 1 ! -type f -print -quit)
	if [ -n "$invalid" ]; then
		printf '%s\n' "integration-test: fixture entry must be an ordinary regular file: $invalid" >&2
		return 1
	fi
	find "$root" -mindepth 1 -maxdepth 1 -type f -print | LC_ALL=C sort | \
		while IFS= read -r path; do
			relative=${path#"$root"/}
			printf '%s\tregular-file\t%s\t%s\n' "$relative" "$(file_size "$path")" "$(sha256 "$path")"
		done
}

ignored_file_manifest() {
	root=$1
	find "$root" -mindepth 1 -maxdepth 1 -type f ! -name "$PRIMARY_NAME" -print | LC_ALL=C sort | \
		while IFS= read -r path; do
			relative=${path#"$root"/}
			printf '%s\tregular-file\t%s\t%s\n' "$relative" "$(file_size "$path")" "$(sha256 "$path")"
		done
}

capture_manifest() {
	label=$1
	root=$2
	if ! manifest=$(entry_manifest "$root"); then
		return 1
	fi
	count=$(manifest_lines "$manifest")
	if [ "$count" != 6 ]; then
		printf '%s\n' "integration-test: $label manifest must contain six regular files, found $count" >&2
		return 1
	fi
	printf '%s' "$manifest"
}

persist_manifest() {
	name=$1
	manifest=$2
	[ -n "$MANIFEST_DIR" ] || return 0
	[ ! -L "$MANIFEST_DIR" ] || die "manifest directory must not be a symlink: $MANIFEST_DIR"
	printf '%s\n' "$manifest" >"$MANIFEST_DIR/$name.tsv"
}

assert_backup_integrity() {
	if ! current_manifest=$(capture_manifest backup "$BACKUP_DIR"); then
		die 'could not build backup manifest'
	fi
	[ "$current_manifest" = "$BACKUP_INITIAL_MANIFEST" ] || die 'backup manifest changed'
}

assert_working_manifest() {
	label=$1
	if ! current_manifest=$(capture_manifest working "$WORK_DIR"); then
		die "could not build working manifest after $label"
	fi
	[ "$current_manifest" = "$BACKUP_INITIAL_MANIFEST" ] || die "working manifest differs from backup after $label"
	persist_manifest "working-$label" "$current_manifest"
}

assert_primary_matches_backup() {
	[ -f "$WORK_PRIMARY" ] || die "working primary is missing: $WORK_PRIMARY"
	[ ! -L "$WORK_PRIMARY" ] || die "working primary must not be a symlink: $WORK_PRIMARY"
	working_size=$(file_size "$WORK_PRIMARY")
	working_hash=$(sha256 "$WORK_PRIMARY")
	[ "$working_size" = "$BACKUP_PRIMARY_SIZE" ] || die "working primary size differs from backup: $working_size"
	[ "$working_hash" = "$BACKUP_PRIMARY_HASH" ] || die "working primary SHA-256 differs from backup: $working_hash"
}

restore_fixture() {
	stage=$1
	manifest_key=$2
	printf '\n== Restore fixture: %s ==\n' "$stage"
	assert_backup_integrity
	rm -rf "$WORK_DIR"
	if cp -cR "$BACKUP_DIR" "$WORK_DIR" 2>/dev/null; then
		copy_method='cp -cR'
	else
		rm -rf "$WORK_DIR"
		cp -pR "$BACKUP_DIR" "$WORK_DIR"
		copy_method='cp -pR'
	fi
	assert_backup_integrity
	assert_working_manifest "$manifest_key"
	assert_primary_matches_backup
	printf 'RESTORE PASS: method=%s entries=6 working-size=%s working-sha256=%s\n' \
		"$copy_method" "$working_size" "$working_hash"
}

assert_ignored_files_unchanged() {
	backup_ignored=$(ignored_file_manifest "$BACKUP_DIR")
	working_ignored=$(ignored_file_manifest "$WORK_DIR")
	ignored_count=$(manifest_lines "$working_ignored")
	[ "$ignored_count" = 5 ] || die "working input must retain five ignored files, found $ignored_count"
	[ "$working_ignored" = "$backup_ignored" ] || die 'ignored working files differ from backup after organizer'
	record_assertion 'organizer retained all five ignored input files unchanged'
}

cleanup() {
	status=$1
	trap - EXIT HUP INT TERM
	set +e
	if [ "$CLEANUP_NEEDED" -eq 1 ]; then
		printf '\n== Recovery cleanup ==\n' >&2
		restore_fixture 'EXIT recovery cleanup' cleanup-final
		restore_status=$?
	else
		restore_status=0
	fi
	assert_backup_integrity
	backup_status=$?
	if [ "$EXPECT_PRISTINE_WORK" -eq 1 ]; then
		assert_working_manifest final
		working_status=$?
	else
		working_status=0
	fi
	if [ "$restore_status" -ne 0 ] || [ "$backup_status" -ne 0 ] || [ "$working_status" -ne 0 ]; then
		printf '%s\n' 'integration-test: recovery cleanup failed; inspect fixture paths immediately' >&2
		exit 1
	fi
	exit "$status"
}

case "$LIVE_TIMEOUT_SECONDS" in
''|*[!0-9]*) die 'JAVSP_INTEGRATION_TIMEOUT_SECONDS must be a positive integer' ;;
esac
[ "$LIVE_TIMEOUT_SECONDS" -gt 0 ] || die 'JAVSP_INTEGRATION_TIMEOUT_SECONDS must be a positive integer'

BACKUP_DIR=$(physical_root backup "$BACKUP_ROOT")
WORK_DIR=$(physical_root working "$WORK_ROOT")
require_distinct_non_nested backup "$BACKUP_DIR" working "$WORK_DIR"
OUTPUT_DIR=
if [ "$MODE" = full ]; then
	if [ -e "$OUTPUT_ROOT" ] || [ -L "$OUTPUT_ROOT" ]; then
		[ ! -L "$OUTPUT_ROOT" ] || die "output root must not be a symlink: $OUTPUT_ROOT"
		[ -d "$OUTPUT_ROOT" ] || die "output root is not a directory: $OUTPUT_ROOT"
	else
		mkdir -- "$OUTPUT_ROOT"
	fi
	OUTPUT_DIR=$(physical_root output "$OUTPUT_ROOT")
	require_distinct_non_nested backup "$BACKUP_DIR" output "$OUTPUT_DIR"
	require_distinct_non_nested working "$WORK_DIR" output "$OUTPUT_DIR"
fi
BACKUP_PRIMARY=$BACKUP_DIR/$PRIMARY_NAME
WORK_PRIMARY=$WORK_DIR/$PRIMARY_NAME
[ -f "$BACKUP_PRIMARY" ] || die "missing fixture backup file: $BACKUP_PRIMARY"
[ ! -L "$BACKUP_PRIMARY" ] || die "backup primary must not be a symlink: $BACKUP_PRIMARY"
require_xmllint

if ! BACKUP_INITIAL_MANIFEST=$(capture_manifest backup "$BACKUP_DIR"); then
	die 'could not build initial backup manifest'
fi
BACKUP_PRIMARY_SIZE=$(file_size "$BACKUP_PRIMARY")
BACKUP_PRIMARY_HASH=$(sha256 "$BACKUP_PRIMARY")

trap 'cleanup "$?"' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'PHYSICAL ROOTS: backup=%s work=%s' "$BACKUP_DIR" "$WORK_DIR"
if [ -n "$OUTPUT_DIR" ]; then
	printf ' output=%s' "$OUTPUT_DIR"
fi
printf '\n'
printf 'BACKUP INITIAL: size=%s sha256=%s entries=6\n' "$BACKUP_PRIMARY_SIZE" "$BACKUP_PRIMARY_HASH"

if [ "$MODE" = validate-only ]; then
	CLEANUP_NEEDED=0
	assert_backup_integrity
	assert_working_manifest validate-only
	printf 'BACKUP MANIFEST:\n%s\n' "$BACKUP_INITIAL_MANIFEST"
	printf 'VALIDATION-ONLY PASS\n'
	exit 0
fi

if [ "$MODE" = restore-only ]; then
	restore_fixture restore-only restore-only
	assert_backup_integrity
	assert_working_manifest restore-only-final
	printf 'BACKUP FINAL: size=%s sha256=%s entries=6\n' \
		"$(file_size "$BACKUP_PRIMARY")" "$(sha256 "$BACKUP_PRIMARY")"
	CLEANUP_NEEDED=0
	printf 'RESTORE-ONLY PASS\n'
	exit 0
fi

[ -f "$CONFIG_PATH" ] || die "missing local config: $CONFIG_PATH"
case "${JAVSP_TEST_PROXY-}" in
http://*|https://*|socks5://*|socks5h://*) ;;
*) die 'JAVSP_TEST_PROXY is required for live stages and must be an http://, https://, socks5://, or socks5h:// URL' ;;
esac

archive_previous_output() {
	ARCHIVE_ROOT=$OUTPUT_DIR/archive
	if [ -e "$ARCHIVE_ROOT" ] || [ -L "$ARCHIVE_ROOT" ]; then
		[ ! -L "$ARCHIVE_ROOT" ] || die "archive root must not be a symlink: $ARCHIVE_ROOT"
		[ -d "$ARCHIVE_ROOT" ] || die "archive root is not a directory: $ARCHIVE_ROOT"
		ARCHIVE_ROOT=$(physical_root archive "$ARCHIVE_ROOT")
	else
		mkdir "$ARCHIVE_ROOT"
		ARCHIVE_ROOT=$(physical_root archive "$ARCHIVE_ROOT")
	fi
	require_distinct_non_nested backup "$BACKUP_DIR" archive "$ARCHIVE_ROOT"
	attempt_dir=$ARCHIVE_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$$
	mkdir "$attempt_dir"
	attempt_dir=$(physical_root archive-attempt "$attempt_dir")
	require_distinct_non_nested backup "$BACKUP_DIR" archive-attempt "$attempt_dir"
	find "$OUTPUT_DIR" -mindepth 1 -maxdepth 1 ! -name archive -exec mv {} "$attempt_dir"/ \;
}

prepare_output_artifacts() {
	archive_previous_output
	MANIFEST_DIR=$OUTPUT_DIR/manifests
	[ ! -e "$MANIFEST_DIR" ] && [ ! -L "$MANIFEST_DIR" ] || die "manifest directory already exists or is a symlink: $MANIFEST_DIR"
	mkdir "$MANIFEST_DIR"
	MANIFEST_DIR=$(physical_root manifests "$MANIFEST_DIR")
	require_distinct_non_nested backup "$BACKUP_DIR" manifests "$MANIFEST_DIR"
	persist_manifest backup-initial "$BACKUP_INITIAL_MANIFEST"
}

record_assertion() {
	printf 'PASS: %s\n' "$1" | tee -a "$ASSERTIONS_LOG"
}

assert_nonempty_file() {
	[ -s "$1" ] || die "$2 is missing or empty: $1"
	record_assertion "$2 is nonempty"
}

assert_valid_xml() {
	"$XMLLINT" --noout "$1" || die "$2 is not valid XML: $1"
	record_assertion "$2 is valid XML"
}

assert_jpeg() {
	command -v file >/dev/null 2>&1 || die 'requires file to validate JPEG artifacts'
	file -b "$1" | grep -qi 'JPEG image data' || die "$2 is not recognized as JPEG: $1"
	record_assertion "$2 is recognized as JPEG"
}

assert_log_contains() {
	grep -F "$2" "$1" >/dev/null 2>&1 || die "log $1 is missing expected text: $2"
	record_assertion "log contains: $2"
}

assert_nonempty_element() {
	grep -Eq "<$2([[:space:]][^>]*)?>[[:space:]]*[^<[:space:]][^<]*</$2>" "$1" || die "$3 is missing a nonempty <$2> element"
	record_assertion "$3 has nonempty <$2>"
}

diagnose_element() {
	if grep -Eq "<$2([[:space:]][^>]*)?>[[:space:]]*[^<[:space:]][^<]*</$2>" "$1"; then
		printf 'DIAGNOSTIC: %s <%s>=present\n' "$3" "$2" | tee -a "$ASSERTIONS_LOG"
	else
		printf 'DIAGNOSTIC: %s <%s>=absent from live metadata\n' "$3" "$2" | tee -a "$ASSERTIONS_LOG"
	fi
}

assert_common_artifacts() {
	directory=$1
	label=$2
	nfo_file=$directory/movie.nfo
	fanart_file=$directory/fanart.jpg
	poster_file=$directory/poster.jpg
	assert_nonempty_file "$nfo_file" "$label NFO"
	assert_valid_xml "$nfo_file" "$label NFO"
	assert_nonempty_file "$fanart_file" "$label fanart"
	assert_jpeg "$fanart_file" "$label fanart"
	assert_nonempty_file "$poster_file" "$label poster"
	assert_jpeg "$poster_file" "$label poster"
	grep -Eq '<uniqueid[^>]*>ATID-687</uniqueid>' "$nfo_file" || die "$label NFO does not contain ATID-687 uniqueid"
	record_assertion "$label NFO contains ATID-687 uniqueid"
}

run_bounded() {
	timeout_seconds=$1
	log_file=$2
	shift 2
	if command -v timeout >/dev/null 2>&1; then
		timeout "$timeout_seconds" "$@" >"$log_file" 2>&1
		return
	fi
	if command -v gtimeout >/dev/null 2>&1; then
		gtimeout "$timeout_seconds" "$@" >"$log_file" 2>&1
		return
	fi
	if command -v perl >/dev/null 2>&1; then
		perl -e '$seconds = shift @ARGV; alarm $seconds; exec @ARGV or die "exec: $!\n";' \
			"$timeout_seconds" "$@" >"$log_file" 2>&1
		return
	fi
	die 'requires timeout, gtimeout, or perl to bound live commands'
}

run_stage() {
	stage=$1
	log_file=$2
	shift 2
	printf '\n== Stage: %s ==\n' "$stage"
	if run_bounded "$LIVE_TIMEOUT_SECONDS" "$log_file" "$@"; then
		record_assertion "$stage command completed within ${LIVE_TIMEOUT_SECONDS}s"
		return 0
	else
		status=$?
	fi
	printf '%s\n' "---- $stage log (last 160 lines) ----" >&2
	tail -n 160 "$log_file" >&2 || true
	die "$stage command failed with exit status $status; full log: $log_file"
}

run_javsp() {
	stage=$1
	log_file=$2
	translator=$3
	move_files=$4
	shift 4
	run_stage "$stage" "$log_file" "$BIN_PATH" \
		-config "$CONFIG_PATH" \
		-input "$WORK_INPUT" \
		-o "summarizer.move_files=$move_files" \
		-o "translator.engine=$translator" \
		-o 'summarizer.extra_fanarts.enabled=false' \
		-o 'other.check_update=false' \
		-o 'other.interactive=false' \
		-o 'network.timeout=PT90S' \
		-o "network.proxy_server=$JAVSP_TEST_PROXY" \
		"$@"
}

prepare_output_artifacts
ASSERTIONS_LOG=$OUTPUT_DIR/assertions.log
: >"$ASSERTIONS_LOG"

cd "$PROJECT_ROOT"
run_stage build "$OUTPUT_DIR/build.log" make build
[ -x "$BIN_PATH" ] || die "build did not produce executable: $BIN_PATH"
record_assertion 'build produced bin/javsp'

restore_fixture 'before smoke' before-smoke
run_javsp 'scrape smoke' "$OUTPUT_DIR/smoke.log" null false
assert_log_contains "$OUTPUT_DIR/smoke.log" 'JavSP-Go recognized 1 movie(s)'
assert_log_contains "$OUTPUT_DIR/smoke.log" 'JavSP-Go processed 1/1 movie(s)'
assert_common_artifacts "$WORK_DIR" smoke
restore_fixture 'after smoke' after-smoke

restore_fixture 'before google translation' before-google
run_javsp 'google translation' "$OUTPUT_DIR/google-translation.log" '{name: google}' false \
	-o 'translator.fields.title=true' \
	-o 'translator.fields.plot=true'
assert_log_contains "$OUTPUT_DIR/google-translation.log" 'JavSP-Go recognized 1 movie(s)'
assert_log_contains "$OUTPUT_DIR/google-translation.log" 'JavSP-Go processed 1/1 movie(s)'
assert_common_artifacts "$WORK_DIR" 'google translation'
TRANSLATION_NFO=$WORK_DIR/movie.nfo
assert_nonempty_element "$TRANSLATION_NFO" title 'google translation NFO'
assert_nonempty_element "$TRANSLATION_NFO" originaltitle 'google translation NFO'
diagnose_element "$TRANSLATION_NFO" plot 'google translation NFO'
diagnose_element "$TRANSLATION_NFO" premiered 'google translation NFO'
diagnose_element "$TRANSLATION_NFO" genre 'google translation NFO'
diagnose_element "$TRANSLATION_NFO" name 'google translation NFO actor'
restore_fixture 'after google translation' after-google

restore_fixture 'before organizer' before-organizer
run_javsp 'destructive organizer' "$OUTPUT_DIR/organizer.log" null true
assert_log_contains "$OUTPUT_DIR/organizer.log" 'JavSP-Go recognized 1 movie(s)'
assert_log_contains "$OUTPUT_DIR/organizer.log" 'JavSP-Go processed 1/1 movie(s)'
DEFAULT_FINISHED_ROOT=$WORK_DIR/#整理完成
[ -d "$DEFAULT_FINISHED_ROOT" ] || die "default organizer output root is missing: $DEFAULT_FINISHED_ROOT"
organized_count=$(find "$DEFAULT_FINISHED_ROOT" -type f -name ATID-687.mp4 -print | wc -l | tr -d '[:space:]')
[ "$organized_count" = 1 ] || die "default organizer output must contain one ATID-687.mp4, found $organized_count"
ORGANIZED_VIDEO=$(find "$DEFAULT_FINISHED_ROOT" -type f -name ATID-687.mp4 -print -quit)
case "$ORGANIZED_VIDEO" in
"$DEFAULT_FINISHED_ROOT"/*) ;;
*) die "organized video is outside the default #整理完成 root: $ORGANIZED_VIDEO" ;;
esac
ORGANIZED_MOVIE_DIR=$(dirname -- "$ORGANIZED_VIDEO")
record_assertion 'organizer used run_test/#整理完成 default output root'
assert_nonempty_file "$ORGANIZED_VIDEO" 'organized video'
[ ! -e "$WORK_PRIMARY" ] || die "organizer left source video in working input: $WORK_PRIMARY"
record_assertion 'organizer removed source video from working input'
assert_ignored_files_unchanged
organized_size=$(file_size "$ORGANIZED_VIDEO")
organized_hash=$(sha256 "$ORGANIZED_VIDEO")
[ "$organized_size" = "$BACKUP_PRIMARY_SIZE" ] || die "organized video size differs from backup: $organized_size"
[ "$organized_hash" = "$BACKUP_PRIMARY_HASH" ] || die "organized video SHA-256 differs from backup: $organized_hash"
record_assertion 'organized video matches backup size and SHA-256'
assert_common_artifacts "$ORGANIZED_MOVIE_DIR" organized
assert_backup_integrity
printf 'BACKUP FINAL: size=%s sha256=%s entries=6\n' \
	"$(file_size "$BACKUP_PRIMARY")" "$(sha256 "$BACKUP_PRIMARY")"
printf 'ORGANIZED OUTPUT: %s\n' "$ORGANIZED_MOVIE_DIR"
EXPECT_PRISTINE_WORK=0
CLEANUP_NEEDED=0
printf '\nINTEGRATION PASS: smoke, google translation, and destructive organizer stages completed; final output remains under run_test/#整理完成.\n'
