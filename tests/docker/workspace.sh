#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd "${SCRIPT_DIR}/../.." && pwd)
cd "${REPOSITORY_ROOT}"

# shellcheck source=lib/image.sh
. "${SCRIPT_DIR}/lib/image.sh"

IMAGE=${IMAGE:-nginx-uix:0.2.1-test}
BUILD_IMAGE=${BUILD_IMAGE:-0}
V01_SOURCE_REF=${V01_SOURCE_REF:-d1446e8}
V01_IMAGE=${V01_IMAGE:-nginx-uix:0.1.0-upgrade-seed}
PLAYWRIGHT_IMAGE='mcr.microsoft.com/playwright:v1.61.0-noble@sha256:57b65fdc9ceabe0ef613124c7bbe2babcf9362c4d85e382fe3b03604e84b428a'
FIXTURE_ROOT="${REPOSITORY_ROOT}/tests/fixtures/nginx/workspace"
BROWSER_SPEC="${REPOSITORY_ROOT}/web/e2e/config_workspace_docker.spec.ts"
PRIVATE_MARKER='NGINX-UIX-E2E-NOT-BASE64-NOT-A-KEY'

RUN_RANDOM=$(openssl rand -hex 4)
RUN_ID="workspace-$$-${RUN_RANDOM}"
LABEL="io.nginx-uix.workspace-run=${RUN_ID}"
NETWORK="nginx-uix-${RUN_ID}"
CONFIG_VOLUME="nginx-uix-${RUN_ID}-config"
DATA_VOLUME="nginx-uix-${RUN_ID}-data"
READONLY_DATA_VOLUME="nginx-uix-${RUN_ID}-readonly-data"
MAIN_CONTAINER="nginx-uix-${RUN_ID}-main"
READONLY_CONTAINER="nginx-uix-${RUN_ID}-readonly"
SMALL_CONTAINER="nginx-uix-${RUN_ID}-small"
BROWSER_CONTAINER="nginx-uix-${RUN_ID}-browser"
BROWSER_TEST_IMAGE="nginx-uix-playwright-${RUN_ID}"
WORK_DIR="${TMPDIR:-/tmp}/nginx-uix-${RUN_ID}"
ADMIN_USERNAME="workspace-admin-${RUN_RANDOM}"
WORKSPACE_NAME="workspace-${RUN_RANDOM}"

OWN_CONTAINERS=""
OWN_VOLUMES=""
OWN_IMAGES=""
NETWORK_CREATED=0
WORK_DIR_CREATED=0
ACTIVE_COMMAND_PID=
ACTIVE_WATCHDOG_PID=
HTTP_CODE=
RESPONSE_ETAG=
CSRF_TOKEN=
BASE_URL=

log() {
  printf '[workspace] %s\n' "$1"
}

fail() {
  printf '[workspace] ERROR: %s\n' "$1" >&2
  exit 1
}

pass() {
  printf '[workspace] PASS: %s\n' "$1"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
}

register_container() {
  register_name=$1
  case "${register_name}" in
    nginx-uix-"${RUN_ID}"-*) ;;
    *) fail 'container name is outside the owned run prefix' ;;
  esac
  case " ${OWN_CONTAINERS} " in
    *" ${register_name} "*) return 0 ;;
  esac
  docker container inspect "${register_name}" >/dev/null 2>&1 &&
    fail 'owned container name already exists'
  OWN_CONTAINERS="${OWN_CONTAINERS} ${register_name}"
}

register_volume() {
  register_name=$1
  case "${register_name}" in
    nginx-uix-"${RUN_ID}"-*) ;;
    *) fail 'volume name is outside the owned run prefix' ;;
  esac
  docker volume inspect "${register_name}" >/dev/null 2>&1 &&
    fail 'owned volume name already exists'
  OWN_VOLUMES="${OWN_VOLUMES} ${register_name}"
}

register_image() {
  register_name=$1
  case "${register_name}" in
    nginx-uix-playwright-"${RUN_ID}") ;;
    *) fail 'image name is outside the owned run prefix' ;;
  esac
  docker image inspect "${register_name}" >/dev/null 2>&1 &&
    fail 'owned image name already exists'
  OWN_IMAGES="${OWN_IMAGES} ${register_name}"
}

cleanup() {
  cleanup_rc=$?
  trap - EXIT HUP INT TERM
  if [ -n "${ACTIVE_WATCHDOG_PID}" ]; then
    kill "${ACTIVE_WATCHDOG_PID}" >/dev/null 2>&1 || true
    wait "${ACTIVE_WATCHDOG_PID}" >/dev/null 2>&1 || true
  fi
  if [ -n "${ACTIVE_COMMAND_PID}" ]; then
    kill -TERM "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
    wait "${ACTIVE_COMMAND_PID}" >/dev/null 2>&1 || true
  fi
  if command -v docker >/dev/null 2>&1; then
    for cleanup_container in ${OWN_CONTAINERS}; do
      docker rm --force "${cleanup_container}" >/dev/null 2>&1 || true
    done
    for cleanup_volume in ${OWN_VOLUMES}; do
      docker volume rm "${cleanup_volume}" >/dev/null 2>&1 || true
    done
    for cleanup_image in ${OWN_IMAGES}; do
      docker image rm "${cleanup_image}" >/dev/null 2>&1 || true
    done
    if [ "${NETWORK_CREATED}" = 1 ]; then
      docker network rm "${NETWORK}" >/dev/null 2>&1 || true
    fi
  fi
  if [ "${BUILDX_CACHE_LOCK_OWNED:-0}" = 1 ]; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
  fi
  if [ "${WORK_DIR_CREATED}" = 1 ]; then
    case "${WORK_DIR}" in
      "${TMPDIR:-/tmp}"/nginx-uix-workspace-*) rm -rf "${WORK_DIR}" ;;
    esac
  fi
  exit "${cleanup_rc}"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

run_bounded() {
  bounded_seconds=$1
  shift
  "$@" &
  bounded_pid=$!
  ACTIVE_COMMAND_PID=${bounded_pid}
  (
    sleep "${bounded_seconds}"
    kill -TERM "${bounded_pid}" >/dev/null 2>&1 || exit 0
    sleep 5
    kill -KILL "${bounded_pid}" >/dev/null 2>&1 || true
  ) &
  bounded_watchdog=$!
  ACTIVE_WATCHDOG_PID=${bounded_watchdog}
  if wait "${bounded_pid}"; then
    bounded_rc=0
  else
    bounded_rc=$?
  fi
  kill "${bounded_watchdog}" >/dev/null 2>&1 || true
  wait "${bounded_watchdog}" >/dev/null 2>&1 || true
  ACTIVE_COMMAND_PID=
  ACTIVE_WATCHDOG_PID=
  return "${bounded_rc}"
}

digest_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

assert_safe_artifact() {
  artifact_path=$1
  [ -f "${artifact_path}" ] || return 0
  if grep -F "${PRIVATE_MARKER}" "${artifact_path}" >/dev/null 2>&1; then
    fail 'private fixture marker leaked into a response or bounded log'
  fi
  if grep -F 'BEGIN INVALID E2E PRIVATE KEY' "${artifact_path}" >/dev/null 2>&1; then
    fail 'private fixture signature leaked into a response or bounded log'
  fi
  if grep -F "${ADMIN_PASSWORD}" "${artifact_path}" >/dev/null 2>&1; then
    fail 'administrator password leaked into a response or bounded log'
  fi
  if grep -Eiq 'set-cookie:|nginx_uix_session=' "${artifact_path}"; then
    case "${artifact_path}" in
      *.headers | *.cookie) ;;
      *) fail 'session material leaked into a non-cookie evidence artifact' ;;
    esac
  fi
}

