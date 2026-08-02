#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.7.0

set -eu
umask 077
export LC_ALL=C
export LANG=C

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd "${SCRIPT_DIR}/../.." && pwd)
cd "${REPOSITORY_ROOT}"

# shellcheck source=lib/image.sh
. "${SCRIPT_DIR}/lib/image.sh"

PROJECT_VERSION=$(tr -d '\r\n' <"${REPOSITORY_ROOT}/VERSION")
IMAGE=${IMAGE:-nginx-uix:${PROJECT_VERSION}-test}
BUILD_IMAGE=${BUILD_IMAGE:-0}
PLATFORM=${PLATFORM:-}
SOURCE_VERSION=${SOURCE_VERSION:-0.7.0}
case "${SOURCE_VERSION}" in
  0.6.0) DEFAULT_SOURCE_REF=97036da
    EXPECTED_SOURCE_COMMIT=97036da9efbb3a614080565ef64785e75a7584fd
    ;;
  0.7.0) DEFAULT_SOURCE_REF=e46d34d
    EXPECTED_SOURCE_COMMIT=e46d34d6b12e8e0fdf31b606d73cd224fcc0d61e
    ;;
  *) DEFAULT_SOURCE_REF=
    EXPECTED_SOURCE_COMMIT=
    ;;
esac
SOURCE_REF=${SOURCE_REF:-${DEFAULT_SOURCE_REF}}
SOURCE_IMAGE=${SOURCE_IMAGE:-}
[ -n "${SOURCE_IMAGE}" ] || SOURCE_IMAGE="nginx-uix:${SOURCE_VERSION}-upgrade-seed"

RUN_RANDOM=$(openssl rand -hex 4)
RUN_ID="upgrade-$$-${RUN_RANDOM}"
LABEL="io.nginx-uix.upgrade-run=${RUN_ID}"
RESOURCE_PREFIX="nginx-uix-${RUN_ID}"
WORK_DIR="${TMPDIR:-/tmp}/nginx-uix-${RUN_ID}"
MAIN_CONTAINER="${RESOURCE_PREFIX}-main"
SOURCE_CONFIG_VOLUME="${RESOURCE_PREFIX}-source-config"
SOURCE_DATA_VOLUME="${RESOURCE_PREFIX}-source-data"
RESTORED_CONFIG_VOLUME="${RESOURCE_PREFIX}-restored-config"
RESTORED_DATA_VOLUME="${RESOURCE_PREFIX}-restored-data"
ADMIN_USERNAME="upgrade-admin-${RUN_RANDOM}"
RELEASE_WORKSPACE_NAME="upgrade-release-${RUN_RANDOM}"
OPEN_WORKSPACE_NAME="upgrade-open-${RUN_RANDOM}"
GROUP_NAME="upgrade-group-${RUN_RANDOM}"
CERTIFICATE_FIXTURE="upgrade-certificate-${RUN_RANDOM}"

OWNED_CONTAINERS=
OWNED_VOLUMES=
WORK_DIR_CREATED=0
ACTIVE_COMMAND_PID=
HELPER_SEQUENCE=0
BASE_URL=
CSRF_TOKEN=
HTTP_CODE=
RESPONSE_ETAG=

log() {
  printf '[upgrade] %s\n' "$1"
}

pass() {
  printf '[upgrade] PASS: %s\n' "$1"
}

fail() {
  printf '[upgrade] ERROR: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
}

digest_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

digest_text() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

register_container() {
  case "$1" in
    "${RESOURCE_PREFIX}"-*) ;;
    *) fail 'container name is outside the owned run prefix' ;;
  esac
  case " ${OWNED_CONTAINERS} " in
    *" $1 "*) return 0 ;;
  esac
  docker container inspect "$1" >/dev/null 2>&1 && fail 'owned container name already exists'
  OWNED_CONTAINERS="${OWNED_CONTAINERS} $1"
}

create_volume() {
  case "$1" in
    "${RESOURCE_PREFIX}"-*) ;;
    *) fail 'volume name is outside the owned run prefix' ;;
  esac
  docker volume inspect "$1" >/dev/null 2>&1 && fail 'owned volume name already exists'
  OWNED_VOLUMES="${OWNED_VOLUMES} $1"
  docker volume create --label "${LABEL}" "$1" >/dev/null
}

new_helper() {
  HELPER_SEQUENCE=$((HELPER_SEQUENCE + 1))
  HELPER_CONTAINER="${RESOURCE_PREFIX}-helper-${HELPER_SEQUENCE}"
  register_container "${HELPER_CONTAINER}"
}

cleanup() {
  cleanup_status=$?
  trap - EXIT HUP INT TERM
  if [ -n "${ACTIVE_COMMAND_PID}" ]; then
    kill -TERM "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
    wait "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
  fi
  if command -v docker >/dev/null 2>&1; then
    for cleanup_container in ${OWNED_CONTAINERS}; do
      docker rm --force --volumes "${cleanup_container}" >/dev/null 2>&1 || true
    done
    for cleanup_volume in ${OWNED_VOLUMES}; do
      docker volume rm "${cleanup_volume}" >/dev/null 2>&1 || true
    done
  fi
  if [ "${BUILDX_CACHE_LOCK_OWNED:-0}" = 1 ]; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
  fi
  if [ "${WORK_DIR_CREATED}" = 1 ]; then
    case "${WORK_DIR}" in
      "${TMPDIR:-/tmp}"/nginx-uix-upgrade-*) rm -rf "${WORK_DIR}" ;;
      *) printf '[upgrade] refusing unsafe cleanup path: %s\n' "${WORK_DIR}" >&2 ;;
    esac
  fi
  exit "${cleanup_status}"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

