#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 1.0.0

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)

WORKSPACE_PROFILE=stability "${SCRIPT_DIR}/workspace.sh" "$@"
