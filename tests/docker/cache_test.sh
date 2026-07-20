#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
# shellcheck source=lib/cache.sh
. "${SCRIPT_DIR}/lib/cache.sh"

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-cache-test.XXXXXX")
cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT HUP INT TERM

fail() {
    printf 'cache_test: FAIL: %s\n' "$*" >&2
    exit 1
}

assert_equal() {
    [ "$1" = "$2" ] || fail "$3: got '$1', want '$2'"
}

make_cache() {
    cache_dir=$1
    marker=$2
    mkdir -p "${cache_dir}"
    printf '{"schemaVersion":2,"manifests":[]}\n' >"${cache_dir}/index.json"
    printf '%s\n' "${marker}" >"${cache_dir}/marker"
}

inode_of() {
    if stat -f '%i' "$1" >/dev/null 2>&1; then
        stat -f '%i' "$1"
    else
        stat -c '%i' "$1"
    fi
}

snapshot_of() {
    find "$1" -type f -print | LC_ALL=C sort | while IFS= read -r path; do
        printf '%s ' "${path#"$1"/}"
        cksum "${path}"
    done
}

VALIDATION_ROOT="${TEST_ROOT}/validation"
make_cache "${VALIDATION_ROOT}/valid" valid
validate_buildx_cache "${VALIDATION_ROOT}/valid" || fail 'valid cache was rejected'

mkdir -p "${VALIDATION_ROOT}/missing-index"
if validate_buildx_cache "${VALIDATION_ROOT}/missing-index"; then
    fail 'cache without index.json was accepted'
fi
make_cache "${VALIDATION_ROOT}/bad-json" bad
printf 'not-json\n' >"${VALIDATION_ROOT}/bad-json/index.json"
if validate_buildx_cache "${VALIDATION_ROOT}/bad-json"; then
    fail 'cache with invalid index.json was accepted'
fi
ln -s "${VALIDATION_ROOT}/valid" "${VALIDATION_ROOT}/cache-link"
if validate_buildx_cache "${VALIDATION_ROOT}/cache-link"; then
    fail 'symlink cache directory was accepted'
fi
make_cache "${VALIDATION_ROOT}/linked-index" linked
rm "${VALIDATION_ROOT}/linked-index/index.json"
ln -s "${VALIDATION_ROOT}/valid/index.json" "${VALIDATION_ROOT}/linked-index/index.json"
if validate_buildx_cache "${VALIDATION_ROOT}/linked-index"; then
    fail 'symlink index.json was accepted'
fi

ARG_ROOT="${TEST_ROOT}/arguments"
make_cache "${ARG_ROOT}/current" current
make_cache "${ARG_ROOT}/seed" seed
seed_inode_before=$(inode_of "${ARG_ROOT}/seed/index.json")
seed_snapshot_before=$(snapshot_of "${ARG_ROOT}/seed")
append_cache_from_args "${ARG_ROOT}/current" "${ARG_ROOT}/seed"
assert_equal "${BUILDX_CACHE_FROM_CURRENT}" "type=local,src=${ARG_ROOT}/current" 'current cache-from'
assert_equal "${BUILDX_CACHE_FROM_SEED}" "type=local,src=${ARG_ROOT}/seed" 'seed cache-from'
assert_equal "${BUILDX_CACHE_KIND}" current 'cache source kind'
assert_equal "$(inode_of "${ARG_ROOT}/seed/index.json")" "${seed_inode_before}" 'seed index inode changed'
assert_equal "$(snapshot_of "${ARG_ROOT}/seed")" "${seed_snapshot_before}" 'seed content changed'

append_cache_from_args "${ARG_ROOT}/absent" "${ARG_ROOT}/seed"
assert_equal "${BUILDX_CACHE_FROM_CURRENT}" '' 'absent current cache-from'
assert_equal "${BUILDX_CACHE_FROM_SEED}" "type=local,src=${ARG_ROOT}/seed" 'seed-only cache-from'
assert_equal "${BUILDX_CACHE_KIND}" seed 'seed-only cache kind'

append_cache_from_args "${ARG_ROOT}/absent" ''
assert_equal "${BUILDX_CACHE_KIND}" none 'empty cache kind'