run_bounded() {
  bounded_limit=$1
  bounded_output=$2
  shift 2
  : >"${bounded_output}"
  "$@" >"${bounded_output}" 2>&1 &
  ACTIVE_COMMAND_PID=$!
  bounded_deadline=$(( $(date +%s) + bounded_limit ))
  while kill -0 "${ACTIVE_COMMAND_PID}" 2>/dev/null; do
    if [ "$(date +%s)" -ge "${bounded_deadline}" ]; then
      kill -TERM "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
      sleep 1
      kill -KILL "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
      wait "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
      ACTIVE_COMMAND_PID=
      return 124
    fi
    sleep 1
  done
  if wait "${ACTIVE_COMMAND_PID}"; then
    bounded_status=0
  else
    bounded_status=$?
  fi
  ACTIVE_COMMAND_PID=
  return "${bounded_status}"
}

wait_ready() {
  ready_deadline=$(( $(date +%s) + 90 ))
  while [ "$(date +%s)" -lt "${ready_deadline}" ]; do
    if [ "$(docker inspect --format '{{.State.Running}}' "${MAIN_CONTAINER}" 2>/dev/null || true)" = true ]; then
      ready_code=$(curl --silent --connect-timeout 1 --max-time 2 --output /dev/null \
        --write-out '%{http_code}' "${BASE_URL}/health/ready" || true)
      [ "${ready_code}" = 200 ] && return 0
    fi
    sleep 1
  done
  docker logs --tail 160 "${MAIN_CONTAINER}" >&2 || true
  return 1
}

container_url() {
  mapped_port=$(docker port "${MAIN_CONTAINER}" 9000/tcp | sed -n 's/^127\.0\.0\.1:\([0-9][0-9]*\)$/\1/p')
  case "${mapped_port}" in
    ''|*[!0-9]*) fail 'UI port is not mapped only to the host loopback address' ;;
  esac
  printf 'http://127.0.0.1:%s\n' "${mapped_port}"
}

start_application() {
  start_image=$1
  start_config=$2
  start_data=$3
  docker rm --force "${MAIN_CONTAINER}" >/dev/null 2>&1 || true
  set -- docker run --detach --name "${MAIN_CONTAINER}" --label "${LABEL}" \
    --publish '127.0.0.1::9000' \
    --mount "type=volume,src=${start_config},dst=/etc/nginx" \
    --mount "type=volume,src=${start_data},dst=/var/lib/nginx-uix" \
    --mount "type=bind,src=${WORK_DIR}/admin-password,dst=/run/secrets/nginx-uix-admin,readonly" \
    --env "NGINX_UIX_ADMIN_USERNAME=${ADMIN_USERNAME}" \
    --env 'NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin'
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${start_image}"
  run_bounded 30 "${WORK_DIR}/start.log" "$@" || fail "could not start ${start_image}"
  BASE_URL=$(container_url)
  wait_ready || fail "${start_image} did not become ready"
}

stop_application() {
  stop_label=$1
  run_bounded 25 "${WORK_DIR}/${stop_label}.stop.log" docker stop --time 15 "${MAIN_CONTAINER}" ||
    fail "${stop_label} did not stop within its bound"
  [ "$(docker inspect --format '{{.State.ExitCode}}' "${MAIN_CONTAINER}")" = 0 ] ||
    fail "${stop_label} did not exit cleanly"
}

ensure_source_image() {
  case "${SOURCE_REF}" in
    ''|-*|*'..'*|*[!A-Za-z0-9._/-]*) fail 'SOURCE_REF is unsafe' ;;
  esac
  source_version=$(git show "${SOURCE_REF}:VERSION" 2>/dev/null | tr -d '\r\n') ||
    fail "could not read the v${SOURCE_VERSION} source VERSION"
  [ "${source_version}" = "${SOURCE_VERSION}" ] ||
    fail "upgrade source VERSION is ${source_version}, want ${SOURCE_VERSION}"
  source_commit=$(git rev-parse --verify "${SOURCE_REF}^{commit}")
  [ "${source_commit}" = "${EXPECTED_SOURCE_COMMIT}" ] ||
    fail "upgrade source commit is ${source_commit}, want ${EXPECTED_SOURCE_COMMIT}"
  source_identity=$(docker image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{.Os}}/{{.Architecture}}' \
    "${SOURCE_IMAGE}" 2>/dev/null || true)
  if [ -n "${source_identity}" ]; then
    [ "${source_identity}" = "${SOURCE_VERSION}|${source_commit}|${PLATFORM}" ] ||
      fail "existing v${SOURCE_VERSION} fixture image has the wrong version, revision, or platform"
    pass "reused the exact v${SOURCE_VERSION} release-commit image"
    return
  fi

  source_context="${WORK_DIR}/source-context"
  mkdir "${source_context}"
  git archive --format=tar --output="${WORK_DIR}/source.tar" "${source_commit}"
  tar -xf "${WORK_DIR}/source.tar" -C "${source_context}"
  source_fingerprint=$(digest_file "${WORK_DIR}/source.tar")
  source_build_identity=$(printf '%s\n' \
    "source=${source_fingerprint}" "platform=${PLATFORM}" "version=${SOURCE_VERSION}" | digest_text)
  source_build_time=$(git show -s --format=%cI "${source_commit}")
  source_epoch=$(git show -s --format=%ct "${source_commit}")

  set -- docker buildx build --progress=plain --platform "${PLATFORM}" \
    --file "${source_context}/deploy/docker/Dockerfile" \
    --tag "${SOURCE_IMAGE}" --load \
    --build-arg "VERSION=${SOURCE_VERSION}" \
    --build-arg "COMMIT=${source_commit}" \
    --build-arg "BUILD_TIME=${source_build_time}" \
    --build-arg "SOURCE_FINGERPRINT=${source_fingerprint}" \
    --build-arg "BUILD_IDENTITY=${source_build_identity}" \
    --build-arg "SOURCE_DATE_EPOCH=${source_epoch}"
  [ -z "${BUILD_STEP_HTTP_PROXY:-}" ] || set -- "$@" --build-arg "http_proxy=${BUILD_STEP_HTTP_PROXY}"
  [ -z "${BUILD_STEP_HTTPS_PROXY:-}" ] || set -- "$@" --build-arg "https_proxy=${BUILD_STEP_HTTPS_PROXY}"
  [ -z "${BUILD_STEP_NO_PROXY:-}" ] || set -- "$@" --build-arg "no_proxy=${BUILD_STEP_NO_PROXY}"
  set -- "$@" "${source_context}"
  if ! run_bounded 1200 "${WORK_DIR}/source-build.log" "$@"; then
    tail -n 160 "${WORK_DIR}/source-build.log" >&2 || true
    fail "v${SOURCE_VERSION} release-commit image build failed"
  fi
  source_identity=$(docker image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{.Os}}/{{.Architecture}}' \
    "${SOURCE_IMAGE}")
  [ "${source_identity}" = "${SOURCE_VERSION}|${source_commit}|${PLATFORM}" ] ||
    fail "built v${SOURCE_VERSION} fixture image has the wrong version, revision, or platform"
  pass "built the exact v${SOURCE_VERSION} release commit for the native architecture"
}

