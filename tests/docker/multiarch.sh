#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.1.0

set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd "${SCRIPT_DIR}/../.." && pwd)
cd "${REPOSITORY_ROOT}"

VERSION=$(tr -d '\n' < VERSION)
SOURCE_COMMIT=$(git rev-parse HEAD)
BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
RUN_RANDOM=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
RUN_ID="t17-$(date -u '+%Y%m%dT%H%M%SZ')-$$-${RUN_RANDOM}"

OCI_ARCHIVE=""
TEMP_DIR=""
OWN_CONTAINERS=""
OWN_BUILDERS=""
OWN_IMAGES=""
OWN_VOLUMES=""
CACHE_ROTATION_ACTIVE=0
CACHE_HAD_PREVIOUS=0
BUILDX_CACHE_RESULT='miss'

AMD64_IMAGE="nginx-uix:${VERSION}-multiarch-${RUN_ID}-amd64"
ARM64_IMAGE="nginx-uix:${VERSION}-multiarch-${RUN_ID}-arm64"
PLAYWRIGHT_IMAGE="nginx-uix-playwright:${VERSION}-${RUN_ID}"
RESOURCE_PREFIX="nginx-uix-${RUN_ID}"
BUILDER_NAME="nginx-uix-${RUN_ID}"
BUILDX_CACHE_PARENT="${REPOSITORY_ROOT}/.tmp"
BUILDX_CACHE_DIR="${BUILDX_CACHE_PARENT}/buildx-cache"
BUILDX_CACHE_STAGING_DIR="${BUILDX_CACHE_PARENT}/buildx-cache.${RUN_ID}.new"
BUILDX_CACHE_BACKUP_DIR="${BUILDX_CACHE_PARENT}/buildx-cache.${RUN_ID}.old"

NGINX_BASE='nginx:1.30.3-trixie@sha256:b6edb43d9e6e3df4914ffee84030c41f84a9a8c38d9af9b0d44ee4ee295a0a2b'
PLAYWRIGHT_BASE='mcr.microsoft.com/playwright:v1.61.0-noble@sha256:57b65fdc9ceabe0ef613124c7bbe2babcf9362c4d85e382fe3b03604e84b428a'
SKIP_EMULATED_AMD64_RUNTIME=${SKIP_EMULATED_AMD64_RUNTIME:-0}

log() {
  printf '[multiarch] %s\n' "$*"
}

fail() {
  printf '[multiarch] ERROR: %s\n' "$*" >&2
  exit 1
}

remove_owned_cache_path() {
  remove_cache_path=$1
  case "${remove_cache_path}" in
    "${BUILDX_CACHE_DIR}" | "${BUILDX_CACHE_STAGING_DIR}" | "${BUILDX_CACHE_BACKUP_DIR}") ;;
    *) return 1 ;;
  esac
  rm -rf "${remove_cache_path}"
}

cleanup_buildx_cache() {
  if [ "${CACHE_ROTATION_ACTIVE}" -eq 1 ] && [ "${CACHE_HAD_PREVIOUS}" -eq 1 ] &&
    [ -d "${BUILDX_CACHE_BACKUP_DIR}" ] && [ ! -L "${BUILDX_CACHE_BACKUP_DIR}" ]; then
    if [ -e "${BUILDX_CACHE_DIR}" ] || [ -L "${BUILDX_CACHE_DIR}" ]; then
      remove_owned_cache_path "${BUILDX_CACHE_DIR}" || true
    fi
    if mv "${BUILDX_CACHE_BACKUP_DIR}" "${BUILDX_CACHE_DIR}"; then
      CACHE_HAD_PREVIOUS=0
    else
      printf '[multiarch] WARNING: previous Buildx cache remains at %s\n' \
        "${BUILDX_CACHE_BACKUP_DIR}" >&2
    fi
  fi

  if [ -e "${BUILDX_CACHE_STAGING_DIR}" ] || [ -L "${BUILDX_CACHE_STAGING_DIR}" ]; then
    remove_owned_cache_path "${BUILDX_CACHE_STAGING_DIR}" || true
  fi
  if [ "${CACHE_ROTATION_ACTIVE}" -eq 0 ] &&
    { [ -e "${BUILDX_CACHE_BACKUP_DIR}" ] || [ -L "${BUILDX_CACHE_BACKUP_DIR}" ]; }; then
    remove_owned_cache_path "${BUILDX_CACHE_BACKUP_DIR}" || true
  fi
}

cleanup() {
  cleanup_rc=$?
  trap - EXIT HUP INT TERM

  for cleanup_builder in ${OWN_BUILDERS}; do
    docker buildx rm --force "${cleanup_builder}" >/dev/null 2>&1 || true
  done
  for cleanup_container in ${OWN_CONTAINERS}; do
    docker rm --force "${cleanup_container}" >/dev/null 2>&1 || true
  done
  for cleanup_volume in ${OWN_VOLUMES}; do
    docker volume rm --force "${cleanup_volume}" >/dev/null 2>&1 || true
  done
  for cleanup_image in ${OWN_IMAGES}; do
    docker image rm --force "${cleanup_image}" >/dev/null 2>&1 || true
  done
  if [ -n "${TEMP_DIR}" ] && [ -d "${TEMP_DIR}" ]; then
    rm -rf "${TEMP_DIR}"
  fi
  cleanup_buildx_cache

  exit "${cleanup_rc}"
}

