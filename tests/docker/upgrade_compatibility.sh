#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 1.0.0

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)

printf '[upgrade-compatibility] running direct v0.7.0 to v1.0.0 acceptance\n'
SOURCE_VERSION=0.7.0 \
SOURCE_REF=e46d34d \
SOURCE_IMAGE=nginx-uix:0.7.0-upgrade-seed \
  "${SCRIPT_DIR}/upgrade.sh" "$@"

printf '[upgrade-compatibility] running v0.6.0 long-chain to v1.0.0 acceptance\n'
SOURCE_VERSION=0.6.0 \
SOURCE_REF=97036da \
SOURCE_IMAGE=nginx-uix:0.6.0-upgrade-seed \
  "${SCRIPT_DIR}/upgrade.sh" "$@"

printf '\nDocker v1.0 upgrade compatibility matrix: PASS\n'