capture_logs() {
  capture_container=$1
  capture_output=$2
  run_bounded 15 docker logs --tail 200 "${capture_container}" >"${capture_output}" 2>&1 || true
  assert_safe_artifact "${capture_output}"
  if grep -E 'workspace-fixture\.example\.test|nginx-uix workspace fixture|workspace-draft-edit' \
    "${capture_output}" >/dev/null 2>&1; then
    fail 'configuration content leaked into bounded container logs'
  fi
}

wait_ready() {
  wait_container=$1
  wait_url=$2
  wait_limit=$3
  wait_count=0
  while [ "${wait_count}" -lt "${wait_limit}" ]; do
    if [ "$(docker inspect --format '{{.State.Running}}' "${wait_container}" 2>/dev/null || true)" != true ]; then
      return 1
    fi
    wait_code=$(curl --silent --connect-timeout 1 --max-time 2 \
      --output /dev/null --write-out '%{http_code}' "${wait_url}/health/ready" || true)
    [ "${wait_code}" = 200 ] && return 0
    wait_count=$((wait_count + 1))
    sleep 1
  done
  return 1
}

container_url() {
  container_port=$(docker port "$1" 9000/tcp | sed -n 's/^127\.0\.0\.1:\([0-9][0-9]*\)$/\1/p')
  case "${container_port}" in
    ''|*[!0-9]*) fail 'container UI port was not mapped to loopback' ;;
  esac
  printf 'http://127.0.0.1:%s\n' "${container_port}"
}

remove_container() {
  docker rm --force "$1" >/dev/null 2>&1 || true
}

start_main_container() {
  start_image=$1
  remove_container "${MAIN_CONTAINER}"
  run_bounded 30 docker run --detach \
    --name "${MAIN_CONTAINER}" \
    --label "${LABEL}" \
    --network "${NETWORK}" \
    --publish '127.0.0.1::9000' \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/etc/nginx" \
    --mount "type=volume,src=${DATA_VOLUME},dst=/var/lib/nginx-uix" \
    --mount "type=bind,src=${WORK_DIR}/admin-password,dst=/run/secrets/nginx-uix-admin,readonly" \
    --env "NGINX_UIX_ADMIN_USERNAME=${ADMIN_USERNAME}" \
    --env 'NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin' \
    "${start_image}" >/dev/null
  BASE_URL=$(container_url "${MAIN_CONTAINER}")
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 90 || {
    capture_logs "${MAIN_CONTAINER}" "${WORK_DIR}/main-start.log"
    fail 'application container did not become ready'
  }
}

start_failure_container() {
  failure_name=$1
  failure_data_mount=$2
  shift 2
  remove_container "${failure_name}"
  set -- docker run --detach \
    --name "${failure_name}" \
    --label "${LABEL}" \
    --network "${NETWORK}" \
    --publish '127.0.0.1::9000' \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/etc/nginx,readonly" \
    --mount "${failure_data_mount}" \
    --mount "type=bind,src=${WORK_DIR}/admin-password,dst=/run/secrets/nginx-uix-admin,readonly" \
    --env "NGINX_UIX_ADMIN_USERNAME=${ADMIN_USERNAME}" \
    --env 'NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin' \
    "$@" "${IMAGE}"
  run_bounded 30 "$@" >/dev/null
}

fixture_digest() {
  fixture_output="${WORK_DIR}/fixture-checksums.$$.txt"
  helper_name="nginx-uix-${RUN_ID}-digest-$$"
  register_container "${helper_name}"
  run_bounded 30 docker run --rm --name "${helper_name}" --entrypoint /bin/sh \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/fixture,readonly" \
    "${IMAGE}" -eu -c '
      cd /fixture
      sha256sum nginx.conf conf.d/site.conf conf.d/cycle-a.conf conf.d/cycle-b.conf private/server.key
    ' >"${fixture_output}" || fail 'could not checksum the fixed production fixture'
  digest_file "${fixture_output}"
}

restore_site_fixture() {
  helper_name="nginx-uix-${RUN_ID}-restore-$$"
  register_container "${helper_name}"
  run_bounded 30 docker run --rm --name "${helper_name}" --entrypoint /bin/sh \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/target" \
    --mount "type=bind,src=${FIXTURE_ROOT},dst=/source,readonly" \
    "${IMAGE}" -eu -c 'cp /source/conf.d/site.conf /target/conf.d/site.conf; chmod 0644 /target/conf.d/site.conf' >/dev/null ||
    fail 'could not restore the fixed production site fixture'
}