on_signal() {
  exit 130
}

# Install cleanup before creating temporary files or Docker resources.
trap cleanup EXIT
trap on_signal HUP INT TERM

case "${SKIP_EMULATED_AMD64_RUNTIME}" in
  0 | 1) ;;
  *) fail 'SKIP_EMULATED_AMD64_RUNTIME must be 0 or 1' ;;
esac

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
}

require_executable() {
  [ -x "$1" ] || fail "required executable is unavailable: $1"
}

register_container() {
  register_name=$1
  if docker container inspect "${register_name}" >/dev/null 2>&1; then
    fail "refusing to reuse existing container: ${register_name}"
  fi
  OWN_CONTAINERS="${OWN_CONTAINERS} ${register_name}"
}

register_builder() {
  register_name=$1
  if docker buildx inspect "${register_name}" >/dev/null 2>&1; then
    fail "refusing to reuse existing Buildx builder: ${register_name}"
  fi
  OWN_BUILDERS="${OWN_BUILDERS} ${register_name}"
}

register_image() {
  register_name=$1
  if docker image inspect "${register_name}" >/dev/null 2>&1; then
    fail "refusing to reuse existing image: ${register_name}"
  fi
  OWN_IMAGES="${OWN_IMAGES} ${register_name}"
}

register_volume() {
  register_name=$1
  if docker volume inspect "${register_name}" >/dev/null 2>&1; then
    fail "refusing to reuse existing volume: ${register_name}"
  fi
  OWN_VOLUMES="${OWN_VOLUMES} ${register_name}"
}

retry() (
  retry_limit=$1
  shift
  retry_attempt=1
  while :; do
    if "$@"; then
      exit 0
    fi
    if [ "${retry_attempt}" -ge "${retry_limit}" ]; then
      exit 1
    fi
    log "attempt ${retry_attempt}/${retry_limit} failed; retrying the unchanged command"
    sleep $((retry_attempt * 2))
    retry_attempt=$((retry_attempt + 1))
  done
)

digest_file() (
  digest_path=$1
  if command -v sha256sum >/dev/null 2>&1; then
    LC_ALL=C sha256sum "${digest_path}" | awk '{print $1}'
  else
    LC_ALL=C shasum -a 256 "${digest_path}" | awk '{print $1}'
  fi
)

digest_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    LC_ALL=C sha256sum | awk '{print $1}'
  else
    LC_ALL=C shasum -a 256 | awk '{print $1}'
  fi
}

source_fingerprint() {
  for fingerprint_path in \
    .dockerignore \
    VERSION \
    go.mod \
    go.sum \
    web/package.json \
    web/package-lock.json \
    deploy/docker/Dockerfile \
    deploy/docker/Playwright.Dockerfile; do
    printf '%s %s\n' "${fingerprint_path}" "$(digest_file "${fingerprint_path}")"
  done | digest_stream
}

assert_release_sources_clean() {
  if [ -n "$(git status --porcelain -- \
    .dockerignore VERSION go.mod go.sum cmd internal web deploy/docker)" ]; then
    fail 'release inputs differ from the recorded commit'
  fi
}

prepare_buildx_cache() {
  if [ -L "${BUILDX_CACHE_PARENT}" ]; then
    fail "refusing symbolic-link Buildx cache parent: ${BUILDX_CACHE_PARENT}"
  fi
  mkdir -p "${BUILDX_CACHE_PARENT}"
  [ -d "${BUILDX_CACHE_PARENT}" ] ||
    fail "Buildx cache parent is not a directory: ${BUILDX_CACHE_PARENT}"

  if [ -L "${BUILDX_CACHE_DIR}" ]; then
    fail "refusing symbolic-link Buildx cache: ${BUILDX_CACHE_DIR}"
  fi
  if [ -e "${BUILDX_CACHE_DIR}" ]; then
    [ -d "${BUILDX_CACHE_DIR}" ] ||
      fail "Buildx cache is not a directory: ${BUILDX_CACHE_DIR}"
    [ -f "${BUILDX_CACHE_DIR}/index.json" ] && [ ! -L "${BUILDX_CACHE_DIR}/index.json" ] ||
      fail "Buildx cache has no regular index: ${BUILDX_CACHE_DIR}"
    BUILDX_CACHE_RESULT='imported'
    log "importing reusable Buildx cache from ${BUILDX_CACHE_DIR}"
  else
    log "reusable Buildx cache miss at ${BUILDX_CACHE_DIR}"
  fi

  for prepare_cache_path in "${BUILDX_CACHE_STAGING_DIR}" "${BUILDX_CACHE_BACKUP_DIR}"; do
    if [ -e "${prepare_cache_path}" ] || [ -L "${prepare_cache_path}" ]; then
      fail "refusing existing per-run Buildx cache path: ${prepare_cache_path}"
    fi
  done
}