prepare_source_config_compatibility() {
  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${SOURCE_IMAGE}" -eu -c 'stat -c "%a:%u:%g" /etc /etc/nginx'
  run_bounded 30 "${WORK_DIR}/source-image-nginx-root.log" "$@" ||
    fail "could not inspect the v${SOURCE_VERSION} image Nginx root"
  source_image_root_modes=$(tr '\n' '|' <"${WORK_DIR}/source-image-nginx-root.log")
  [ "${source_image_root_modes}" = '700:0:0|755:0:0|' ] ||
    fail "the v${SOURCE_VERSION} release image no longer exposes the documented /etc and Nginx-root modes"

  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh \
    --mount "type=volume,src=${SOURCE_CONFIG_VOLUME},dst=/etc/nginx"
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${SOURCE_IMAGE}" -eu -c '
    test -z "$(find /etc/nginx -mindepth 1 -print -quit)"
    test "$(stat -c "%a:%u:%g" /etc/nginx)" = 755:0:0
    cp -a /usr/share/nginx-uix/default-nginx/. /etc/nginx/
    test -f /etc/nginx/nginx.conf
    chmod 0700 /etc/nginx
    test "$(stat -c "%a:%u:%g" /etc/nginx)" = 700:0:0
  '
  run_bounded 30 "${WORK_DIR}/source-config-compatibility.log" "$@" ||
    fail "could not prepare the empty v${SOURCE_VERSION} configuration volume compatibility mode"

  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh \
    --mount "type=volume,src=${SOURCE_CONFIG_VOLUME},dst=/etc/nginx,readonly"
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${SOURCE_IMAGE}" -eu -c '
    test "$(stat -c "%a:%u:%g" /etc/nginx)" = 700:0:0
    test -f /etc/nginx/nginx.conf
  '
  run_bounded 30 "${WORK_DIR}/source-config-compatibility-remount.log" "$@" ||
    fail "v${SOURCE_VERSION} configuration compatibility did not survive a fresh mount"
  log "v${SOURCE_VERSION} compatibility: retained the exact release image, pre-copied its immutable defaults and changed only the volume root from 0755 to 0700"
  log "v${SOURCE_VERSION} compatibility reason: startup validation rejects the image-owned 0755 /etc/nginx root"
}

restore_source_runtime_config_mode() {
  run_bounded 30 "${WORK_DIR}/source-config-runtime-mode.log" docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c '
    test "$(stat -c "%a:%u:%g" /etc)" = 700:0:0
    test "$(stat -c "%a:%u:%g" /etc/nginx)" = 700:0:0
    chmod 0755 /etc /etc/nginx
    test "$(stat -c "%a:%u:%g" /etc)" = 755:0:0
    test "$(stat -c "%a:%u:%g" /etc/nginx)" = 755:0:0
  ' || fail "could not restore the v${SOURCE_VERSION} runtime Nginx-root mode"
  ready_code=$(curl --silent --connect-timeout 1 --max-time 3 --output /dev/null \
    --write-out '%{http_code}' "${BASE_URL}/health/ready" || true)
  [ "${ready_code}" = 200 ] ||
    fail "v${SOURCE_VERSION} lost readiness after restoring the runtime Nginx-root mode"
  log "v${SOURCE_VERSION} compatibility: restored /etc and the Nginx volume root to 0755 after Agent startup so the Nginx worker can traverse its document root"
}

login() {
  login_prefix=$1
  jq -n --arg username "${ADMIN_USERNAME}" --arg password "${ADMIN_PASSWORD}" \
    '{username:$username,password:$password}' >"${WORK_DIR}/${login_prefix}.request.json"
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 15 \
    --output "${WORK_DIR}/${login_prefix}.response.json" \
    --dump-header "${WORK_DIR}/${login_prefix}.headers" \
    --write-out '%{http_code}' \
    --cookie-jar "${WORK_DIR}/session.cookie" \
    --header "Origin: ${BASE_URL}" \
    --header 'Content-Type: application/json' \
    --data-binary "@${WORK_DIR}/${login_prefix}.request.json" \
    "${BASE_URL}/api/v1/auth/session")
  [ "${HTTP_CODE}" = 200 ] || fail 'login did not return HTTP 200'
  CSRF_TOKEN=$(jq -er '.csrf_token | select(type == "string" and length > 20)' \
    "${WORK_DIR}/${login_prefix}.response.json")
}