seed_fixture_volume() {
  helper_name="nginx-uix-${RUN_ID}-seed"
  register_container "${helper_name}"
  run_bounded 30 docker run --rm --name "${helper_name}" --entrypoint /bin/sh \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/target" \
    --mount "type=bind,src=${FIXTURE_ROOT},dst=/source,readonly" \
    "${IMAGE}" -eu -c '
      test -z "$(find /target -mindepth 1 -print -quit)"
      cp -R /source/. /target/
      chmod 0644 /target/nginx.conf /target/conf.d/*.conf
      chmod 0600 /target/private/server.key
      ln -s site.conf /target/conf.d/internal-link.conf
      ln -s /outside/nginx-uix-external.conf /target/conf.d/external-link.conf
      ln -s missing.conf /target/conf.d/broken-link.conf
      mkfifo /target/conf.d/stream.fifo
    ' >/dev/null || fail 'could not seed the owned production fixture volume'
}

ensure_v01_image() {
  case "${V01_SOURCE_REF}" in
    ''|-*|*'..'*|*[!A-Za-z0-9._/-]*) fail 'V01_SOURCE_REF is unsafe' ;;
  esac
  old_version=$(git show "${V01_SOURCE_REF}:VERSION" 2>/dev/null | tr -d '\r\n') ||
    fail 'could not read the prior-version fixture VERSION'
  [ "${old_version}" = 0.1.0 ] || fail 'prior-version source is not exactly v0.1.0'
  old_commit=$(git rev-parse --verify "${V01_SOURCE_REF}^{commit}")
  old_time=$(git show -s --format=%cI "${old_commit}")
  if docker image inspect "${V01_IMAGE}" >/dev/null 2>&1; then
    old_identity=$(docker image inspect --format \
      '{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{.Os}}/{{.Architecture}}' \
      "${V01_IMAGE}")
    [ "${old_identity}" = "0.1.0|${old_commit}|${PLATFORM}" ] ||
      fail 'existing v0.1 fixture image has the wrong version/revision/platform identity'
    return 0
  fi
  old_context="${WORK_DIR}/v0.1-context"
  mkdir "${old_context}"
  git archive "${old_commit}" | tar -x -C "${old_context}"

  acquire_buildx_cache_lock "${REPOSITORY_ROOT}/.tmp" 60 || fail 'could not lock cache for v0.1 fixture build'
  append_cache_from_args "${REPOSITORY_ROOT}/.tmp/buildx-cache" "${BUILDX_CACHE_SEED_DIR}" ||
    fail 'v0.1 fixture cache imports are invalid'
  set -- docker buildx build --progress=plain --platform "${PLATFORM}" \
    --file "${old_context}/deploy/docker/Dockerfile" \
    --tag "${V01_IMAGE}" --load \
    --label 'org.opencontainers.image.version=0.1.0' \
    --label "org.opencontainers.image.revision=${old_commit}" \
    --build-arg 'VERSION=0.1.0' \
    --build-arg "COMMIT=${old_commit}" \
    --build-arg "BUILD_TIME=${old_time}"
  [ -z "${BUILDX_CACHE_FROM_CURRENT}" ] || set -- "$@" --cache-from "${BUILDX_CACHE_FROM_CURRENT}"
  [ -z "${BUILDX_CACHE_FROM_SEED}" ] || set -- "$@" --cache-from "${BUILDX_CACHE_FROM_SEED}"
  set -- "$@" "${old_context}"
  if ! run_bounded 900 "$@" >"${WORK_DIR}/v0.1-build.log" 2>&1; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
    assert_safe_artifact "${WORK_DIR}/v0.1-build.log"
    fail 'v0.1 fixture image build failed'
  fi
  release_buildx_cache_lock || fail 'could not release v0.1 fixture cache lock'
  assert_safe_artifact "${WORK_DIR}/v0.1-build.log"
  old_identity=$(docker image inspect --format \
    '{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{.Os}}/{{.Architecture}}' \
    "${V01_IMAGE}")
  [ "${old_identity}" = "0.1.0|${old_commit}|${PLATFORM}" ] ||
    fail 'built v0.1 fixture image has the wrong version/revision/platform identity'
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
  assert_safe_artifact "${WORK_DIR}/${login_prefix}.response.json"
}

api_get() {
  get_url=$1
  get_output=$2
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 20 \
    --output "${get_output}" --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" "${get_url}")
  [ "${HTTP_CODE}" = 200 ] || fail 'authenticated GET did not return HTTP 200'
  assert_safe_artifact "${get_output}"
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
  [ "${HTTP_CODE}" = "${mutation_expected}" ] || fail "mutation ${mutation_prefix} returned an unexpected status"
  RESPONSE_ETAG=$(sed -n 's/^[Ee][Tt][Aa][Gg]:[[:space:]]*//p' \
    "${WORK_DIR}/${mutation_prefix}.headers" | tr -d '\r' | sed -n '1p')
  assert_safe_artifact "${WORK_DIR}/${mutation_prefix}.response.json"
}

status_signature() {
  status_output=$1
  api_get "${BASE_URL}/api/v1/system/status" "${status_output}"
  jq -ecS '
    select(.components.ui == "healthy" and .components.agent == "healthy" and .components.nginx == "running") |
    select((.master.pid | type) == "number" and (.workers | length) > 0) |
    {master:.master.pid,workers:(.workers | map(.pid) | sort),components:.components}
  ' "${status_output}"
}

assert_production_invariant() {
  invariant_workspace=$1
  invariant_production=$2
  invariant_context=$3
  api_get "${BASE_URL}/api/v1/config/workspaces/${invariant_workspace}" \
    "${WORK_DIR}/${invariant_context}.workspace.json"
  [ "$(jq -er '.production_digest' "${WORK_DIR}/${invariant_context}.workspace.json")" = "${invariant_production}" ] ||
    fail 'workspace production digest changed after a draft mutation'
  current_status=$(status_signature "${WORK_DIR}/${invariant_context}.status.json")
  [ "${current_status}" = "${BASELINE_STATUS}" ] || fail 'Nginx process/health identity changed after a draft mutation'
  ready_code=$(curl --silent --connect-timeout 1 --max-time 3 --output /dev/null \
    --write-out '%{http_code}' "${BASE_URL}/health/ready" || true)
  [ "${ready_code}" = 200 ] || fail 'application readiness changed after a draft mutation'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'production fixture bytes changed after a draft mutation'
}

workspace_etag_from_body() {
  jq -er '.draft_etag | select(test("^\\\"draft-v1:[0-9a-f]{64}\\\"$"))' "$1"
}

assert_no_preparing_name() {
  absent_name=$1
  output=$2
  api_get "${BASE_URL}/api/v1/config/workspaces" "${output}"
  jq -e --arg name "${absent_name}" \
    'all(.workspaces[]; .name != $name or .state != "preparing")' "${output}" >/dev/null ||
    fail 'failed operation left a preparing workspace'
}

api_get_expected() {
  expected_url=$1
  expected_status=$2
  expected_output=$3
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 20 \
    --output "${expected_output}" --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" "${expected_url}")
  [ "${HTTP_CODE}" = "${expected_status}" ] || fail 'authenticated GET returned an unexpected status'
  assert_safe_artifact "${expected_output}"
}

groups_etag_from_body() {
  jq -er '.groups_etag | select(test("^\\\"groups-v1:[0-9a-f]{64}\\\"$"))' "$1"
}

assert_error_code() {
  error_file=$1
  error_code=$2
  jq -e --arg code "${error_code}" '.error.code == $code' "${error_file}" >/dev/null ||
    fail "API error did not use ${error_code}"
}

stop_main_container() {
  stop_context=$1
  stop_started=$(date +%s)
  run_bounded 25 docker stop --time 15 "${MAIN_CONTAINER}" >/dev/null ||
    fail "${stop_context}: container did not stop cleanly"
  stop_elapsed=$(($(date +%s) - stop_started))
  [ "${stop_elapsed}" -le 20 ] || fail "${stop_context}: graceful stop exceeded its bound"
  [ "$(docker inspect --format '{{.State.ExitCode}}' "${MAIN_CONTAINER}")" = 0 ] ||
    fail "${stop_context}: container exited unsuccessfully"
}

copy_database() {
  database_directory=$1
  mkdir "${database_directory}"
  run_bounded 30 docker cp "${MAIN_CONTAINER}:/var/lib/nginx-uix/." "${database_directory}" >/dev/null ||
    fail 'could not copy the stopped SQLite data volume'
  database_file="${database_directory}/nginx-uix.db"
  [ -f "${database_file}" ] || fail 'SQLite database was absent from the data volume'
  [ "$(sqlite3 "${database_file}" 'PRAGMA integrity_check;')" = ok ] ||
    fail 'SQLite integrity_check failed'
}

assert_upgrade_database() {
  database_file=$1
  migrations=$(sqlite3 "${database_file}" \
    'SELECT COALESCE(group_concat(version, ","), "") FROM (SELECT version FROM schema_migrations ORDER BY version);')
  [ "${migrations}" = '1,2' ] || fail 'upgraded database does not contain exactly migrations [1,2]'
  [ "$(sqlite3 "${database_file}" 'SELECT COUNT(*) FROM users;')" -ge 1 ] ||
    fail 'upgraded database lost the bootstrap user'
  [ "$(sqlite3 "${database_file}" 'SELECT COUNT(*) FROM sessions;')" -ge 1 ] ||
    fail 'upgraded database lost the authenticated session'
}

assert_workspace_root_permissions() {
  permission_record=$(docker exec "${MAIN_CONTAINER}" stat -c '%u:%g:%a' /var/lib/nginx-uix/workspaces)
  [ "${permission_record}" = '10001:10001:700' ] ||
    fail 'workspace root does not have UID/GID 10001 and mode 0700'
}

