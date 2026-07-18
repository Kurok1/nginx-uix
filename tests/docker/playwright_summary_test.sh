#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
# shellcheck source=lib/playwright_summary.sh
. "${SCRIPT_DIR}/lib/playwright_summary.sh"

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-playwright-summary-test.XXXXXX")
cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT HUP INT TERM

fail() {
    printf 'playwright_summary_test: FAIL: %s\n' "$*" >&2
    exit 1
}

write_summary() {
    summary_path=$1
    passed_count=$2
    skipped_count=$3
    {
        printf 'Running %s tests using 5 workers\n' "$((passed_count + skipped_count))"
        printf '  %s skipped\n' "${skipped_count}"
        printf '  %s passed (8.9s)\n' "${passed_count}"
    } >"${summary_path}"
}

CURRENT_LOG="${TEST_ROOT}/current.log"
write_summary "${CURRENT_LOG}" 48 1
verify_playwright_summary "${CURRENT_LOG}" ||
    fail 'current 48 passed plus 1 skipped summary was rejected'

for fixture in old-count low-count high-count no-skip extra-skip; do
    case "${fixture}" in
        old-count) passed=42 skipped=1 ;;
        low-count) passed=47 skipped=1 ;;
        high-count) passed=49 skipped=1 ;;
        no-skip) passed=48 skipped=0 ;;
        extra-skip) passed=48 skipped=2 ;;
    esac
    fixture_log="${TEST_ROOT}/${fixture}.log"
    write_summary "${fixture_log}" "${passed}" "${skipped}"
    if verify_playwright_summary "${fixture_log}"; then
        fail "${fixture} summary was accepted"
    fi
done

DUPLICATE_LOG="${TEST_ROOT}/duplicate.log"
write_summary "${DUPLICATE_LOG}" 48 1
printf '  48 passed (retry report)\n' >>"${DUPLICATE_LOG}"
if verify_playwright_summary "${DUPLICATE_LOG}"; then
    fail 'duplicate passed summaries were accepted'
fi

printf '%s\n' 'playwright_summary_test: PASS'