api_get() {
  get_url=$1
  get_output=$2
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 20 \
    --output "${get_output}" --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" "${get_url}")
  [ "${HTTP_CODE}" = 200 ] || fail "authenticated GET returned ${HTTP_CODE}"
}

api_mutation() {
  mutation_method=$1
  mutation_url=$2
  mutation_body=$3
  mutation_etag=$4
  mutation_expected=$5
  mutation_prefix=$6
  set -- curl --silent --show-error --connect-timeout 2 --max-time 60 \
    --request "${mutation_method}" \
    --output "${WORK_DIR}/${mutation_prefix}.response.json" \
    --dump-header "${WORK_DIR}/${mutation_prefix}.headers" \
    --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" \
    --header "Origin: ${BASE_URL}" \
    --header "X-CSRF-Token: ${CSRF_TOKEN}" \
    --header 'Content-Type: application/json'
  [ -z "${mutation_etag}" ] || set -- "$@" --header "If-Match: ${mutation_etag}"
  set -- "$@" --data-binary "@${mutation_body}" "${mutation_url}"
  HTTP_CODE=$("$@")
  [ "${HTTP_CODE}" = "${mutation_expected}" ] ||
    fail "mutation ${mutation_prefix} returned ${HTTP_CODE}, want ${mutation_expected}"
  RESPONSE_ETAG=$(sed -n 's/^[Ee][Tt][Aa][Gg]:[[:space:]]*//p' \
    "${WORK_DIR}/${mutation_prefix}.headers" | tr -d '\r' | sed -n '1p')
}

workspace_etag() {
  jq -er '.draft_etag | select(test("^\\\"draft-v1:[0-9a-f]{64}\\\"$"))' "$1"
}

create_workspace() {
  create_name=$1
  create_prefix=$2
  jq -n --arg name "${create_name}" '{name:$name}' >"${WORK_DIR}/${create_prefix}.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces" \
    "${WORK_DIR}/${create_prefix}.request.json" '' 201 "${create_prefix}"
  CREATED_WORKSPACE_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/${create_prefix}.response.json")
  CREATED_WORKSPACE_ETAG=$(workspace_etag "${WORK_DIR}/${create_prefix}.response.json")
  [ "${RESPONSE_ETAG}" = "${CREATED_WORKSPACE_ETAG}" ] || fail 'workspace ETag differs by transport'
  jq -e '.state == "ready"' "${WORK_DIR}/${create_prefix}.response.json" >/dev/null ||
    fail 'new workspace is not ready'
}

mutate_default_file() {
  mutate_workspace_id=$1
  mutate_etag=$2
  mutate_marker=$3
  mutate_prefix=$4
  api_get "${BASE_URL}/api/v1/config/workspaces/${mutate_workspace_id}/files?path=conf.d%2Fdefault.conf" \
    "${WORK_DIR}/${mutate_prefix}.read.json"
  jq --arg marker "${mutate_marker}" '{content:(.content + "\n# " + $marker + "\n")}' \
    "${WORK_DIR}/${mutate_prefix}.read.json" >"${WORK_DIR}/${mutate_prefix}.request.json"
  api_mutation PUT \
    "${BASE_URL}/api/v1/config/workspaces/${mutate_workspace_id}/files?path=conf.d%2Fdefault.conf" \
    "${WORK_DIR}/${mutate_prefix}.request.json" "${mutate_etag}" 200 "${mutate_prefix}"
  MUTATED_WORKSPACE_ETAG=$(workspace_etag "${WORK_DIR}/${mutate_prefix}.response.json")
  [ "${RESPONSE_ETAG}" = "${MUTATED_WORKSPACE_ETAG}" ] || fail 'file mutation ETag differs by transport'
}

create_group() {
  api_get "${BASE_URL}/api/v1/config/groups" "${WORK_DIR}/groups-initial.json"
  groups_etag=$(jq -er '.groups_etag | select(test("^\\\"groups-v1:[0-9a-f]{64}\\\"$"))' \
    "${WORK_DIR}/groups-initial.json")
  jq -n --arg name "${GROUP_NAME}" \
    '{name:$name,sort_order:70,members:["conf.d/default.conf"]}' >"${WORK_DIR}/group-create.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/groups" "${WORK_DIR}/group-create.request.json" \
    "${groups_etag}" 201 group-create
  GROUP_ID=$(jq -er --arg name "${GROUP_NAME}" \
    '.groups[] | select(.name == $name) | .id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/group-create.response.json")
}

publish_workspace() {
  publish_workspace_id=$1
  publish_workspace_name=$2
  publish_workspace_etag=$3
  jq -n '{}' >"${WORK_DIR}/publish-check.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces/${publish_workspace_id}/publish-checks" \
    "${WORK_DIR}/publish-check.request.json" "${publish_workspace_etag}" 201 publish-check
  check_id=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/publish-check.response.json")
  jq -e '.state == "valid" and .diagnostic_count == 0' \
    "${WORK_DIR}/publish-check.response.json" >/dev/null ||
    fail "v${SOURCE_VERSION} publish check is not valid"
  jq -n --arg check_id "${check_id}" --arg name "${publish_workspace_name}" \
    '{check_id:$check_id,confirm_name:$name}' >"${WORK_DIR}/release.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces/${publish_workspace_id}/releases" \
    "${WORK_DIR}/release.request.json" "${publish_workspace_etag}" 202 release
  RELEASE_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' "${WORK_DIR}/release.response.json")
  release_deadline=$(( $(date +%s) + 150 ))
  while [ "$(date +%s)" -lt "${release_deadline}" ]; do
    api_get "${BASE_URL}/api/v1/config/releases/${RELEASE_ID}" "${WORK_DIR}/release-terminal.json"
    release_state=$(jq -er '.state' "${WORK_DIR}/release-terminal.json")
    case "${release_state}" in
      succeeded|failed|rolled_back|needs_attention|cancelled) break ;;
    esac
    sleep 1
  done
  if ! jq -e '.state == "succeeded" and .stage == "committed" and (.backup_id | test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/release-terminal.json" >/dev/null; then
    jq . "${WORK_DIR}/release-terminal.json" >&2 || true
    docker logs --tail 200 "${MAIN_CONTAINER}" >&2 || true
    fail "v${SOURCE_VERSION} release did not commit"
  fi
  BACKUP_ID=$(jq -er '.backup_id' "${WORK_DIR}/release-terminal.json")
}