LOCK_PARENT="${TEST_ROOT}/lock-parent"
acquire_buildx_cache_lock "${LOCK_PARENT}" 2 || fail 'could not acquire free cache lock'
[ -f "${LOCK_PARENT}/buildx-cache.lock/pid" ] || fail 'owned lock has no pid marker'
release_buildx_cache_lock || fail 'could not release owned cache lock'
[ ! -e "${LOCK_PARENT}/buildx-cache.lock" ] || fail 'owned lock was not removed'

mkdir "${LOCK_PARENT}/buildx-cache.lock"
printf '%s\n' "$$" >"${LOCK_PARENT}/buildx-cache.lock/pid"
if acquire_buildx_cache_lock "${LOCK_PARENT}" 1; then
    fail 'foreign lock was acquired'
fi
[ -d "${LOCK_PARENT}/buildx-cache.lock" ] || fail 'foreign lock was removed'
rm -rf "${LOCK_PARENT}/buildx-cache.lock"

PUBLISH_PARENT="${TEST_ROOT}/publish"
mkdir -p "${PUBLISH_PARENT}"
CURRENT="${PUBLISH_PARENT}/buildx-cache"
STAGING="${PUBLISH_PARENT}/buildx-cache.writer.new"
BACKUP="${PUBLISH_PARENT}/buildx-cache.writer.old"
make_cache "${CURRENT}" old
mkdir "${STAGING}"
printf 'invalid\n' >"${STAGING}/index.json"
acquire_buildx_cache_lock "${PUBLISH_PARENT}" 2 || fail 'could not acquire publication lock'
if publish_buildx_cache "${STAGING}" "${CURRENT}" "${BACKUP}"; then
    fail 'invalid staging cache was published'
fi
assert_equal "$(cat "${CURRENT}/marker")" old 'invalid staging changed current cache'
release_buildx_cache_lock || fail 'could not release publication lock'
rm -rf "${STAGING}"

make_cache "${STAGING}" new
acquire_buildx_cache_lock "${PUBLISH_PARENT}" 2 || fail 'could not reacquire publication lock'
publish_buildx_cache "${STAGING}" "${CURRENT}" "${BACKUP}" || fail 'valid staging cache was not published'
assert_equal "$(cat "${CURRENT}/marker")" new 'published cache marker'
[ ! -e "${BACKUP}" ] || fail 'successful publication left a backup'
release_buildx_cache_lock || fail 'could not release successful publication lock'

make_cache "${STAGING}" newest
MV_BIN="${TEST_ROOT}/mv-bin"
MV_COUNT="${TEST_ROOT}/mv-count"
mkdir "${MV_BIN}"
printf '0\n' >"${MV_COUNT}"
cat >"${MV_BIN}/mv" <<'EOF'
#!/bin/sh
count=$(cat "${MV_COUNT}")
count=$((count + 1))
printf '%s\n' "${count}" >"${MV_COUNT}"
if [ "${count}" -eq 2 ]; then
    exit 1
fi
exec /bin/mv "$@"
EOF
chmod 0755 "${MV_BIN}/mv"
acquire_buildx_cache_lock "${PUBLISH_PARENT}" 2 || fail 'could not acquire rollback lock'
if PATH="${MV_BIN}:${PATH}" MV_COUNT="${MV_COUNT}" publish_buildx_cache "${STAGING}" "${CURRENT}" "${BACKUP}"; then
    fail 'publication with failed second rename succeeded'
fi
assert_equal "$(cat "${CURRENT}/marker")" new 'failed publication did not restore current cache'
[ ! -e "${BACKUP}" ] || fail 'failed publication left a backup after restoration'
release_buildx_cache_lock || fail 'could not release rollback lock'

CONCURRENT_PARENT="${TEST_ROOT}/concurrent"
(
    acquire_buildx_cache_lock "${CONCURRENT_PARENT}" 2
    printf 'ready\n' >"${TEST_ROOT}/writer-ready"
    sleep 3
    release_buildx_cache_lock
) &
first_writer=$!
while [ ! -f "${TEST_ROOT}/writer-ready" ]; do sleep 1; done
if acquire_buildx_cache_lock "${CONCURRENT_PARENT}" 1; then
    fail 'second writer acquired a live lock'
fi
[ -d "${CONCURRENT_PARENT}/buildx-cache.lock" ] || fail 'second writer corrupted the live lock'
wait "${first_writer}"
acquire_buildx_cache_lock "${CONCURRENT_PARENT}" 2 || fail 'second writer could not acquire released lock'
release_buildx_cache_lock || fail 'second writer could not release lock'

printf '%s\n' 'cache_test: PASS'