verify_upgrade() {
  log 'starting the v0.1.0 upgrade fixture'
  start_main_container "${V01_IMAGE}"
  login v01-login
  api_get "${BASE_URL}/api/v1/system/status" "${WORK_DIR}/v01-status.json"
  api_get "${BASE_URL}/api/v1/nginx/effective-config" "${WORK_DIR}/v01-effective.json"
  jq -e '.components.ui == "healthy" and .components.agent == "healthy" and .components.nginx == "running"' \
    "${WORK_DIR}/v01-status.json" >/dev/null || fail 'v0.1 status was not healthy'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'v0.1 startup overwrote preexisting configuration bytes'
  stop_main_container 'v0.1 upgrade fixture'

  log 'starting v0.2.1 on the same configuration and data volumes'
  start_main_container "${IMAGE}"
  api_get "${BASE_URL}/api/v1/auth/session" "${WORK_DIR}/upgraded-session.json"
  jq -e --arg username "${ADMIN_USERNAME}" '.user.username == $username and (.csrf_token | type == "string" and length > 20)' \
    "${WORK_DIR}/upgraded-session.json" >/dev/null || fail 'v0.1 user/session did not survive the v0.2 upgrade'
  api_get "${BASE_URL}/api/v1/system/status" "${WORK_DIR}/upgraded-status.json"
  api_get "${BASE_URL}/api/v1/nginx/effective-config" "${WORK_DIR}/upgraded-effective.json"
  assert_workspace_root_permissions
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'v0.2 upgrade overwrote preexisting configuration bytes'

  stop_main_container 'v0.2 migration inspection'
  copy_database "${WORK_DIR}/upgraded-database"
  assert_upgrade_database "${WORK_DIR}/upgraded-database/nginx-uix.db"
  start_main_container "${IMAGE}"
  login upgraded-login
  pass 'v0.1.0 data/session/configuration upgraded to migrations [1,2] with exact bytes and permissions preserved'
}

create_workspace() {
  create_name=$1
  create_prefix=$2
  jq -n --arg name "${create_name}" '{name:$name}' >"${WORK_DIR}/${create_prefix}.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces" \
    "${WORK_DIR}/${create_prefix}.request.json" '' 201 "${create_prefix}"
  CREATE_WORKSPACE_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/${create_prefix}.response.json")
  CREATE_WORKSPACE_ETAG=$(workspace_etag_from_body "${WORK_DIR}/${create_prefix}.response.json")
  CREATE_PRODUCTION_DIGEST=$(jq -er '.production_digest | select(test("^[0-9a-f]{64}$"))' \
    "${WORK_DIR}/${create_prefix}.response.json")
  [ "${RESPONSE_ETAG}" = "${CREATE_WORKSPACE_ETAG}" ] || fail 'workspace create header/body ETags differ'
  jq -e '.state == "ready"' "${WORK_DIR}/${create_prefix}.response.json" >/dev/null ||
    fail 'created workspace was not ready'
}

delete_workspace() {
  delete_id=$1
  delete_name=$2
  delete_etag=$3
  delete_prefix=$4
  jq -n --arg name "${delete_name}" '{confirm_name:$name}' >"${WORK_DIR}/${delete_prefix}.request.json"
  api_mutation DELETE "${BASE_URL}/api/v1/config/workspaces/${delete_id}" \
    "${WORK_DIR}/${delete_prefix}.request.json" "${delete_etag}" 204 "${delete_prefix}"
}

accept_workspace_mutation() {
  mutation_prefix=$1
  mutation_context=$2
  WORKSPACE_ETAG=$(workspace_etag_from_body "${WORK_DIR}/${mutation_prefix}.response.json")
  [ "${RESPONSE_ETAG}" = "${WORKSPACE_ETAG}" ] || fail 'workspace mutation header/body ETags differ'
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" "${mutation_context}"
}

assert_tree_contract() {
  tree_file=$1
  jq -e '
    def node($path): first(.entries[] | select(.path == $path));
    (node("private/server.key") |
      .entry_type == "regular" and .read_only == true and .managed == false and
      .status_reason_code == "sensitive_material" and
      (has("content_digest") | not) and (has("diff_status") | not)) and
    (node("conf.d/internal-link.conf") |
      .entry_type == "symlink" and .read_only == true and .status_reason_code == "symlink_internal") and
    (node("conf.d/external-link.conf") |
      .entry_type == "symlink" and .read_only == true and .status_reason_code == "symlink_external") and
    (node("conf.d/broken-link.conf") |
      .entry_type == "symlink" and .read_only == true and .status_reason_code == "symlink_unavailable") and
    (node("conf.d/stream.fifo") |
      .entry_type == "special" and .read_only == true and .status_reason_code == "special") and
    all(.entries[]; (.path | startswith("control/")) | not)
  ' "${tree_file}" >/dev/null || fail 'workspace tree did not preserve sensitive/symlink/special safety classifications'
}