reset_buildx_cache_staging() {
  if [ -L "${BUILDX_CACHE_STAGING_DIR}" ]; then
    fail "refusing symbolic-link Buildx cache staging path: ${BUILDX_CACHE_STAGING_DIR}"
  fi
  if [ -e "${BUILDX_CACHE_STAGING_DIR}" ]; then
    remove_owned_cache_path "${BUILDX_CACHE_STAGING_DIR}" ||
      fail "refusing to clean unowned Buildx cache path: ${BUILDX_CACHE_STAGING_DIR}"
  fi
}

publish_buildx_cache() {
  [ -d "${BUILDX_CACHE_STAGING_DIR}" ] && [ ! -L "${BUILDX_CACHE_STAGING_DIR}" ] ||
    fail "Buildx cache export is not a regular directory: ${BUILDX_CACHE_STAGING_DIR}"
  [ -f "${BUILDX_CACHE_STAGING_DIR}/index.json" ] &&
    [ ! -L "${BUILDX_CACHE_STAGING_DIR}/index.json" ] ||
    fail "Buildx cache export has no regular index: ${BUILDX_CACHE_STAGING_DIR}"
  if [ -e "${BUILDX_CACHE_BACKUP_DIR}" ] || [ -L "${BUILDX_CACHE_BACKUP_DIR}" ]; then
    fail "Buildx cache backup path already exists: ${BUILDX_CACHE_BACKUP_DIR}"
  fi

  CACHE_ROTATION_ACTIVE=1
  CACHE_HAD_PREVIOUS=0
  if [ -e "${BUILDX_CACHE_DIR}" ]; then
    [ -d "${BUILDX_CACHE_DIR}" ] && [ ! -L "${BUILDX_CACHE_DIR}" ] ||
      fail "Buildx cache changed before publication: ${BUILDX_CACHE_DIR}"
    mv "${BUILDX_CACHE_DIR}" "${BUILDX_CACHE_BACKUP_DIR}"
    CACHE_HAD_PREVIOUS=1
  fi

  if ! mv "${BUILDX_CACHE_STAGING_DIR}" "${BUILDX_CACHE_DIR}"; then
    if [ "${CACHE_HAD_PREVIOUS}" -eq 1 ]; then
      mv "${BUILDX_CACHE_BACKUP_DIR}" "${BUILDX_CACHE_DIR}" ||
        fail "cannot restore previous Buildx cache: ${BUILDX_CACHE_BACKUP_DIR}"
      CACHE_HAD_PREVIOUS=0
    fi
    CACHE_ROTATION_ACTIVE=0
    fail 'cannot publish the new Buildx cache'
  fi

  CACHE_ROTATION_ACTIVE=0
  if [ "${CACHE_HAD_PREVIOUS}" -eq 1 ]; then
    remove_owned_cache_path "${BUILDX_CACHE_BACKUP_DIR}" ||
      fail "cannot remove previous Buildx cache backup: ${BUILDX_CACHE_BACKUP_DIR}"
    CACHE_HAD_PREVIOUS=0
  fi
  log "published reusable Buildx cache at ${BUILDX_CACHE_DIR}"
}

build_oci_index() {
  rm -f "${OCI_ARCHIVE}"
  reset_buildx_cache_staging
  set -- docker buildx build \
    --builder "${BUILDER_NAME}" \
    --platform linux/amd64,linux/arm64 \
    --provenance=false \
    --output "type=oci,dest=${OCI_ARCHIVE}" \
    --cache-to "type=local,dest=${BUILDX_CACHE_STAGING_DIR},mode=max" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "COMMIT=${SOURCE_COMMIT}" \
    --build-arg "BUILD_TIME=${BUILD_TIME}"
  if [ -f "${BUILDX_CACHE_DIR}/index.json" ] && [ ! -L "${BUILDX_CACHE_DIR}/index.json" ]; then
    set -- "$@" --cache-from "type=local,src=${BUILDX_CACHE_DIR}"
  fi
  if [ -n "${BUILD_STEP_HTTP_PROXY:-}" ]; then
    set -- "$@" --build-arg "http_proxy=${BUILD_STEP_HTTP_PROXY}"
  fi
  if [ -n "${BUILD_STEP_HTTPS_PROXY:-}" ]; then
    set -- "$@" --build-arg "https_proxy=${BUILD_STEP_HTTPS_PROXY}"
  fi
  if [ -n "${BUILD_STEP_NO_PROXY:-}" ]; then
    set -- "$@" --build-arg "no_proxy=${BUILD_STEP_NO_PROXY}"
  fi
  set -- "$@" --file deploy/docker/Dockerfile .
  "$@"
}