exercise_login_throttle() {
  blocked_username="upgrade-blocked-${RUN_RANDOM}"
  wrong_password=$(openssl rand -hex 16)
  jq -n --arg username "${blocked_username}" --arg password "${wrong_password}" \
    '{username:$username,password:$password}' >"${WORK_DIR}/blocked-login.request.json"
  throttle_code=
  for throttle_attempt in 1 2 3 4 5 6 7; do
    throttle_code=$(curl --silent --connect-timeout 2 --max-time 10 --output /dev/null \
      --write-out '%{http_code}' --header "Origin: ${BASE_URL}" \
      --header 'Content-Type: application/json' \
      --data-binary "@${WORK_DIR}/blocked-login.request.json" \
      "${BASE_URL}/api/v1/auth/session" || true)
    case "${throttle_code}" in 401|429) ;; *) fail 'login throttle fixture returned an unexpected status' ;; esac
  done
  [ "${throttle_code}" = 429 ] || fail 'login throttle fixture did not reach a blocked state'
}

install_certificate_fixture() {
  mkdir "${WORK_DIR}/certificate-fixture"
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=${CERTIFICATE_FIXTURE}.example.test" \
    -keyout "${WORK_DIR}/certificate-fixture/private.key" \
    -out "${WORK_DIR}/certificate-fixture/fullchain.pem" \
    >"${WORK_DIR}/certificate-fixture/openssl.log" 2>&1 || fail 'could not create certificate fixture'
  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh \
    --mount "type=volume,src=${SOURCE_DATA_VOLUME},dst=/data" \
    --mount "type=bind,src=${WORK_DIR}/certificate-fixture,dst=/fixture,readonly"
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${IMAGE}" -eu -c "
    target='/data/certs/staging/${CERTIFICATE_FIXTURE}'
    mkdir \"\${target}\"
    cp /fixture/fullchain.pem \"\${target}/fullchain.pem\"
    cp /fixture/private.key \"\${target}/private.key\"
    ln -s fullchain.pem \"\${target}/current.pem\"
    chown -R 10001:10001 \"\${target}\"
    chown -h 10001:10001 \"\${target}/current.pem\"
    chmod 0700 \"\${target}\"
    chmod 0600 \"\${target}/fullchain.pem\" \"\${target}/private.key\"
  "
  run_bounded 30 "${WORK_DIR}/certificate-install.log" "$@" ||
    fail 'could not install certificate fixture in the persistent vault'
}

volume_manifest() {
  manifest_volume=$1
  manifest_output=$2
  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh \
    --mount "type=volume,src=${manifest_volume},dst=/source,readonly"
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${IMAGE}" -eu -c '
    cd /source
    find . -print | LC_ALL=C sort | while IFS= read -r entry; do
      metadata=$(stat -c "%a:%u:%g" "${entry}")
      if [ -L "${entry}" ]; then
        printf "symlink|%s|%s|%s\n" "${metadata}" "${entry}" "$(readlink "${entry}")"
      elif [ -f "${entry}" ]; then
        digest=$(sha256sum "${entry}" | awk "{print \$1}")
        printf "file|%s|%s|%s\n" "${metadata}" "${entry}" "${digest}"
      elif [ -d "${entry}" ]; then
        printf "directory|%s|%s|\n" "${metadata}" "${entry}"
      else
        printf "other|%s|%s|\n" "${metadata}" "${entry}"
      fi
    done
  '
  run_bounded 60 "${manifest_output}" "$@" || fail "could not inventory volume ${manifest_volume}"
}

