#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

verify_playwright_summary() {
    playwright_summary_log=$1
    playwright_expected_passed=${2:-82}
    playwright_expected_skipped=${3:-1}
    [ -f "${playwright_summary_log}" ] || return 1

    case "${playwright_expected_passed}:${playwright_expected_skipped}" in
        *[!0-9:]*) return 1 ;;
    esac

    playwright_passed_count=$(awk '$1 ~ /^[0-9]+$/ && $2 == "passed" { print $1 }' "${playwright_summary_log}")
    playwright_skipped_count=$(awk '$1 ~ /^[0-9]+$/ && $2 == "skipped" { print $1 }' "${playwright_summary_log}")
    [ "${playwright_passed_count}" = "${playwright_expected_passed}" ] &&
        [ "${playwright_skipped_count}" = "${playwright_expected_skipped}" ]
}