create_buildx_builder() {
  set -- docker buildx create --name "${BUILDER_NAME}" --driver docker-container
  if [ -n "${BUILDX_HTTP_PROXY:-}" ]; then
    set -- "$@" --driver-opt "env.http_proxy=${BUILDX_HTTP_PROXY}"
  fi
  if [ -n "${BUILDX_HTTPS_PROXY:-}" ]; then
    set -- "$@" --driver-opt "env.https_proxy=${BUILDX_HTTPS_PROXY}"
  fi
  if [ -n "${BUILDX_NO_PROXY:-}" ]; then
    set -- "$@" --driver-opt "env.no_proxy=${BUILDX_NO_PROXY}"
  fi
  "$@" >/dev/null
}

oci_blob_path() (
  oci_digest=$1
  case "${oci_digest}" in
    sha256:[0-9a-f][0-9a-f]*) ;;
    *) fail "unexpected OCI digest: ${oci_digest}" ;;
  esac
  oci_hex=${oci_digest#sha256:}
  [ "${#oci_hex}" -eq 64 ] || fail "unexpected OCI digest length: ${oci_digest}"
  printf '%s/blobs/sha256/%s\n' "${OCI_ROOT}" "${oci_hex}"
)

verify_oci_blob() (
  verify_digest=$1
  verify_path=$(oci_blob_path "${verify_digest}")
  [ -f "${verify_path}" ] || fail "OCI descriptor blob is missing: ${verify_digest}"
  verify_actual=$(digest_file "${verify_path}")
  [ "sha256:${verify_actual}" = "${verify_digest}" ] || fail "OCI blob digest mismatch: ${verify_digest}"
)

inspect_oci_architecture() (
  inspect_arch=$1
  inspect_count=$(jq --arg arch "${inspect_arch}" \
    '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch)] | length' \
    "${OCI_PLATFORM_INDEX}")
  [ "${inspect_count}" -eq 1 ] || fail "OCI index contains ${inspect_count} linux/${inspect_arch} manifests"

  inspect_manifest_digest=$(jq -er --arg arch "${inspect_arch}" \
    '.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest' \
    "${OCI_PLATFORM_INDEX}")
  verify_oci_blob "${inspect_manifest_digest}"
  inspect_manifest_path=$(oci_blob_path "${inspect_manifest_digest}")
  inspect_config_digest=$(jq -er '.config.digest' "${inspect_manifest_path}")
  verify_oci_blob "${inspect_config_digest}"
  inspect_config_path=$(oci_blob_path "${inspect_config_digest}")
  jq -e --arg arch "${inspect_arch}" \
    '.os == "linux" and .architecture == $arch' "${inspect_config_path}" >/dev/null ||
    fail "OCI config does not identify linux/${inspect_arch}"

  printf '%s\n' "${inspect_manifest_digest}"
)

build_loaded_image() {
  loaded_arch=$1
  loaded_image=$2
  retry 3 build_loaded_image_once

  loaded_os=$(docker image inspect --format '{{.Os}}' "${loaded_image}")
  loaded_actual_arch=$(docker image inspect --format '{{.Architecture}}' "${loaded_image}")
  [ "${loaded_os}/${loaded_actual_arch}" = "linux/${loaded_arch}" ] ||
    fail "loaded image platform is ${loaded_os}/${loaded_actual_arch}, expected linux/${loaded_arch}"
}

build_loaded_image_once() {
  set -- docker buildx build \
    --builder "${BUILDER_NAME}" \
    --platform "linux/${loaded_arch}" \
    --provenance=false \
    --load \
    --tag "${loaded_image}" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "COMMIT=${SOURCE_COMMIT}" \
    --build-arg "BUILD_TIME=${BUILD_TIME}"
  if [ -n "${BUILD_STEP_HTTP_PROXY:-}" ]; then
    set -- "$@" --build-arg "http_proxy=${BUILD_STEP_HTTP_PROXY}"
  fi
  if [ -n "${BUILD_STEP_HTTPS_PROXY:-}" ]; then
    set -- "$@" --build-arg "https_proxy=${BUILD_STEP_HTTPS_PROXY}"
  fi
  if [ -n "${BUILD_STEP_NO_PROXY:-}" ]; then
    set -- "$@" --build-arg "no_proxy=${BUILD_STEP_NO_PROXY}"
  fi
  set -- "$@" --file deploy/docker/Dockerfile .
  "$@"
}

normalize_architecture() (
  case "$1" in
    amd64 | x86_64) printf 'amd64\n' ;;
    arm64 | aarch64) printf 'arm64\n' ;;
    *) printf '%s\n' "$1" ;;
  esac
)

platform_runnable() {
  runnable_arch=$1
  runnable_image=$2
  runnable_container="${RESOURCE_PREFIX}-probe-${runnable_arch}"
  runnable_log="${TEMP_DIR}/probe-${runnable_arch}.log"
  register_container "${runnable_container}"
  if ! docker create \
    --name "${runnable_container}" \
    --platform "linux/${runnable_arch}" \
    --entrypoint /bin/true \
    "${runnable_image}" >"${runnable_log}" 2>&1; then
    if [ "${runnable_arch}" = "${HOST_ARCH}" ]; then
      sed -n '1,80p' "${runnable_log}" >&2
      fail "native linux/${runnable_arch} image cannot be created"
    fi
    log "linux/${runnable_arch} cannot be created on this runner; runtime checks are skipped for that architecture"
    return 1
  fi
  if docker start --attach "${runnable_container}" >"${runnable_log}" 2>&1; then
    return 0
  fi
  if [ "${runnable_arch}" = "${HOST_ARCH}" ]; then
    sed -n '1,80p' "${runnable_log}" >&2
    fail "native linux/${runnable_arch} image cannot execute"
  fi
  log "linux/${runnable_arch} cannot execute on this runner; runtime checks are skipped for that architecture"
  return 1
}