exercise_group_crud() {
  api_get "${BASE_URL}/api/v1/config/groups" "${WORK_DIR}/groups-initial.json"
  GROUPS_ETAG=$(groups_etag_from_body "${WORK_DIR}/groups-initial.json")

  jq -n --arg name "temporary-${RUN_RANDOM}" \
    '{name:$name,sort_order:10,members:["conf.d/site.conf"]}' >"${WORK_DIR}/group-create.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/groups" "${WORK_DIR}/group-create.request.json" \
    "${GROUPS_ETAG}" 201 group-create
  GROUPS_ETAG=$(groups_etag_from_body "${WORK_DIR}/group-create.response.json")
  [ "${RESPONSE_ETAG}" = "${GROUPS_ETAG}" ] || fail 'group create header/body ETags differ'
  TEMP_GROUP_ID=$(jq -er --arg name "temporary-${RUN_RANDOM}" \
    '.groups[] | select(.name == $name) | .id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/group-create.response.json")
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" group-create-invariant

  jq -n --arg name "temporary-updated-${RUN_RANDOM}" \
    '{name:$name,sort_order:11,members:["conf.d/site.conf","conf.d/renamed.conf"]}' \
    >"${WORK_DIR}/group-update.request.json"
  api_mutation PUT "${BASE_URL}/api/v1/config/groups/${TEMP_GROUP_ID}" \
    "${WORK_DIR}/group-update.request.json" "${GROUPS_ETAG}" 200 group-update
  GROUPS_ETAG=$(groups_etag_from_body "${WORK_DIR}/group-update.response.json")
  [ "${RESPONSE_ETAG}" = "${GROUPS_ETAG}" ] || fail 'group update header/body ETags differ'
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" group-update-invariant

  jq -n --arg name "temporary-updated-${RUN_RANDOM}" '{confirm_name:$name}' \
    >"${WORK_DIR}/group-delete.request.json"
  api_mutation DELETE "${BASE_URL}/api/v1/config/groups/${TEMP_GROUP_ID}" \
    "${WORK_DIR}/group-delete.request.json" "${GROUPS_ETAG}" 200 group-delete
  GROUPS_ETAG=$(groups_etag_from_body "${WORK_DIR}/group-delete.response.json")
  [ "${RESPONSE_ETAG}" = "${GROUPS_ETAG}" ] || fail 'group delete header/body ETags differ'
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" group-delete-invariant

  PERSISTED_GROUP_NAME="persisted-${RUN_RANDOM}"
  jq -n --arg name "${PERSISTED_GROUP_NAME}" \
    '{name:$name,sort_order:20,members:["conf.d/site.conf","conf.d/renamed.conf"]}' \
    >"${WORK_DIR}/group-persist.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/groups" "${WORK_DIR}/group-persist.request.json" \
    "${GROUPS_ETAG}" 201 group-persist
  PERSISTED_GROUP_ETAG=$(groups_etag_from_body "${WORK_DIR}/group-persist.response.json")
  [ "${RESPONSE_ETAG}" = "${PERSISTED_GROUP_ETAG}" ] || fail 'persisted group header/body ETags differ'
  PERSISTED_GROUP_ID=$(jq -er --arg name "${PERSISTED_GROUP_NAME}" \
    '.groups[] | select(.name == $name) | .id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/group-persist.response.json")
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" group-persist-invariant
}

exercise_workspace_crud() {
  log 'exercising real workspace HTTP CRUD and production invariants'
  BASELINE_STATUS=$(status_signature "${WORK_DIR}/baseline-status.json")
  create_workspace "${WORKSPACE_NAME}" workspace-create
  WORKSPACE_ID=${CREATE_WORKSPACE_ID}
  WORKSPACE_ETAG=${CREATE_WORKSPACE_ETAG}
  PRODUCTION_DIGEST=${CREATE_PRODUCTION_DIGEST}
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" workspace-create-invariant

  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files" "${WORK_DIR}/workspace-tree.json"
  [ "$(workspace_etag_from_body "${WORK_DIR}/workspace-tree.json")" = "${WORKSPACE_ETAG}" ] ||
    fail 'tree ETag differs from the workspace ETag'
  assert_tree_contract "${WORK_DIR}/workspace-tree.json"
  api_get_expected "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files?path=private%2Fserver.key" \
    422 "${WORK_DIR}/sensitive-read.response.json"
  assert_error_code "${WORK_DIR}/sensitive-read.response.json" CONFIG_ENTRY_NOT_MANAGED

  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files?path=conf.d%2Fsite.conf" \
    "${WORK_DIR}/site-read.json"
  [ "$(workspace_etag_from_body "${WORK_DIR}/site-read.json")" = "${WORKSPACE_ETAG}" ] ||
    fail 'file ETag differs from the workspace ETag'
  jq -e '.path == "conf.d/site.conf" and (.content_digest | test("^[0-9a-f]{64}$"))' \
    "${WORK_DIR}/site-read.json" >/dev/null || fail 'managed file response is malformed'

  jq '{content:(.content + "\n# workspace-draft-edit\n")}' "${WORK_DIR}/site-read.json" \
    >"${WORK_DIR}/file-replace.request.json"
  api_mutation PUT "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files?path=conf.d%2Fsite.conf" \
    "${WORK_DIR}/file-replace.request.json" "${WORKSPACE_ETAG}" 200 file-replace
  accept_workspace_mutation file-replace file-replace-invariant

  jq -n '{path:"conf.d/new.conf",content:"server { listen 18081; }\n"}' \
    >"${WORK_DIR}/file-create.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files" \
    "${WORK_DIR}/file-create.request.json" "${WORKSPACE_ETAG}" 201 file-create
  accept_workspace_mutation file-create file-create-invariant

  jq -n '{source_path:"conf.d/new.conf",destination_path:"conf.d/copy.conf"}' \
    >"${WORK_DIR}/file-copy.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files/copies" \
    "${WORK_DIR}/file-copy.request.json" "${WORKSPACE_ETAG}" 201 file-copy
  accept_workspace_mutation file-copy file-copy-invariant

  jq -n '{destination_path:"conf.d/renamed.conf"}' >"${WORK_DIR}/file-rename.request.json"
  api_mutation PATCH "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files?path=conf.d%2Fcopy.conf" \
    "${WORK_DIR}/file-rename.request.json" "${WORKSPACE_ETAG}" 200 file-rename
  accept_workspace_mutation file-rename file-rename-invariant

  jq -n '{confirm_path:"conf.d/new.conf"}' >"${WORK_DIR}/file-delete.request.json"
  api_mutation DELETE "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files?path=conf.d%2Fnew.conf" \
    "${WORK_DIR}/file-delete.request.json" "${WORKSPACE_ETAG}" 200 file-delete
  accept_workspace_mutation file-delete file-delete-invariant

  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files/search?query=workspace-draft-edit" \
    "${WORK_DIR}/workspace-search.json"
  jq -e '.complete == true and any(.matches[]; .path == "conf.d/site.conf")' \
    "${WORK_DIR}/workspace-search.json" >/dev/null || fail 'workspace search did not find the draft edit'
  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/diff" "${WORK_DIR}/workspace-diff.json"
  jq -e '
    (.complete == true) and
    any(.files[]; .path == "conf.d/site.conf" and .status == "modified") and
    any(.files[]; .path == "conf.d/renamed.conf" and .status == "created") and
    all(.files[]; (.path | startswith("control/")) | not)
  ' "${WORK_DIR}/workspace-diff.json" >/dev/null || fail 'workspace diff did not describe the persisted draft changes'

  exercise_group_crud
  PERSISTED_WORKSPACE_ETAG=${WORKSPACE_ETAG}
  pass 'real HTTP workspace/file/search/diff/group CRUD preserved production digest, processes, health, and fixture bytes'
}

assert_workspace_disk_separation() {
  separation=$(docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c \
    "stat -c '%i:%a:%u:%g' '/var/lib/nginx-uix/workspaces/${WORKSPACE_ID}/base/conf.d/site.conf'; stat -c '%i:%a:%u:%g' '/var/lib/nginx-uix/workspaces/${WORKSPACE_ID}/draft/conf.d/site.conf'")
  base_record=$(printf '%s\n' "${separation}" | sed -n '1p')
  draft_record=$(printf '%s\n' "${separation}" | sed -n '2p')
  base_inode=${base_record%%:*}
  draft_inode=${draft_record%%:*}
  [ -n "${base_inode}" ] && [ "${base_inode}" != "${draft_inode}" ] ||
    fail 'workspace base and draft unexpectedly share an inode'
  [ "${base_record#*:}" = '400:10001:10001' ] || fail 'workspace base file is writable or has the wrong owner'
  [ "${draft_record#*:}" = '600:10001:10001' ] || fail 'workspace draft file has the wrong mode or owner'
}

verify_workspace_recreation() {
  log 'recreating v0.2.1 with the same configuration and data volumes'
  stop_main_container 'workspace persistence recreation'
  start_main_container "${IMAGE}"
  login recreate-login
  RECREATED_STATUS=$(status_signature "${WORK_DIR}/recreated-status.json")
  [ -n "${RECREATED_STATUS}" ] || fail 'recreated container status was not healthy'

  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}" "${WORK_DIR}/recreated-workspace.json"
  [ "$(workspace_etag_from_body "${WORK_DIR}/recreated-workspace.json")" = "${PERSISTED_WORKSPACE_ETAG}" ] ||
    fail 'workspace ETag did not survive container recreation'
  jq -e --arg name "${WORKSPACE_NAME}" '.name == $name and .state == "ready"' \
    "${WORK_DIR}/recreated-workspace.json" >/dev/null || fail 'workspace metadata did not survive container recreation'

  api_get "${BASE_URL}/api/v1/config/groups?workspace_id=${WORKSPACE_ID}" "${WORK_DIR}/recreated-groups.json"
  [ "$(groups_etag_from_body "${WORK_DIR}/recreated-groups.json")" = "${PERSISTED_GROUP_ETAG}" ] ||
    fail 'group collection ETag did not survive container recreation'
  jq -e --arg id "${PERSISTED_GROUP_ID}" --arg name "${PERSISTED_GROUP_NAME}" \
    'any(.groups[]; .id == $id and .name == $name and .members == ["conf.d/renamed.conf","conf.d/site.conf"])' \
    "${WORK_DIR}/recreated-groups.json" >/dev/null || fail 'persisted group did not survive container recreation'

  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files" "${WORK_DIR}/recreated-tree.json"
  assert_tree_contract "${WORK_DIR}/recreated-tree.json"
  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/diff" "${WORK_DIR}/recreated-diff.json"
  jq -e '
    any(.files[]; .path == "conf.d/site.conf" and .status == "modified") and
    any(.files[]; .path == "conf.d/renamed.conf" and .status == "created") and
    all(.files[]; (.path | startswith("control/")) | not)
  ' "${WORK_DIR}/recreated-diff.json" >/dev/null || fail 'workspace diff did not survive container recreation'
  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}/files/search?query=journal.json" \
    "${WORK_DIR}/control-search.json"
  jq -e '.matches == []' "${WORK_DIR}/control-search.json" >/dev/null ||
    fail 'workspace control files appeared in search results'
  assert_workspace_disk_separation

  BASELINE_STATUS=${RECREATED_STATUS}
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" recreate-invariant

  jq -n --arg name "${PERSISTED_GROUP_NAME}" '{confirm_name:$name}' \
    >"${WORK_DIR}/persisted-group-delete.request.json"
  api_mutation DELETE "${BASE_URL}/api/v1/config/groups/${PERSISTED_GROUP_ID}" \
    "${WORK_DIR}/persisted-group-delete.request.json" "${PERSISTED_GROUP_ETAG}" 200 persisted-group-delete
  PERSISTED_GROUP_ETAG=$(groups_etag_from_body "${WORK_DIR}/persisted-group-delete.response.json")
  [ "${RESPONSE_ETAG}" = "${PERSISTED_GROUP_ETAG}" ] || fail 'persisted group delete header/body ETags differ'
  assert_production_invariant "${WORKSPACE_ID}" "${PRODUCTION_DIGEST}" persisted-group-delete-invariant

  delete_workspace "${WORKSPACE_ID}" "${WORKSPACE_NAME}" "${PERSISTED_WORKSPACE_ETAG}" workspace-delete
  api_get "${BASE_URL}/api/v1/config/workspaces" "${WORK_DIR}/workspaces-after-delete.json"
  jq -e --arg id "${WORKSPACE_ID}" 'all(.workspaces[]; .id != $id)' \
    "${WORK_DIR}/workspaces-after-delete.json" >/dev/null || fail 'named-deleted workspace remains listed'
  [ "$(status_signature "${WORK_DIR}/status-after-delete.json")" = "${BASELINE_STATUS}" ] ||
    fail 'named workspace delete changed Nginx process/health identity'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'named workspace delete changed production fixture bytes'
  pass 'workspace, group, ETags, diff and file separation survived recreation before bounded named deletion'
}