copy_volume() {
  copy_source=$1
  copy_target=$2
  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh \
    --mount "type=volume,src=${copy_source},dst=/source,readonly" \
    --mount "type=volume,src=${copy_target},dst=/target"
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${IMAGE}" -eu -c '
    test -z "$(find /target -mindepth 1 -print -quit)"
    tar -C /source -cpf /tmp/nginx-uix-volume.tar .
    tar -C /target -xpf /tmp/nginx-uix-volume.tar
    find /source -type l -print | while IFS= read -r source_link; do
      relative_link=${source_link#/source/}
      chown -h "$(stat -c "%u:%g" "${source_link}")" "/target/${relative_link}"
    done
    chown "$(stat -c "%u:%g" /source)" /target
    chmod "$(stat -c "%a" /source)" /target
    rm /tmp/nginx-uix-volume.tar
  '
  run_bounded 120 "${WORK_DIR}/copy-${copy_target}.log" "$@" ||
    fail "could not copy ${copy_source} to the empty ${copy_target}"
}

copy_container_data() {
  copy_label=$1
  copy_destination="${WORK_DIR}/${copy_label}"
  mkdir "${copy_destination}"
  if ! run_bounded 60 "${WORK_DIR}/${copy_label}.copy.log" \
    docker cp "${MAIN_CONTAINER}:/var/lib/nginx-uix/nginx-uix.db" \
      "${copy_destination}/nginx-uix.db"; then
    tail -n 80 "${WORK_DIR}/${copy_label}.copy.log" >&2 || true
    fail "could not copy ${copy_label} data from the stopped container"
  fi
  COPIED_DATABASE="${copy_destination}/nginx-uix.db"
  [ -f "${COPIED_DATABASE}" ] || fail "${copy_label} database is absent"
}

assert_database_state() {
  database_file=$1
  [ "$(sqlite3 "${database_file}" 'PRAGMA integrity_check;')" = ok ] ||
    fail 'SQLite integrity_check failed'
  [ -z "$(sqlite3 "${database_file}" 'PRAGMA foreign_key_check;')" ] ||
    fail 'SQLite foreign_key_check failed'
  migrations=$(sqlite3 "${database_file}" \
    "SELECT COALESCE(group_concat(version, ','), '') FROM (SELECT version FROM schema_migrations ORDER BY version);")
  [ "${migrations}" = '1,2,3,4,5,6,7' ] || fail 'database migrations are not exactly [1..7]'
  [ "$(sqlite3 "${database_file}" "SELECT state FROM config_workspaces WHERE id='${RELEASE_WORKSPACE_ID}';")" = published ] ||
    fail 'published workspace state was not preserved'
  [ "$(sqlite3 "${database_file}" "SELECT state FROM config_workspaces WHERE id='${OPEN_WORKSPACE_ID}';")" = ready ] ||
    fail 'open workspace state was not preserved'
  [ "$(sqlite3 "${database_file}" "SELECT COUNT(*) FROM config_groups WHERE id='${GROUP_ID}';")" = 1 ] ||
    fail 'configuration group was not preserved'
  [ "$(sqlite3 "${database_file}" "SELECT COUNT(*) FROM config_group_members WHERE group_id='${GROUP_ID}' AND path='conf.d/default.conf';")" = 1 ] ||
    fail 'configuration group membership was not preserved'
  [ "$(sqlite3 "${database_file}" "SELECT state || ':' || backup_id FROM config_releases WHERE id='${RELEASE_ID}';")" = "succeeded:${BACKUP_ID}" ] ||
    fail 'release terminal state or backup link was not preserved'
  [ "$(sqlite3 "${database_file}" "SELECT state FROM config_backups WHERE id='${BACKUP_ID}';")" = complete ] ||
    fail 'release backup state was not preserved'
  [ "$(sqlite3 "${database_file}" 'SELECT COUNT(*) FROM sessions;')" -ge 1 ] ||
    fail 'open session was not preserved'
  [ "$(sqlite3 "${database_file}" 'SELECT COUNT(*) FROM login_throttles;')" -ge 1 ] ||
    fail 'login throttle state was not preserved'
  [ "$(sqlite3 "${database_file}" 'SELECT COUNT(*) FROM audit_events;')" -ge 5 ] ||
    fail 'audit history was not preserved'
}

assert_certificate_fixture() {
  expected_data_volume=$1
  new_helper
  set -- docker run --rm --name "${HELPER_CONTAINER}" --label "${LABEL}" --entrypoint /bin/sh \
    --mount "type=volume,src=${expected_data_volume},dst=/data,readonly" \
    --mount "type=bind,src=${WORK_DIR}/certificate-fixture,dst=/fixture,readonly"
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  set -- "$@" "${IMAGE}" -eu -c "
    target='/data/certs/staging/${CERTIFICATE_FIXTURE}'
    test \"\$(stat -c '%a:%u:%g' \"\${target}\")\" = 700:10001:10001
    test \"\$(stat -c '%a:%u:%g' \"\${target}/fullchain.pem\")\" = 600:10001:10001
    test \"\$(stat -c '%a:%u:%g' \"\${target}/private.key\")\" = 600:10001:10001
    test -L \"\${target}/current.pem\"
    test \"\$(readlink \"\${target}/current.pem\")\" = fullchain.pem
    cmp -s /fixture/fullchain.pem \"\${target}/fullchain.pem\"
    cmp -s /fixture/private.key \"\${target}/private.key\"
  "
  run_bounded 30 "${WORK_DIR}/certificate-${expected_data_volume}.log" "$@" ||
    fail 'certificate bytes, symlink, owner, or mode were not preserved'
}

assert_domain_api() {
  domain_label=$1
  api_get "${BASE_URL}/api/v1/auth/session" "${WORK_DIR}/${domain_label}-session.json"
  jq -e --arg username "${ADMIN_USERNAME}" \
    '.user.username == $username and (.csrf_token | type == "string" and length > 20)' \
    "${WORK_DIR}/${domain_label}-session.json" >/dev/null || fail 'authenticated session was not preserved'
  CSRF_TOKEN=$(jq -er '.csrf_token' "${WORK_DIR}/${domain_label}-session.json")

  api_get "${BASE_URL}/api/v1/config/workspaces/${OPEN_WORKSPACE_ID}" \
    "${WORK_DIR}/${domain_label}-workspace.json"
  jq -e --arg id "${OPEN_WORKSPACE_ID}" --arg name "${OPEN_WORKSPACE_NAME}" \
    '.id == $id and .name == $name and .state == "ready"' \
    "${WORK_DIR}/${domain_label}-workspace.json" >/dev/null || fail 'open workspace was not preserved'
  api_get "${BASE_URL}/api/v1/config/workspaces/${OPEN_WORKSPACE_ID}/files?path=conf.d%2Fdefault.conf" \
    "${WORK_DIR}/${domain_label}-draft.json"
  jq -e --arg marker "${OPEN_MARKER}" '.content | contains($marker)' \
    "${WORK_DIR}/${domain_label}-draft.json" >/dev/null || fail 'open workspace draft bytes were not preserved'

  api_get "${BASE_URL}/api/v1/config/groups" "${WORK_DIR}/${domain_label}-groups.json"
  jq -e --arg id "${GROUP_ID}" --arg name "${GROUP_NAME}" \
    'any(.groups[]; .id == $id and .name == $name and (.members | index("conf.d/default.conf")) != null)' \
    "${WORK_DIR}/${domain_label}-groups.json" >/dev/null || fail 'group metadata was not preserved'
  api_get "${BASE_URL}/api/v1/config/history/releases?limit=100" \
    "${WORK_DIR}/${domain_label}-releases.json"
  jq -e --arg id "${RELEASE_ID}" 'any(.items[]; .id == $id and .state == "succeeded")' \
    "${WORK_DIR}/${domain_label}-releases.json" >/dev/null || fail 'release history was not preserved'
  api_get "${BASE_URL}/api/v1/config/backups?limit=100" \
    "${WORK_DIR}/${domain_label}-backups.json"
  jq -e --arg id "${BACKUP_ID}" \
    'any(.items[]; .id == $id and .state == "complete" and .body_present == true)' \
    "${WORK_DIR}/${domain_label}-backups.json" >/dev/null || fail 'backup inventory/body was not preserved'
  api_get "${BASE_URL}/api/v1/config/audit-events?limit=100" \
    "${WORK_DIR}/${domain_label}-audit.json"
  jq -e '(.items | length) >= 5 and all(.items[]; (.details | type) == "object")' \
    "${WORK_DIR}/${domain_label}-audit.json" >/dev/null || fail 'audit history was not preserved'
  api_get "${BASE_URL}/api/v1/nginx/effective-config" \
    "${WORK_DIR}/${domain_label}-effective.json"
  jq -e --arg marker "${RELEASE_MARKER}" \
    'any(.occurrences[]; .content | contains($marker))' \
    "${WORK_DIR}/${domain_label}-effective.json" >/dev/null || fail 'released Nginx configuration was not preserved'
}

validate_inputs() {
  case "${BUILD_IMAGE}" in auto|0|1) ;; *) fail 'BUILD_IMAGE must be auto, 0, or 1' ;; esac
  case "${PLATFORM}" in ''|linux/amd64|linux/arm64) ;; *) fail 'PLATFORM must be empty, linux/amd64, or linux/arm64' ;; esac
  case "${SOURCE_VERSION}" in
    0.6.0|0.7.0) ;;
    *) fail 'SOURCE_VERSION must be 0.6.0 or 0.7.0' ;;
  esac
  [ -n "${EXPECTED_SOURCE_COMMIT}" ] || fail 'upgrade source commit is not pinned'
  for required_command in docker curl openssl git go jq sqlite3 tar awk sed grep cmp diff date chmod; do
    require_command "${required_command}"
  done
  if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    fail 'sha256sum or shasum is required'
  fi
  [ "${PROJECT_VERSION}" = 1.1.0 ] || fail 'upgrade acceptance requires VERSION 1.1.0'
  docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'
  docker buildx version >/dev/null 2>&1 || fail 'Docker Buildx is unavailable'
}

