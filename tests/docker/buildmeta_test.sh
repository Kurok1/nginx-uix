#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd "${SCRIPT_DIR}/../.." && pwd)
TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-buildmeta-test.XXXXXX")
cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT HUP INT TERM

fail() {
    printf 'buildmeta_test: FAIL: %s\n' "$*" >&2
    exit 1
}

assert_digest() {
    case "$1" in
        [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]* ) ;;
        *) fail "$2 is not lowercase hexadecimal" ;;
    esac
    [ "${#1}" -eq 64 ] || fail "$2 is not 64 characters"
    if printf '%s' "$1" | LC_ALL=C grep -Eq '[^0-9a-f]'; then
        fail "$2 contains a non-hexadecimal character"
    fi
}

export GOCACHE="${GOCACHE:-${REPOSITORY_ROOT}/.tmp/go-cache}"
mkdir -p "${GOCACHE}"

FIXTURE_SOURCE="${TEST_ROOT}/fixture-source"
FIXTURE="${TEST_ROOT}/copied-context"
mkdir -p "${FIXTURE_SOURCE}/internal" "${FIXTURE_SOURCE}/empty"
printf '.tmp\n.superpowers\nnode_modules\n' >"${FIXTURE_SOURCE}/.dockerignore"
printf 'package internal\n' >"${FIXTURE_SOURCE}/internal/app.go"
printf '0.2.1\n' >"${FIXTURE_SOURCE}/VERSION"
cp -R "${FIXTURE_SOURCE}" "${FIXTURE}"

cd "${REPOSITORY_ROOT}"
baseline=$(go run ./tests/docker/cmd/buildmeta source --root "${FIXTURE}" --ignore .dockerignore)
assert_digest "${baseline}" 'baseline source fingerprint'

printf 'package internal // changed\n' >"${FIXTURE}/internal/app.go"
included=$(go run ./tests/docker/cmd/buildmeta source --root "${FIXTURE}" --ignore .dockerignore)
assert_digest "${included}" 'changed source fingerprint'
[ "${included}" != "${baseline}" ] || fail 'included file change did not change source fingerprint'

mkdir -p "${FIXTURE}/.tmp"
printf 'excluded\n' >"${FIXTURE}/.tmp/excluded"
excluded=$(go run ./tests/docker/cmd/buildmeta source --root "${FIXTURE}" --ignore .dockerignore)
[ "${excluded}" = "${included}" ] || fail 'ignored .tmp file changed source fingerprint'

GO_DIGEST=1111111111111111111111111111111111111111111111111111111111111111
NODE_DIGEST=2222222222222222222222222222222222222222222222222222222222222222
NGINX_DIGEST=3333333333333333333333333333333333333333333333333333333333333333
identity_amd64=$(go run ./tests/docker/cmd/buildmeta identity \
    --source "${excluded}" \
    --platform linux/amd64 \
    --base "go=${GO_DIGEST}" \
    --base "node=${NODE_DIGEST}" \
    --base "nginx=${NGINX_DIGEST}" \
    --arg VERSION=0.2.1 \
    --arg COMMIT=0123456789abcdef \
    --arg BUILD_TIME=2026-07-17T00:00:00Z \
    --arg SOURCE_DATE_EPOCH=1784246400)
identity_arm64=$(go run ./tests/docker/cmd/buildmeta identity \
    --source "${excluded}" \
    --platform linux/arm64 \
    --base "go=${GO_DIGEST}" \
    --base "node=${NODE_DIGEST}" \
    --base "nginx=${NGINX_DIGEST}" \
    --arg VERSION=0.2.1 \
    --arg COMMIT=0123456789abcdef \
    --arg BUILD_TIME=2026-07-17T00:00:00Z \
    --arg SOURCE_DATE_EPOCH=1784246400)
assert_digest "${identity_amd64}" 'amd64 build identity'
assert_digest "${identity_arm64}" 'arm64 build identity'
[ "${identity_amd64}" != "${identity_arm64}" ] || fail 'platform did not change build identity'

if go run ./tests/docker/cmd/buildmeta source \
    --root "${TEST_ROOT}/missing-context" --ignore .dockerignore \
    >"${TEST_ROOT}/unexpected.out" 2>"${TEST_ROOT}/diagnostic.err"; then
    fail 'missing source context unexpectedly succeeded'
fi
if grep -F "${TEST_ROOT}" "${TEST_ROOT}/diagnostic.err" >/dev/null; then
    fail 'CLI diagnostic leaked an absolute fixture path'
fi

printf '%s\n' 'buildmeta_test: PASS'