verify_agent_unavailable_create() {
  failure_name="agent-unavailable-${RUN_RANDOM}"
  run_bounded 10 docker exec "${MAIN_CONTAINER}" \
    /command/s6-svc -d -wD -T 5000 /run/service/nginx-uix-agent \
    >"${WORK_DIR}/agent-down.log" 2>&1 || fail 'could not stop Agent through s6'
  assert_safe_artifact "${WORK_DIR}/agent-down.log"
  jq -n --arg name "${failure_name}" '{name:$name}' >"${WORK_DIR}/agent-create.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces" \
    "${WORK_DIR}/agent-create.request.json" '' 503 agent-create
  assert_error_code "${WORK_DIR}/agent-create.response.json" AGENT_UNAVAILABLE
  run_bounded 15 docker exec "${MAIN_CONTAINER}" \
    /command/s6-svc -u -wU -T 10000 /run/service/nginx-uix-agent \
    >"${WORK_DIR}/agent-up.log" 2>&1 || fail 'could not restore Agent through s6'
  assert_safe_artifact "${WORK_DIR}/agent-up.log"
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 30 || fail 'readiness did not recover after Agent restart'
  assert_no_preparing_name "${failure_name}" "${WORK_DIR}/agent-create-list.json"
  [ "$(status_signature "${WORK_DIR}/agent-recovered-status.json")" = "${BASELINE_STATUS}" ] ||
    fail 'Agent failure/recovery changed Nginx process identity'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'Agent failure/recovery changed production fixture bytes'
  pass 'real-container Agent unavailability returns 503 and leaves no preparing workspace'
}

mutate_production_fixture() {
  helper_name="nginx-uix-${RUN_ID}-production-change"
  register_container "${helper_name}"
  run_bounded 30 docker run --rm --name "${helper_name}" --entrypoint /bin/sh \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/fixture" \
    "${IMAGE}" -eu -c 'printf "\n# bounded-production-change\n" >> /fixture/conf.d/site.conf' >/dev/null ||
    fail 'could not inject the bounded production-change fault'
}

verify_production_change_stale() {
  stale_name="stale-${RUN_RANDOM}"
  create_workspace "${stale_name}" stale-create
  stale_id=${CREATE_WORKSPACE_ID}
  stale_etag=${CREATE_WORKSPACE_ETAG}
  stale_production=${CREATE_PRODUCTION_DIGEST}
  [ "${stale_production}" = "${BASE_PRODUCTION_DIGEST}" ] || fail 'stale fixture started from an unexpected production digest'
  mutate_production_fixture
  [ "$(fixture_digest)" != "${FIXTURE_DIGEST}" ] || fail 'production-change fault did not alter the production fixture'
  jq -n '{content:"server { listen 18082; }\n"}' >"${WORK_DIR}/stale-replace.request.json"
  api_mutation PUT "${BASE_URL}/api/v1/config/workspaces/${stale_id}/files?path=conf.d%2Fsite.conf" \
    "${WORK_DIR}/stale-replace.request.json" "${stale_etag}" 409 stale-replace
  assert_error_code "${WORK_DIR}/stale-replace.response.json" CONFIG_WORKSPACE_STALE
  api_get "${BASE_URL}/api/v1/config/workspaces/${stale_id}" "${WORK_DIR}/stale-workspace.json"
  jq -e '.state == "stale" and .state_reason_code == "PRODUCTION_CHANGED"' \
    "${WORK_DIR}/stale-workspace.json" >/dev/null || fail 'production change did not fail closed to stale'
  stale_etag=$(workspace_etag_from_body "${WORK_DIR}/stale-workspace.json")
  restore_site_fixture
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'production fixture could not be restored byte-for-byte'
  delete_workspace "${stale_id}" "${stale_name}" "${stale_etag}" stale-delete
  [ "$(status_signature "${WORK_DIR}/stale-status.json")" = "${BASELINE_STATUS}" ] ||
    fail 'stale fault changed Nginx process/health identity'
  pass 'real-container production mutation fails draft writes closed to stale and supports named cleanup after exact restoration'
}