wait_for_readiness() {
  readiness_container=$1
  readiness_origin=$2
  readiness_deadline=$(($(date +%s) + 120))
  while [ "$(date +%s)" -lt "${readiness_deadline}" ]; do
    readiness_running=$(docker container inspect --format '{{.State.Running}}' "${readiness_container}")
    if [ "${readiness_running}" != 'true' ]; then
      docker logs --tail 100 "${readiness_container}" >&2 || true
      fail "container stopped before readiness: ${readiness_container}"
    fi
    readiness_code=$(curl --silent --max-time 2 --output /dev/null \
      --write-out '%{http_code}' "${readiness_origin}/health/ready" || true)
    if [ "${readiness_code}" = '200' ]; then
      return 0
    fi
    sleep 1
  done
  docker logs --tail 100 "${readiness_container}" >&2 || true
  fail "container did not become ready within 120 seconds: ${readiness_container}"
}

run_minimal_boot() {
  boot_arch=$1
  boot_image=$2
  boot_container="${RESOURCE_PREFIX}-boot-${boot_arch}"
  boot_config_volume="${RESOURCE_PREFIX}-config-${boot_arch}"
  boot_data_volume="${RESOURCE_PREFIX}-data-${boot_arch}"
  boot_secret="${TEMP_DIR}/admin-password-${boot_arch}"
  boot_login_payload="${TEMP_DIR}/login-${boot_arch}.request.json"
  boot_login_response="${TEMP_DIR}/login-${boot_arch}.response.json"
  boot_status_response="${TEMP_DIR}/status-${boot_arch}.response.json"
  boot_cookie_jar="${TEMP_DIR}/cookie-${boot_arch}.txt"
  boot_username="admin-${boot_arch}-${RUN_RANDOM}"
  boot_password="multiarch-${boot_arch}-${RUN_ID}"

  printf '%s\n' "${boot_password}" >"${boot_secret}"
  chmod 0600 "${boot_secret}"
  printf '{"username":"%s","password":"%s"}\n' \
    "${boot_username}" "${boot_password}" >"${boot_login_payload}"
  chmod 0600 "${boot_login_payload}"

  register_volume "${boot_config_volume}"
  docker volume create "${boot_config_volume}" >/dev/null
  register_volume "${boot_data_volume}"
  docker volume create "${boot_data_volume}" >/dev/null
  register_container "${boot_container}"

  docker run --detach \
    --name "${boot_container}" \
    --platform "linux/${boot_arch}" \
    --publish '127.0.0.1::9000' \
    --env "NGINX_UIX_ADMIN_USERNAME=${boot_username}" \
    --env 'NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin' \
    --mount "type=bind,src=${boot_secret},dst=/run/secrets/nginx-uix-admin,readonly" \
    --mount "type=volume,src=${boot_config_volume},dst=/etc/nginx" \
    --mount "type=volume,src=${boot_data_volume},dst=/var/lib/nginx-uix" \
    "${boot_image}" >/dev/null

  boot_port=$(docker container inspect --format \
    '{{(index (index .NetworkSettings.Ports "9000/tcp") 0).HostPort}}' "${boot_container}")
  case "${boot_port}" in
    '' | *[!0-9]*) fail "container has no dynamic UI port: ${boot_container}" ;;
  esac
  boot_origin="http://127.0.0.1:${boot_port}"
  wait_for_readiness "${boot_container}" "${boot_origin}"

  if ! boot_login_code=$(curl --silent --show-error --max-time 10 \
    --output "${boot_login_response}" \
    --write-out '%{http_code}' \
    --cookie-jar "${boot_cookie_jar}" \
    --header "Origin: ${boot_origin}" \
    --header 'Content-Type: application/json' \
    --data-binary "@${boot_login_payload}" \
    "${boot_origin}/api/v1/auth/session"); then
    fail "login request failed for linux/${boot_arch}"
  fi
  [ "${boot_login_code}" = '200' ] || fail "login returned HTTP ${boot_login_code} for linux/${boot_arch}"
  jq -e --arg username "${boot_username}" \
    '.user.username == $username and (.csrf_token | type == "string" and length > 0)' \
    "${boot_login_response}" >/dev/null || fail "login response is incomplete for linux/${boot_arch}"

  if ! boot_status_code=$(curl --silent --show-error --max-time 10 \
    --output "${boot_status_response}" \
    --write-out '%{http_code}' \
    --cookie "${boot_cookie_jar}" \
    "${boot_origin}/api/v1/system/status"); then
    fail "status request failed for linux/${boot_arch}"
  fi
  [ "${boot_status_code}" = '200' ] || fail "status returned HTTP ${boot_status_code} for linux/${boot_arch}"
  jq -e \
    '.components.ui == "healthy" and .components.agent == "healthy" and
     .components.nginx == "running" and (.master.pid > 0) and (.workers | length > 0)' \
    "${boot_status_response}" >/dev/null || fail "status is not healthy for linux/${boot_arch}"

  boot_stop_started=$(date +%s)
  docker stop --time 15 "${boot_container}" >/dev/null
  boot_stop_finished=$(date +%s)
  boot_stop_elapsed=$((boot_stop_finished - boot_stop_started))
  [ "${boot_stop_elapsed}" -le 20 ] || fail "graceful stop exceeded 20 seconds for linux/${boot_arch}"
  [ "$(docker container inspect --format '{{.State.Running}}' "${boot_container}")" = 'false' ] ||
    fail "container remains running after stop for linux/${boot_arch}"
  [ "$(docker container inspect --format '{{.State.ExitCode}}' "${boot_container}")" -eq 0 ] ||
    fail "container exited nonzero after graceful stop for linux/${boot_arch}"

  log "linux/${boot_arch} init, login, status, and graceful stop passed"
}

