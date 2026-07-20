#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
# shellcheck source=lib/image.sh
. "${SCRIPT_DIR}/lib/image.sh"

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-image-test.XXXXXX")
cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT HUP INT TERM

fail() {
    printf 'image_test: FAIL: %s\n' "$*" >&2
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

snapshot_of() {
    find "$1" -type f -print | LC_ALL=C sort | while IFS= read -r path; do
        printf '%s ' "${path#"$1"/}"
        cksum "${path}"
    done
}

FAKE_BIN="${TEST_ROOT}/bin"
FAKE_DRIVER_MODE="${TEST_ROOT}/driver-mode"
FAKE_DOCKER_CALLS="${TEST_ROOT}/docker-calls"
FAKE_BUILD_ARGS="${TEST_ROOT}/build-args"
FAKE_BUILD_MARKER="${TEST_ROOT}/build-marker"
mkdir -p "${FAKE_BIN}"
export FAKE_DRIVER_MODE FAKE_DOCKER_CALLS FAKE_BUILD_ARGS FAKE_BUILD_MARKER

cat >"${FAKE_BIN}/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"${FAKE_DOCKER_CALLS}"
if [ "$1" = buildx ] && [ "$2" = inspect ]; then
    mode=$(cat "${FAKE_DRIVER_MODE}")
    [ "${mode}" != inspect-fail ] || exit 17
    printf 'Name: fixture\nDriver: %s\n' "${mode}"
    exit 0
fi
if [ "$1" = buildx ] && [ "$2" = build ]; then
    : >"${FAKE_BUILD_ARGS}"
    for argument in "$@"; do
        printf '%s\n' "${argument}" >>"${FAKE_BUILD_ARGS}"
        case "${argument}" in
            type=local,dest=*,mode=max)
                cache_path=${argument#type=local,dest=}
                cache_path=${cache_path%,mode=max}
                mkdir -p "${cache_path}"
                printf '{"schemaVersion":2,"manifests":[]}\n' >"${cache_path}/index.json"
                printf 'published\n' >"${cache_path}/marker"
                ;;
        esac
    done
    : >"${FAKE_BUILD_MARKER}"
    printf '# fixture CACHED\n'
    exit 0
fi
exit 19
EOF
chmod 0755 "${FAKE_BIN}/docker"
PATH="${FAKE_BIN}:${PATH}"
export PATH

docker_build_metadata() {
    BUILD_VERSION=0.2.1
    SOURCE_FINGERPRINT=1111111111111111111111111111111111111111111111111111111111111111
    BUILD_IDENTITY=2222222222222222222222222222222222222222222222222222222222222222
    SOURCE_COMMIT=0123456789abcdef0123456789abcdef01234567
    SOURCE_DATE_EPOCH=1784246400
    BUILD_TIME=2026-07-17T00:00:00Z
    PLATFORM=${PLATFORM:-linux/arm64}
    export BUILD_VERSION SOURCE_FINGERPRINT BUILD_IDENTITY SOURCE_COMMIT SOURCE_DATE_EPOCH BUILD_TIME PLATFORM
}

assert_image_identity() {
    [ -f "${FAKE_BUILD_MARKER}" ] || return 1
    IMAGE_DIGEST=sha256:3333333333333333333333333333333333333333333333333333333333333333
    export IMAGE_DIGEST
}

printf 'docker\n' >"${FAKE_DRIVER_MODE}"
BUILDX_BUILDER=fixture-builder
export BUILDX_BUILDER
assert_equal "$(buildx_builder_driver)" docker 'explicit docker builder driver'
grep -Fx 'buildx inspect fixture-builder' "${FAKE_DOCKER_CALLS}" >/dev/null ||
    fail 'explicit BUILDX_BUILDER was not inspected'

printf 'docker-container\n' >"${FAKE_DRIVER_MODE}"
unset BUILDX_BUILDER
assert_equal "$(buildx_builder_driver)" docker-container 'current docker-container driver'

printf 'remote\n' >"${FAKE_DRIVER_MODE}"
if buildx_builder_driver >"${TEST_ROOT}/unknown.out" 2>"${TEST_ROOT}/unknown.err"; then
    fail 'unknown Buildx driver was accepted'
fi
grep -F 'unsupported Buildx driver' "${TEST_ROOT}/unknown.err" >/dev/null ||
    fail 'unknown driver diagnostic was unstable'

printf 'inspect-fail\n' >"${FAKE_DRIVER_MODE}"
if buildx_builder_driver >"${TEST_ROOT}/inspect.out" 2>"${TEST_ROOT}/inspect.err"; then
    fail 'failed Buildx inspection was accepted'
fi
grep -F 'could not inspect the Buildx builder' "${TEST_ROOT}/inspect.err" >/dev/null ||
    fail 'inspect failure diagnostic was unstable'

DOCKER_REPOSITORY="${TEST_ROOT}/docker-repository"
REPOSITORY_ROOT="${DOCKER_REPOSITORY}"
mkdir -p "${REPOSITORY_ROOT}/.tmp/buildx-cache"
printf 'do-not-read\n' >"${REPOSITORY_ROOT}/.tmp/buildx-cache/sentinel"
docker_cache_before=$(snapshot_of "${REPOSITORY_ROOT}/.tmp/buildx-cache")
BUILDX_CACHE_SEED_DIR="${TEST_ROOT}/missing-seed"
BUILDX_BUILDER=fixture-builder
export REPOSITORY_ROOT BUILDX_CACHE_SEED_DIR BUILDX_BUILDER
printf 'docker\n' >"${FAKE_DRIVER_MODE}"
: >"${FAKE_DOCKER_CALLS}"
rm -f "${FAKE_BUILD_ARGS}" "${FAKE_BUILD_MARKER}"
BUILDX_CACHE_LOCK_OWNED=1
docker_evidence=$(ensure_test_image fixture:image auto linux/arm64)
BUILDX_CACHE_LOCK_OWNED=0
printf '%s\n' "${docker_evidence}" | grep -F 'driver=docker' >/dev/null ||
    fail 'docker evidence omitted the driver'
printf '%s\n' "${docker_evidence}" | grep -F 'cache=daemon' >/dev/null ||
    fail 'docker evidence did not identify daemon cache'
if grep -E -- '--cache-(from|to)' "${FAKE_BUILD_ARGS}" >/dev/null; then
    fail 'docker driver received an external cache argument'
fi
assert_equal "$(snapshot_of "${REPOSITORY_ROOT}/.tmp/buildx-cache")" "${docker_cache_before}" \
    'docker driver touched the current local cache'
[ ! -e "${REPOSITORY_ROOT}/.tmp/buildx-cache.lock" ] ||
    fail 'docker driver acquired the local cache lock'
[ ! -e "${BUILDX_CACHE_SEED_DIR}" ] || fail 'docker driver touched the absent seed'

CONTAINER_REPOSITORY="${TEST_ROOT}/container-repository"
REPOSITORY_ROOT="${CONTAINER_REPOSITORY}"
mkdir -p "${REPOSITORY_ROOT}/.tmp"
make_cache "${REPOSITORY_ROOT}/.tmp/buildx-cache" current
BUILDX_CACHE_SEED_DIR="${TEST_ROOT}/seed"
make_cache "${BUILDX_CACHE_SEED_DIR}" seed
seed_before=$(snapshot_of "${BUILDX_CACHE_SEED_DIR}")
export REPOSITORY_ROOT BUILDX_CACHE_SEED_DIR
printf 'docker-container\n' >"${FAKE_DRIVER_MODE}"
: >"${FAKE_DOCKER_CALLS}"
rm -f "${FAKE_BUILD_ARGS}" "${FAKE_BUILD_MARKER}"
container_evidence=$(ensure_test_image fixture:image auto linux/arm64)
printf '%s\n' "${container_evidence}" | grep -F 'driver=docker-container' >/dev/null ||
    fail 'docker-container evidence omitted the driver'
printf '%s\n' "${container_evidence}" | grep -F 'cache=current' >/dev/null ||
    fail 'docker-container evidence lost the current-cache classification'
grep -F -- "--cache-from" "${FAKE_BUILD_ARGS}" >/dev/null ||
    fail 'docker-container build omitted cache-from'
grep -F "type=local,src=${REPOSITORY_ROOT}/.tmp/buildx-cache" "${FAKE_BUILD_ARGS}" >/dev/null ||
    fail 'docker-container build omitted the current local cache'
grep -F "type=local,src=${BUILDX_CACHE_SEED_DIR}" "${FAKE_BUILD_ARGS}" >/dev/null ||
    fail 'docker-container build omitted the read-only seed cache'
grep -F -- "--cache-to" "${FAKE_BUILD_ARGS}" >/dev/null ||
    fail 'docker-container build omitted transactional cache-to'
assert_equal "$(cat "${REPOSITORY_ROOT}/.tmp/buildx-cache/marker")" published \
    'docker-container build did not publish its staging cache'
assert_equal "$(snapshot_of "${BUILDX_CACHE_SEED_DIR}")" "${seed_before}" \
    'docker-container build changed the seed cache'
[ ! -e "${REPOSITORY_ROOT}/.tmp/buildx-cache.lock" ] ||
    fail 'docker-container build left the cache lock behind'

for failure_mode in remote inspect-fail; do
    FAILURE_REPOSITORY="${TEST_ROOT}/${failure_mode}-repository"
    REPOSITORY_ROOT="${FAILURE_REPOSITORY}"
    mkdir -p "${REPOSITORY_ROOT}/.tmp"
    export REPOSITORY_ROOT
    printf '%s\n' "${failure_mode}" >"${FAKE_DRIVER_MODE}"
    : >"${FAKE_DOCKER_CALLS}"
    rm -f "${FAKE_BUILD_ARGS}" "${FAKE_BUILD_MARKER}"
    if ensure_test_image fixture:image auto linux/arm64 \
        >"${TEST_ROOT}/${failure_mode}-ensure.out" \
        2>"${TEST_ROOT}/${failure_mode}-ensure.err"; then
        fail "${failure_mode} driver reached a successful build"
    fi
    [ ! -e "${FAKE_BUILD_MARKER}" ] || fail "${failure_mode} driver reached Buildx build"
    [ ! -e "${REPOSITORY_ROOT}/.tmp/buildx-cache.lock" ] ||
        fail "${failure_mode} driver acquired the cache lock"
done

printf '%s\n' 'image_test: PASS'
