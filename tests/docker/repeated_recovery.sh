#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 1.0.0

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPEAT_TOTAL_ROUNDS=10

printf '[repeated-recovery] running batch 1/2 (rounds 1-5 of %s)\n' "${REPEAT_TOTAL_ROUNDS}"
REPEAT_TOTAL_ROUNDS="${REPEAT_TOTAL_ROUNDS}" REPEAT_BATCH=1 WORKSPACE_PROFILE=repeat \
  "${SCRIPT_DIR}/workspace.sh" "$@"

printf '[repeated-recovery] running batch 2/2 (rounds 6-10 of %s)\n' "${REPEAT_TOTAL_ROUNDS}"
REPEAT_TOTAL_ROUNDS="${REPEAT_TOTAL_ROUNDS}" REPEAT_BATCH=2 WORKSPACE_PROFILE=repeat \
  "${SCRIPT_DIR}/workspace.sh" "$@"

printf '[repeated-recovery] PASS: %s publish/restore rounds completed across two isolated data roots\n' \
  "${REPEAT_TOTAL_ROUNDS}"