run_smoke_suite() {
  suite_image=$1
  suite_arch=$2
  suite_profile=$3
  log "running ${suite_profile} smoke suite for linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 SMOKE_PROFILE="${suite_profile}" \
    "${SCRIPT_DIR}/smoke.sh"
}

run_fault_suite() {
  suite_image=$1
  suite_arch=$2
  log "running fault suite for linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/faults.sh"
}

write_image_layers() {
  layer_image=$1
  layer_output=$2
  docker image inspect --format '{{range .RootFS.Layers}}{{println .}}{{end}}' \
    "${layer_image}" | sed '/^[[:space:]]*$/d' >"${layer_output}"
  [ -s "${layer_output}" ] || fail "image has no RootFS layers: ${layer_image}"
  if grep -Ev '^sha256:[0-9a-f]{64}$' "${layer_output}" >/dev/null; then
    fail "image has a malformed RootFS layer digest: ${layer_image}"
  fi
}

assert_layer_prefix() {
  prefix_file=$1
  complete_file=$2
  prefix_line=1
  while IFS= read -r prefix_layer; do
    complete_layer=$(sed -n "${prefix_line}p" "${complete_file}")
    [ "${prefix_layer}" = "${complete_layer}" ] || fail 'release image does not preserve the pinned Nginx base layers'
    prefix_line=$((prefix_line + 1))
  done <"${prefix_file}"
}

assert_browser_isolation() {
  isolation_release_image=$1
  isolation_arch=$2
  isolation_export_container="${RESOURCE_PREFIX}-release-export"
  isolation_export_tar="${TEMP_DIR}/release-rootfs.tar"
  isolation_file_list="${TEMP_DIR}/release-rootfs.list"
  isolation_base_layers="${TEMP_DIR}/nginx-base.layers"
  isolation_release_layers="${TEMP_DIR}/release.layers"
  isolation_release_added_layers="${TEMP_DIR}/release-added.layers"
  isolation_browser_layers="${TEMP_DIR}/playwright-base.layers"

  grep -F "FROM ${PLAYWRIGHT_BASE}" deploy/docker/Playwright.Dockerfile >/dev/null ||
    fail 'Playwright Dockerfile does not use the required pinned base'
  if grep -Eiq 'playwright|chromium|chrome|firefox|webkit|node_modules' deploy/docker/Dockerfile; then
    fail 'release Dockerfile references browser-only tooling'
  fi

  retry 3 docker pull --platform "linux/${isolation_arch}" "${NGINX_BASE}" >/dev/null
  retry 3 docker pull --platform "linux/${isolation_arch}" "${PLAYWRIGHT_BASE}" >/dev/null
  write_image_layers "${NGINX_BASE}" "${isolation_base_layers}"
  write_image_layers "${isolation_release_image}" "${isolation_release_layers}"
  write_image_layers "${PLAYWRIGHT_BASE}" "${isolation_browser_layers}"
  assert_layer_prefix "${isolation_base_layers}" "${isolation_release_layers}"

  isolation_base_count=$(wc -l <"${isolation_base_layers}" | tr -d ' ')
  awk -v base_count="${isolation_base_count}" 'NR > base_count { print }' \
    "${isolation_release_layers}" >"${isolation_release_added_layers}"
  [ -s "${isolation_release_added_layers}" ] || fail 'release image has no layers above the Nginx base'
  while IFS= read -r isolation_added_layer; do
    if grep -Fx "${isolation_added_layer}" "${isolation_browser_layers}" >/dev/null; then
      fail "release-added layer is shared with the Playwright base: ${isolation_added_layer}"
    fi
  done <"${isolation_release_added_layers}"

  register_container "${isolation_export_container}"
  docker create --name "${isolation_export_container}" \
    --platform "linux/${isolation_arch}" "${isolation_release_image}" >/dev/null
  docker export --output "${isolation_export_tar}" "${isolation_export_container}"
  LC_ALL=C tar -tf "${isolation_export_tar}" >"${isolation_file_list}"
  if grep -Eiq '(^|/)(playwright|ms-playwright|chromium|chrome|firefox|webkit|node|npm|npx)(/|$)|(^|/)node_modules(/|$)|(^|/)workspace(/|$)' \
    "${isolation_file_list}"; then
    grep -Ei '(^|/)(playwright|ms-playwright|chromium|chrome|firefox|webkit|node|npm|npx)(/|$)|(^|/)node_modules(/|$)|(^|/)workspace(/|$)' \
      "${isolation_file_list}" | sed -n '1,20p' >&2
    fail 'release filesystem contains browser, Node, or source artifacts'
  fi

  log 'browser isolation passed using release-added layers and the final filesystem (upstream history was not scanned)'
}