main() {
  validate_inputs
  [ ! -e "${WORK_DIR}" ] && [ ! -L "${WORK_DIR}" ] || fail 'run-specific work directory exists'
  mkdir "${WORK_DIR}"
  WORK_DIR_CREATED=1
  chmod 0700 "${WORK_DIR}"
  ADMIN_PASSWORD=$(openssl rand -hex 24)
  printf '%s\n' "${ADMIN_PASSWORD}" >"${WORK_DIR}/admin-password"
  chmod 0444 "${WORK_DIR}/admin-password"

  register_container "${MAIN_CONTAINER}"
  create_volume "${SOURCE_CONFIG_VOLUME}"
  create_volume "${SOURCE_DATA_VOLUME}"
  create_volume "${RESTORED_CONFIG_VOLUME}"
  create_volume "${RESTORED_DATA_VOLUME}"

  ensure_test_image "${IMAGE}" "${BUILD_IMAGE}" "${PLATFORM}" ||
    fail "v${PROJECT_VERSION} release image identity could not be ensured"
  pass "v${PROJECT_VERSION} image has the exact deterministic source and native platform identity"
  ensure_source_image
  prepare_source_config_compatibility

  log "creating real v${SOURCE_VERSION} users, session, throttle, workspaces, group, release, backup and audit state"
  start_application "${SOURCE_IMAGE}" "${SOURCE_CONFIG_VOLUME}" "${SOURCE_DATA_VOLUME}"
  restore_source_runtime_config_mode
  login source-login
  source_marker_version=$(printf '%s' "${SOURCE_VERSION}" | tr '.' '-')
  RELEASE_MARKER="released-by-v${source_marker_version}-${RUN_RANDOM}"
  OPEN_MARKER="open-draft-v${source_marker_version}-${RUN_RANDOM}"
  create_workspace "${RELEASE_WORKSPACE_NAME}" release-workspace
  RELEASE_WORKSPACE_ID=${CREATED_WORKSPACE_ID}
  mutate_default_file "${RELEASE_WORKSPACE_ID}" "${CREATED_WORKSPACE_ETAG}" \
    "${RELEASE_MARKER}" release-file
  RELEASE_WORKSPACE_ETAG=${MUTATED_WORKSPACE_ETAG}
  create_group
  publish_workspace "${RELEASE_WORKSPACE_ID}" "${RELEASE_WORKSPACE_NAME}" \
    "${RELEASE_WORKSPACE_ETAG}"
  create_workspace "${OPEN_WORKSPACE_NAME}" open-workspace
  OPEN_WORKSPACE_ID=${CREATED_WORKSPACE_ID}
  mutate_default_file "${OPEN_WORKSPACE_ID}" "${CREATED_WORKSPACE_ETAG}" \
    "${OPEN_MARKER}" open-file
  OPEN_WORKSPACE_ETAG=${MUTATED_WORKSPACE_ETAG}
  [ -n "${OPEN_WORKSPACE_ETAG}" ] || fail 'open workspace ETag is empty'
  exercise_login_throttle
  install_certificate_fixture
  assert_certificate_fixture "${SOURCE_DATA_VOLUME}"
  stop_application source-seed
  volume_manifest "${SOURCE_CONFIG_VOLUME}" "${WORK_DIR}/source-config.manifest"
  copy_container_data source-data
  SOURCE_DATABASE=${COPIED_DATABASE}
  assert_database_state "${SOURCE_DATABASE}"
  pass "v${SOURCE_VERSION} fixture contains durable domain data and exact certificate-vault bytes"

  log "opening the two v${SOURCE_VERSION} persistent roots directly with v${PROJECT_VERSION}"
  start_application "${IMAGE}" "${SOURCE_CONFIG_VOLUME}" "${SOURCE_DATA_VOLUME}"
  assert_domain_api direct-upgrade
  assert_certificate_fixture "${SOURCE_DATA_VOLUME}"
  stop_application direct-upgrade
  volume_manifest "${SOURCE_CONFIG_VOLUME}" "${WORK_DIR}/v1-upgraded-config.manifest"
  cmp -s "${WORK_DIR}/source-config.manifest" "${WORK_DIR}/v1-upgraded-config.manifest" ||
    fail "direct v${SOURCE_VERSION} to v${PROJECT_VERSION} upgrade changed Nginx configuration bytes or metadata"
  copy_container_data v1-upgraded-data
  V1_DATABASE=${COPIED_DATABASE}
  assert_database_state "${V1_DATABASE}"
  pass "direct v${SOURCE_VERSION} to v${PROJECT_VERSION} upgrade preserved both roots and all seeded open/terminal state"

  log "copying a stopped v${PROJECT_VERSION} cold backup of both roots into two new empty volumes"
  volume_manifest "${SOURCE_CONFIG_VOLUME}" "${WORK_DIR}/cold-source-config.manifest"
  volume_manifest "${SOURCE_DATA_VOLUME}" "${WORK_DIR}/cold-source-data.manifest"
  copy_volume "${SOURCE_CONFIG_VOLUME}" "${RESTORED_CONFIG_VOLUME}"
  copy_volume "${SOURCE_DATA_VOLUME}" "${RESTORED_DATA_VOLUME}"
  volume_manifest "${SOURCE_CONFIG_VOLUME}" "${WORK_DIR}/cold-source-config-after.manifest"
  volume_manifest "${SOURCE_DATA_VOLUME}" "${WORK_DIR}/cold-source-data-after.manifest"
  volume_manifest "${RESTORED_CONFIG_VOLUME}" "${WORK_DIR}/cold-restored-config.manifest"
  volume_manifest "${RESTORED_DATA_VOLUME}" "${WORK_DIR}/cold-restored-data.manifest"
  cmp -s "${WORK_DIR}/cold-source-config.manifest" "${WORK_DIR}/cold-source-config-after.manifest" ||
    fail 'cold backup read changed the source configuration volume'
  cmp -s "${WORK_DIR}/cold-source-data.manifest" "${WORK_DIR}/cold-source-data-after.manifest" ||
    fail 'cold backup read changed the source data volume'
  if ! cmp -s "${WORK_DIR}/cold-source-config.manifest" "${WORK_DIR}/cold-restored-config.manifest"; then
    diff -u "${WORK_DIR}/cold-source-config.manifest" \
      "${WORK_DIR}/cold-restored-config.manifest" >&2 || true
    fail 'cold-restored configuration digest, owner, mode, type, or symlink differs'
  fi
  if ! cmp -s "${WORK_DIR}/cold-source-data.manifest" "${WORK_DIR}/cold-restored-data.manifest"; then
    diff -u "${WORK_DIR}/cold-source-data.manifest" \
      "${WORK_DIR}/cold-restored-data.manifest" >&2 || true
    fail 'cold-restored data digest, owner, mode, type, or symlink differs'
  fi
  assert_certificate_fixture "${RESTORED_DATA_VOLUME}"
  pass 'stopped cold copy preserved every file digest, owner/mode, entry type and symlink in both roots'

  log "starting v${PROJECT_VERSION} only from the new cold-restored volumes"
  start_application "${IMAGE}" "${RESTORED_CONFIG_VOLUME}" "${RESTORED_DATA_VOLUME}"
  assert_domain_api cold-restore
  assert_certificate_fixture "${RESTORED_DATA_VOLUME}"
  stop_application cold-restore
  volume_manifest "${RESTORED_CONFIG_VOLUME}" "${WORK_DIR}/cold-restored-config-after-start.manifest"
  cmp -s "${WORK_DIR}/cold-source-config.manifest" "${WORK_DIR}/cold-restored-config-after-start.manifest" ||
    fail "restored v${PROJECT_VERSION} runtime changed the Nginx configuration truth"
  copy_container_data cold-restored-data
  COLD_DATABASE=${COPIED_DATABASE}
  assert_database_state "${COLD_DATABASE}"
  pass 'new cold-restored volumes preserve SQLite, config, certs, backups, histories, session and open workspace'

  printf '\nDocker v%s to v%s upgrade and cold-backup recovery acceptance: PASS\n' \
    "${SOURCE_VERSION}" "${PROJECT_VERSION}"
  printf 'source_version=%s source_commit=%s release_id=%s backup_id=%s open_workspace_id=%s group_id=%s\n' \
    "${SOURCE_VERSION}" "${source_commit}" "${RELEASE_ID}" "${BACKUP_ID}" \
    "${OPEN_WORKSPACE_ID}" "${GROUP_ID}"
}

main "$@"