wait_ui_restart() {
  previous_pid=$1
  restart_count=0
  while [ "${restart_count}" -lt 30 ]; do
    current_pid=$(docker exec "${MAIN_CONTAINER}" /command/s6-svstat -o pid /run/service/nginx-uix 2>/dev/null || true)
    case "${current_pid}" in
      ''|*[!0-9]*) ;;
      *)
        if [ "${current_pid}" -gt 0 ] && [ "${current_pid}" != "${previous_pid}" ]; then
          wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 20 && return 0
        fi
        ;;
    esac
    restart_count=$((restart_count + 1))
    sleep 1
  done
  return 1
}

verify_generic_ui_restart() {
  old_ui_pid=$(run_bounded 5 docker exec "${MAIN_CONTAINER}" \
    /command/s6-svstat -o pid /run/service/nginx-uix) || fail 'could not read UI service PID'
  case "${old_ui_pid}" in ''|*[!0-9]*) fail 'UI service PID is malformed' ;; esac
  run_bounded 8 docker exec "${MAIN_CONTAINER}" /command/s6-svc -k /run/service/nginx-uix \
    >"${WORK_DIR}/ui-kill.log" 2>&1 || fail 'could not kill UI through s6'
  assert_safe_artifact "${WORK_DIR}/ui-kill.log"
  wait_ui_restart "${old_ui_pid}" || fail 's6 did not restore the UI within the bound'
  api_get "${BASE_URL}/api/v1/config/workspaces/${WORKSPACE_ID}" "${WORK_DIR}/after-ui-restart.json"
  jq -e '.state == "ready"' "${WORK_DIR}/after-ui-restart.json" >/dev/null ||
    fail 'generic UI restart changed a fully committed workspace from ready'
  [ "$(status_signature "${WORK_DIR}/ui-restart-status.json")" = "${BASELINE_STATUS}" ] ||
    fail 'generic UI restart replaced Nginx process identity'
  pass 'real-container generic UI crash is supervised without changing Nginx; phase-specific recovery is covered separately'
}

tamper_workspace_draft() {
  tamper_id=$1
  helper_name="nginx-uix-${RUN_ID}-tamper"
  register_container "${helper_name}"
  run_bounded 30 docker run --rm --name "${helper_name}" --user 0 --entrypoint /bin/sh \
    --mount "type=volume,src=${DATA_VOLUME},dst=/data" \
    "${IMAGE}" -eu -c \
    "printf 'tampered-draft\\n' > '/data/workspaces/${tamper_id}/draft/conf.d/site.conf'; chmod 0600 '/data/workspaces/${tamper_id}/draft/conf.d/site.conf'; chown 10001:10001 '/data/workspaces/${tamper_id}/draft/conf.d/site.conf'" >/dev/null ||
    fail 'could not inject the bounded draft-tamper fault'
}

verify_needs_attention_recovery() {
  attention_name="needs-attention-${RUN_RANDOM}"
  create_workspace "${attention_name}" attention-create
  attention_id=${CREATE_WORKSPACE_ID}
  stop_main_container 'needs-attention fault injection'
  tamper_workspace_draft "${attention_id}"
  start_main_container "${IMAGE}"
  login attention-login
  api_get "${BASE_URL}/api/v1/config/workspaces/${attention_id}" "${WORK_DIR}/attention-workspace.json"
  jq -e '.state == "needs_attention" and .state_reason_code == "DRAFT_DIGEST_MISMATCH"' \
    "${WORK_DIR}/attention-workspace.json" >/dev/null ||
    fail 'draft tamper did not reconcile to needs_attention'
  attention_etag=$(workspace_etag_from_body "${WORK_DIR}/attention-workspace.json")
  delete_workspace "${attention_id}" "${attention_name}" "${attention_etag}" attention-delete
  BASELINE_STATUS=$(status_signature "${WORK_DIR}/post-attention-status.json")
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'needs-attention recovery changed production fixture bytes'
  pass 'real-container draft tamper reconciles to needs_attention, never ready, and permits bounded named cleanup'
}

assert_failure_not_ready() {
  failure_container=$1
  failure_label=$2
  failure_url=$(container_url "${failure_container}")
  if wait_ready "${failure_container}" "${failure_url}" 12; then
    fail "${failure_label}: unsafe data root unexpectedly became ready"
  fi
  capture_logs "${failure_container}" "${WORK_DIR}/${failure_label}.log"
  remove_container "${failure_container}"
}

verify_data_root_failures() {
  start_failure_container "${READONLY_CONTAINER}" \
    "type=volume,src=${READONLY_DATA_VOLUME},dst=/var/lib/nginx-uix,readonly"
  assert_failure_not_ready "${READONLY_CONTAINER}" readonly-data
  start_failure_container "${SMALL_CONTAINER}" \
    'type=tmpfs,destination=/var/lib/nginx-uix,tmpfs-size=4096,tmpfs-mode=0700'
  assert_failure_not_ready "${SMALL_CONTAINER}" small-data
  pass 'real-container read-only and 4 KiB data roots fail closed without reporting readiness'
}

verify_deterministic_fault_evidence() {
  mkdir -p "${REPOSITORY_ROOT}/.tmp/go-cache"
  if ! run_bounded 240 env GOCACHE="${REPOSITORY_ROOT}/.tmp/go-cache" \
    go test ./internal/config \
    -run 'TestCreateWorkspaceCleansStagedTreeWhenProductionGrowsAfterPreflight|TestSnapshotToRejectsSourceChangeAndCleansStage|TestReconcileFileMutationCrashMatrix|TestFileMutationHonorsCancellationAndLockContention|TestScopedRootDoesNotTrustPriorLstat' \
    -count=1 >"${WORK_DIR}/deterministic-config-faults.log" 2>&1; then
    assert_safe_artifact "${WORK_DIR}/deterministic-config-faults.log"
    fail 'deterministic configuration fault tests failed'
  fi
  assert_safe_artifact "${WORK_DIR}/deterministic-config-faults.log"
  if ! run_bounded 180 env GOCACHE="${REPOSITORY_ROOT}/.tmp/go-cache" \
    go test ./internal/runtime -run 'TestConfigSnapshotCleansOnlyGeneratedStageAfterFaults' -count=1 \
    >"${WORK_DIR}/deterministic-snapshot-faults.log" 2>&1; then
    assert_safe_artifact "${WORK_DIR}/deterministic-snapshot-faults.log"
    fail 'deterministic Agent snapshot fault tests failed'
  fi
  assert_safe_artifact "${WORK_DIR}/deterministic-snapshot-faults.log"
  if ! run_bounded 120 env GOCACHE="${REPOSITORY_ROOT}/.tmp/go-cache" \
    go test ./internal/httpapi -run 'TestConfigErrorResponseMapsStableCodesAndStatuses' -count=1 \
    >"${WORK_DIR}/deterministic-http-error-map.log" 2>&1; then
    assert_safe_artifact "${WORK_DIR}/deterministic-http-error-map.log"
    fail 'deterministic HTTP configuration error mapping test failed'
  fi
  assert_safe_artifact "${WORK_DIR}/deterministic-http-error-map.log"
  pass 'supplemental deterministic Go evidence covers double-inventory change plus HTTP 409 CONFIG_SNAPSHOT_CHANGED mapping, transaction phases, cancellation, and symlink-swap TOCTOU; these are not claimed as real-container failpoints'
}