run_playwright_acceptance() {
  playwright_log="${TEMP_DIR}/playwright.log"
  register_image "${PLAYWRIGHT_IMAGE}"
  retry 3 build_playwright_image

  playwright_container="${RESOURCE_PREFIX}-playwright"
  register_container "${playwright_container}"
  if ! docker run --rm --name "${playwright_container}" \
    "${PLAYWRIGHT_IMAGE}" >"${playwright_log}" 2>&1; then
    sed -n '1,240p' "${playwright_log}" >&2
    fail 'Playwright acceptance failed'
  fi
  sed -n '1,240p' "${playwright_log}"
  grep -Eq '(^|[^0-9])29 passed([[:space:](]|$)' "${playwright_log}" ||
    fail 'Playwright output does not prove all 29 tests passed'
  log 'Playwright acceptance passed: 29/29 tests'
}

build_playwright_image() {
  set -- docker build \
    --file deploy/docker/Playwright.Dockerfile \
    --tag "${PLAYWRIGHT_IMAGE}"
  if [ -n "${BUILD_STEP_HTTP_PROXY:-}" ]; then
    set -- "$@" --build-arg "http_proxy=${BUILD_STEP_HTTP_PROXY}"
  fi
  if [ -n "${BUILD_STEP_HTTPS_PROXY:-}" ]; then
    set -- "$@" --build-arg "https_proxy=${BUILD_STEP_HTTPS_PROXY}"
  fi
  if [ -n "${BUILD_STEP_NO_PROXY:-}" ]; then
    set -- "$@" --build-arg "no_proxy=${BUILD_STEP_NO_PROXY}"
  fi
  set -- "$@" .
  "$@"
}

for required_command in docker git jq curl tar awk sed grep od tr wc chmod date mktemp; do
  require_command "${required_command}"
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  fail 'sha256sum or shasum is required'
fi
require_executable "${SCRIPT_DIR}/smoke.sh"
require_executable "${SCRIPT_DIR}/faults.sh"
[ "${VERSION}" = '0.1.0' ] || fail "unexpected release version: ${VERSION}"
docker info >/dev/null
docker buildx version >/dev/null
assert_release_sources_clean

INITIAL_SOURCE_FINGERPRINT=$(source_fingerprint)
HOST_ARCH=$(normalize_architecture "$(docker info --format '{{.Architecture}}')")
case "${HOST_ARCH}" in
  amd64 | arm64) ;;
  *) fail "unsupported Docker host architecture: ${HOST_ARCH}" ;;
esac
if [ "${SKIP_EMULATED_AMD64_RUNTIME}" -eq 1 ] && [ "${HOST_ARCH}" != 'arm64' ]; then
  fail 'SKIP_EMULATED_AMD64_RUNTIME=1 is only valid on an arm64 Docker host'
fi

TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-multiarch.${RUN_ID}.XXXXXX")
chmod 0700 "${TEMP_DIR}"
OCI_ARCHIVE="${TEMP_DIR}/nginx-uix-${VERSION}.oci.tar"
OCI_ROOT="${TEMP_DIR}/oci"
mkdir "${OCI_ROOT}"
prepare_buildx_cache

register_builder "${BUILDER_NAME}"
create_buildx_builder
retry 3 docker buildx inspect "${BUILDER_NAME}" --bootstrap >/dev/null

log "building OCI index from commit ${SOURCE_COMMIT}"
retry 3 build_oci_index
publish_buildx_cache
LC_ALL=C tar -xf "${OCI_ARCHIVE}" -C "${OCI_ROOT}"
[ -f "${OCI_ROOT}/oci-layout" ] || fail 'OCI layout marker is missing'
[ -f "${OCI_ROOT}/index.json" ] || fail 'OCI index is missing'
OCI_LAYOUT_INDEX_DIGEST="sha256:$(digest_file "${OCI_ROOT}/index.json")"
OCI_TOP_DESCRIPTOR_COUNT=$(jq '.manifests | length' "${OCI_ROOT}/index.json")
[ "${OCI_TOP_DESCRIPTOR_COUNT}" -eq 1 ] || fail 'OCI layout must reference exactly one platform index'
jq -e \
  '.mediaType == "application/vnd.oci.image.index.v1+json" and
   .manifests[0].mediaType == "application/vnd.oci.image.index.v1+json"' \
  "${OCI_ROOT}/index.json" >/dev/null || fail 'OCI layout does not reference a nested platform index'