build_browser_test_image() {
  grep -Fx "FROM ${PLAYWRIGHT_IMAGE}" deploy/docker/Playwright.Dockerfile >/dev/null ||
    fail 'Playwright test Dockerfile does not use the required pinned image'
  register_image "${BROWSER_TEST_IMAGE}"
  if ! run_bounded 600 docker build --platform "${PLATFORM}" \
    --file deploy/docker/Playwright.Dockerfile \
    --label "${LABEL}" \
    --tag "${BROWSER_TEST_IMAGE}" . >"${WORK_DIR}/playwright-build.log" 2>&1; then
    assert_safe_artifact "${WORK_DIR}/playwright-build.log"
    fail 'pinned Playwright test image build failed'
  fi
  assert_safe_artifact "${WORK_DIR}/playwright-build.log"
}

run_browser_acceptance() {
  log 'running the real browser spec in an independent pinned Playwright container'
  api_get "${BASE_URL}/api/v1/config/workspaces" "${WORK_DIR}/browser-before.json"
  jq -cS '[.workspaces[].id] | sort' "${WORK_DIR}/browser-before.json" >"${WORK_DIR}/browser-before.ids"
  build_browser_test_image
  if ! run_bounded 240 docker run --rm \
    --name "${BROWSER_CONTAINER}" \
    --label "${LABEL}" \
    --platform "${PLATFORM}" \
    --network "${NETWORK}" \
    --mount "type=bind,src=${WORK_DIR}/admin-password,dst=/run/secrets/workspace-browser-password,readonly" \
    --env "NGINX_UIX_E2E_BASE_URL=http://${MAIN_CONTAINER}:9000" \
    --env "NGINX_UIX_E2E_USERNAME=${ADMIN_USERNAME}" \
    --env 'NGINX_UIX_E2E_PASSWORD_FILE=/run/secrets/workspace-browser-password' \
    "${BROWSER_TEST_IMAGE}" npm run test:e2e -- e2e/config_workspace_docker.spec.ts \
    >"${WORK_DIR}/playwright-run.log" 2>&1; then
    assert_safe_artifact "${WORK_DIR}/playwright-run.log"
    fail 'real-container Playwright workspace spec failed'
  fi
  assert_safe_artifact "${WORK_DIR}/playwright-run.log"
  api_get "${BASE_URL}/api/v1/config/workspaces" "${WORK_DIR}/browser-after.json"
  jq -cS '[.workspaces[].id] | sort' "${WORK_DIR}/browser-after.json" >"${WORK_DIR}/browser-after.ids"
  cmp -s "${WORK_DIR}/browser-before.ids" "${WORK_DIR}/browser-after.ids" ||
    fail 'browser acceptance left an owned workspace behind'
  [ "$(status_signature "${WORK_DIR}/browser-status.json")" = "${BASELINE_STATUS}" ] ||
    fail 'browser draft workflow changed Nginx process/health identity'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'browser draft workflow changed production fixture bytes'
  pass 'pinned external Playwright container completed the real workspace UI flow without altering the release image'
}

validate_inputs() {
  case "${BUILD_IMAGE}" in auto|0|1) ;; *) fail 'BUILD_IMAGE must be auto, 0, or 1' ;; esac
  case "${PLATFORM:-}" in ''|linux/amd64|linux/arm64) ;; *) fail 'PLATFORM must be linux/amd64 or linux/arm64' ;; esac
  for required_command in docker curl openssl git go jq sqlite3 tar awk sed grep cmp date uname stat chmod; do
    require_command "${required_command}"
  done
  [ "$(tr -d '\r\n' <VERSION)" = 0.2.1 ] || fail 'workspace acceptance requires project VERSION 0.2.1'
  for required_fixture in \
    "${FIXTURE_ROOT}/nginx.conf" \
    "${FIXTURE_ROOT}/conf.d/site.conf" \
    "${FIXTURE_ROOT}/conf.d/cycle-a.conf" \
    "${FIXTURE_ROOT}/conf.d/cycle-b.conf" \
    "${FIXTURE_ROOT}/private/server.key" \
    "${BROWSER_SPEC}" \
    "${REPOSITORY_ROOT}/deploy/docker/Playwright.Dockerfile"; do
    [ -f "${required_fixture}" ] && [ ! -L "${required_fixture}" ] || fail 'required fixed acceptance input is unavailable'
  done
  docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'
  docker buildx version >/dev/null 2>&1 || fail 'Docker Buildx is unavailable'
}

create_owned_resources() {
  [ ! -e "${WORK_DIR}" ] && [ ! -L "${WORK_DIR}" ] || fail 'run-specific work directory already exists'
  mkdir "${WORK_DIR}"
  WORK_DIR_CREATED=1
  chmod 0700 "${WORK_DIR}"
  ADMIN_PASSWORD=$(openssl rand -hex 24)
  printf '%s\n' "${ADMIN_PASSWORD}" >"${WORK_DIR}/admin-password"
  chmod 0444 "${WORK_DIR}/admin-password"

  for owned_container in \
    "${MAIN_CONTAINER}" "${READONLY_CONTAINER}" "${SMALL_CONTAINER}" "${BROWSER_CONTAINER}"; do
    register_container "${owned_container}"
  done
  for owned_volume in "${CONFIG_VOLUME}" "${DATA_VOLUME}" "${READONLY_DATA_VOLUME}"; do
    register_volume "${owned_volume}"
    docker volume create --label "${LABEL}" "${owned_volume}" >/dev/null
  done
  docker network inspect "${NETWORK}" >/dev/null 2>&1 && fail 'owned network name already exists'
  docker network create --label "${LABEL}" "${NETWORK}" >/dev/null
  NETWORK_CREATED=1
}

main() {
  validate_inputs
  create_owned_resources
  ensure_test_image "${IMAGE}" "${BUILD_IMAGE}" "${PLATFORM:-}" ||
    fail 'v0.2 release image identity could not be ensured'
  pass 'v0.2 image has the exact deterministic source/platform identity'
  ensure_v01_image
  seed_fixture_volume
  FIXTURE_DIGEST=$(fixture_digest)
  [ -n "${FIXTURE_DIGEST}" ] || fail 'fixed production fixture digest is empty'

  verify_upgrade
  BASELINE_STATUS=$(status_signature "${WORK_DIR}/fault-baseline-status.json")
  create_workspace "baseline-${RUN_RANDOM}" baseline-production
  BASE_PRODUCTION_DIGEST=${CREATE_PRODUCTION_DIGEST}
  delete_workspace "${CREATE_WORKSPACE_ID}" "baseline-${RUN_RANDOM}" "${CREATE_WORKSPACE_ETAG}" baseline-production-delete

  verify_agent_unavailable_create
  verify_production_change_stale
  exercise_workspace_crud
  verify_generic_ui_restart
  verify_needs_attention_recovery
  verify_workspace_recreation
  verify_data_root_failures
  verify_deterministic_fault_evidence
  run_browser_acceptance

  capture_logs "${MAIN_CONTAINER}" "${WORK_DIR}/main-final.log"
  pass 'workspace upgrade, persistence, safety, fault and browser acceptance completed'
}

main "$@"