OCI_INDEX_DIGEST=$(jq -er '.manifests[0].digest' "${OCI_ROOT}/index.json")
verify_oci_blob "${OCI_INDEX_DIGEST}"
OCI_PLATFORM_INDEX=$(oci_blob_path "${OCI_INDEX_DIGEST}")
jq -e '.mediaType == "application/vnd.oci.image.index.v1+json"' \
  "${OCI_PLATFORM_INDEX}" >/dev/null || fail 'nested OCI descriptor is not a platform index'
AMD64_MANIFEST_DIGEST=$(inspect_oci_architecture amd64)
ARM64_MANIFEST_DIGEST=$(inspect_oci_architecture arm64)
[ "${AMD64_MANIFEST_DIGEST}" != "${ARM64_MANIFEST_DIGEST}" ] || fail 'architecture manifests have the same digest'
log "OCI linux/amd64 manifest: ${AMD64_MANIFEST_DIGEST}"
log "OCI linux/arm64 manifest: ${ARM64_MANIFEST_DIGEST}"

register_image "${AMD64_IMAGE}"
build_loaded_image amd64 "${AMD64_IMAGE}"
register_image "${ARM64_IMAGE}"
build_loaded_image arm64 "${ARM64_IMAGE}"

AMD64_RUNNABLE=0
ARM64_RUNNABLE=0
AMD64_RUNTIME_RESULT='unavailable_on_runner'
ARM64_RUNTIME_RESULT='unavailable_on_runner'
if [ "${SKIP_EMULATED_AMD64_RUNTIME}" -eq 1 ]; then
  AMD64_RUNTIME_RESULT='skipped_pending_native_amd64'
  log 'linux/amd64 runtime, smoke, and fault checks skipped by explicit arm64-runner override; native linux/amd64 acceptance remains required'
elif platform_runnable amd64 "${AMD64_IMAGE}"; then
  AMD64_RUNNABLE=1
  run_minimal_boot amd64 "${AMD64_IMAGE}"
  AMD64_RUNTIME_RESULT='passed'
fi
if platform_runnable arm64 "${ARM64_IMAGE}"; then
  ARM64_RUNNABLE=1
  run_minimal_boot arm64 "${ARM64_IMAGE}"
  ARM64_RUNTIME_RESULT='passed'
fi

if [ "${AMD64_RUNNABLE}" -eq 1 ]; then
  run_smoke_suite "${AMD64_IMAGE}" amd64 full
  run_fault_suite "${AMD64_IMAGE}" amd64
fi
if [ "${ARM64_RUNNABLE}" -eq 1 ]; then
  if [ "${HOST_ARCH}" = 'arm64' ]; then
    run_smoke_suite "${ARM64_IMAGE}" arm64 full
    run_fault_suite "${ARM64_IMAGE}" arm64
  else
    run_smoke_suite "${ARM64_IMAGE}" arm64 basic
  fi
fi

run_playwright_acceptance
if [ "${HOST_ARCH}" = 'arm64' ]; then
  assert_browser_isolation "${ARM64_IMAGE}" arm64
else
  assert_browser_isolation "${AMD64_IMAGE}" amd64
fi

assert_release_sources_clean
FINAL_SOURCE_FINGERPRINT=$(source_fingerprint)
[ "${INITIAL_SOURCE_FINGERPRINT}" = "${FINAL_SOURCE_FINGERPRINT}" ] ||
  fail 'release inputs or lockfiles changed during the architecture builds'

printf '\nTask 17 multi-architecture/browser acceptance: PASS\n'
printf 'run_id=%s\n' "${RUN_ID}"
printf 'source_commit=%s\n' "${SOURCE_COMMIT}"
printf 'source_fingerprint=sha256:%s\n' "${FINAL_SOURCE_FINGERPRINT}"
printf 'oci_layout_index_digest=%s\n' "${OCI_LAYOUT_INDEX_DIGEST}"
printf 'oci_platform_index_digest=%s\n' "${OCI_INDEX_DIGEST}"
printf 'linux_amd64_manifest=%s runnable=%s runtime=%s\n' \
  "${AMD64_MANIFEST_DIGEST}" "${AMD64_RUNNABLE}" "${AMD64_RUNTIME_RESULT}"
printf 'linux_arm64_manifest=%s runnable=%s runtime=%s\n' \
  "${ARM64_MANIFEST_DIGEST}" "${ARM64_RUNNABLE}" "${ARM64_RUNTIME_RESULT}"
printf 'buildx_cache=%s path=%s\n' "${BUILDX_CACHE_RESULT}" "${BUILDX_CACHE_DIR}"
printf 'playwright=29/29\n'
printf 'browser_isolation=release-added-layers+final-filesystem\n'
