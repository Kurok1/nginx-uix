#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

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
WORKSPACE_PROFILE=${WORKSPACE_PROFILE:-full}
REPEAT_TOTAL_ROUNDS=${REPEAT_TOTAL_ROUNDS:-}
REPEAT_BATCH=${REPEAT_BATCH:-}
REPEAT_BATCH_ROUNDS=5
SOAK_DURATION_SECONDS=600
SOAK_INTERVAL_SECONDS=10
SOAK_EXPECTED_SAMPLES=60
SOAK_MAX_MEMORY_BYTES=268435456
SOAK_MAX_MEMORY_GROWTH_BYTES=67108864
SOAK_MAX_PIDS=64
SOAK_MAX_PID_GROWTH=4
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

wait_terminal_resource() {
  terminal_url=$1
  terminal_prefix=$2
  terminal_limit=$3
  TASK_FILE="${WORK_DIR}/${terminal_prefix}.terminal.json"
  terminal_count=0
  while [ "${terminal_count}" -lt "${terminal_limit}" ]; do
    api_get "${terminal_url}" "${TASK_FILE}"
    TASK_STATE=$(jq -er '.state | select(type == "string" and length > 0)' "${TASK_FILE}")
    case "${TASK_STATE}" in
      succeeded|failed|rolled_back|needs_attention|cancelled|timed_out) return 0 ;;
    esac
    terminal_count=$((terminal_count + 1))
    sleep 1
  done
  fail "${terminal_prefix} did not reach a terminal state within ${terminal_limit} seconds"
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
  database_file="${database_directory}/nginx-uix.db"
  run_bounded 30 docker cp \
    "${MAIN_CONTAINER}:/var/lib/nginx-uix/nginx-uix.db" "${database_file}" >/dev/null ||
    fail 'could not copy the stopped SQLite database'
  [ -f "${database_file}" ] || fail 'SQLite database was absent from the data volume'
  [ "$(sqlite3 "${database_file}" 'PRAGMA integrity_check;')" = ok ] ||
    fail 'SQLite integrity_check failed'
}

assert_upgrade_database() {
  database_file=$1
  migrations=$(sqlite3 "${database_file}" \
    "SELECT COALESCE(group_concat(version, ','), '') FROM (SELECT version FROM schema_migrations ORDER BY version);")
  [ "${migrations}" = '1,2,3,4,5,6,7' ] ||
    fail 'upgraded database does not contain exactly migrations [1,2,3,4,5,6,7]'
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

  log "starting v${PROJECT_VERSION} on the same configuration and data volumes"
  start_main_container "${IMAGE}"
  api_get "${BASE_URL}/api/v1/auth/session" "${WORK_DIR}/upgraded-session.json"
  jq -e --arg username "${ADMIN_USERNAME}" '.user.username == $username and (.csrf_token | type == "string" and length > 20)' \
    "${WORK_DIR}/upgraded-session.json" >/dev/null || fail 'v0.1 user/session did not survive the v1.0 upgrade'
  api_get "${BASE_URL}/api/v1/system/status" "${WORK_DIR}/upgraded-status.json"
  api_get "${BASE_URL}/api/v1/nginx/effective-config" "${WORK_DIR}/upgraded-effective.json"
  assert_workspace_root_permissions
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] || fail 'v1.0 upgrade overwrote preexisting configuration bytes'

  stop_main_container 'v1.0 migration inspection'
  copy_database "${WORK_DIR}/upgraded-database"
  assert_upgrade_database "${WORK_DIR}/upgraded-database/nginx-uix.db"
  start_main_container "${IMAGE}"
  login upgraded-login
  pass 'v0.1.0 data/session/configuration upgraded to migrations [1..7] with exact bytes and permissions preserved'
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
  log "recreating v${PROJECT_VERSION} with the same configuration and data volumes"
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
    tail -n 120 "${WORK_DIR}/playwright-build.log" >&2 || true
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
    tail -n 200 "${WORK_DIR}/playwright-run.log" >&2 || true
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
  pass 'pinned external Playwright container completed real persistence, six-width responsive, keyboard and logout flows without altering the release image'
}

sanitize_release_fixture() {
  log 'removing only the bounded special-entry safety fixtures before release transactions'
  helper_name="nginx-uix-${RUN_ID}-release-fixture"
  register_container "${helper_name}"
  run_bounded 30 docker run --rm --name "${helper_name}" --entrypoint /bin/sh \
    --mount "type=volume,src=${CONFIG_VOLUME},dst=/fixture" \
    "${IMAGE}" -eu -c '
      test -L /fixture/conf.d/external-link.conf
      test -L /fixture/conf.d/broken-link.conf
      test -p /fixture/conf.d/stream.fifo
      rm -f /fixture/conf.d/external-link.conf /fixture/conf.d/broken-link.conf /fixture/conf.d/stream.fifo
    ' >/dev/null || fail 'could not remove the bounded special-entry safety fixtures'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'sanitizing special-entry fixtures changed managed production bytes'
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 20 ||
    fail 'application readiness changed while sanitizing special-entry fixtures'
}

preview_and_apply_structured_change() {
  structured_prefix=$1
  structured_request=$2
  api_mutation POST \
    "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/structured-change-previews" \
    "${structured_request}" '' 200 "${structured_prefix}-preview"
  [ "${RESPONSE_ETAG}" = "${CLOSURE_WORKSPACE_ETAG}" ] ||
    fail "${structured_prefix} preview returned an unexpected ETag"
  jq -e --arg etag "${CLOSURE_WORKSPACE_ETAG}" \
    '.complete == true and .draft_etag == $etag and (.changed_files | length) > 0' \
    "${WORK_DIR}/${structured_prefix}-preview.response.json" >/dev/null ||
    fail "${structured_prefix} preview was incomplete or malformed"
  structured_preview_id=$(jq -er '.preview_id | select(test("^[0-9a-f]{64}$"))' \
    "${WORK_DIR}/${structured_prefix}-preview.response.json")
  jq --arg preview_id "${structured_preview_id}" '. + {preview_id:$preview_id}' \
    "${structured_request}" >"${WORK_DIR}/${structured_prefix}-apply.request.json"
  api_mutation POST \
    "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/structured-changes" \
    "${WORK_DIR}/${structured_prefix}-apply.request.json" "${CLOSURE_WORKSPACE_ETAG}" 200 \
    "${structured_prefix}-apply"
  CLOSURE_WORKSPACE_ETAG=$(jq -er '.draft_etag | select(test("^\\\"draft-v1:[0-9a-f]{64}\\\"$"))' \
    "${WORK_DIR}/${structured_prefix}-apply.response.json")
  [ "${RESPONSE_ETAG}" = "${CLOSURE_WORKSPACE_ETAG}" ] ||
    fail "${structured_prefix} apply header/body ETags differ"
  jq -e '.workspace.state == "ready" and (.changed_paths | length) > 0' \
    "${WORK_DIR}/${structured_prefix}-apply.response.json" >/dev/null ||
    fail "${structured_prefix} apply did not leave a ready workspace"
}

exercise_structured_and_route_lab() {
  log 'exercising typed structured edits and an isolated real-Nginx Route Lab request'
  CLOSURE_WORKSPACE_NAME="closure-${RUN_RANDOM}"
  create_workspace "${CLOSURE_WORKSPACE_NAME}" closure-create
  CLOSURE_WORKSPACE_ID=${CREATE_WORKSPACE_ID}
  CLOSURE_WORKSPACE_ETAG=${CREATE_WORKSPACE_ETAG}

  api_get "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/structured-config" \
    "${WORK_DIR}/closure-structured-initial.json"
  jq -e '
    .complete == true and .project_diagnostics == [] and
    any(.http_blocks[]; .editable == true) and
    any(.servers[]; .editable == true and (.server_names | index("workspace-fixture.example.test")) != null)
  ' "${WORK_DIR}/closure-structured-initial.json" >/dev/null ||
    fail 'initial structured projection did not expose the editable fixture'
  CLOSURE_HTTP_BLOCK_ID=$(jq -er 'first(.http_blocks[] | select(.editable == true)) | .id' \
    "${WORK_DIR}/closure-structured-initial.json")
  CLOSURE_UPSTREAM_NAME="closure_backend_${RUN_RANDOM}"
  jq -n --arg block_id "${CLOSURE_HTTP_BLOCK_ID}" --arg name "${CLOSURE_UPSTREAM_NAME}" \
    '{kind:"upstream.create",input:{http_block_id:$block_id,name:$name,servers:[{address:"127.0.0.1",port:18080}]}}' \
    >"${WORK_DIR}/closure-upstream.request.json"
  preview_and_apply_structured_change closure-upstream "${WORK_DIR}/closure-upstream.request.json"

  api_get "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/structured-config" \
    "${WORK_DIR}/closure-after-upstream.json"
  CLOSURE_UPSTREAM_ID=$(jq -er --arg name "${CLOSURE_UPSTREAM_NAME}" \
    'first(.upstreams[] | select(.name == $name and .editable == true)) | .id' \
    "${WORK_DIR}/closure-after-upstream.json")
  CLOSURE_SERVER_ID=$(jq -er \
    'first(.servers[] | select(.editable == true and (.server_names | index("workspace-fixture.example.test")) != null)) | .id' \
    "${WORK_DIR}/closure-after-upstream.json")
  jq -n --arg parent_id "${CLOSURE_SERVER_ID}" --arg upstream_id "${CLOSURE_UPSTREAM_ID}" \
    '{kind:"location.create",input:{parent_id:$parent_id,type:"prefix",matcher:"/closure",proxy_pass:{upstream_id:$upstream_id,scheme:"http",uri:"/"}}}' \
    >"${WORK_DIR}/closure-location.request.json"
  preview_and_apply_structured_change closure-location "${WORK_DIR}/closure-location.request.json"

  api_get "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/structured-config" \
    "${WORK_DIR}/closure-structured-final.json"
  jq -e --arg upstream_id "${CLOSURE_UPSTREAM_ID}" '
    .complete == true and
    any(.servers[].locations[]; .matcher == "/closure" and
      any(.proxy_passes[]; .upstream_id == $upstream_id and .scheme == "http"))
  ' "${WORK_DIR}/closure-structured-final.json" >/dev/null ||
    fail 'structured location and proxy_pass did not survive reparsing'

  jq -n '{
    scheme:"http",host:"workspace-fixture.example.test",port:8080,method:"GET",uri:"/healthz",
    confirmation:"RUN SIDE-EFFECTING REQUEST",
    assertions:{status_code:200,contains_text:"nginx-uix workspace fixture"}
  }' >"${WORK_DIR}/closure-route.request.json"
  api_mutation POST \
    "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/route-analyses" \
    "${WORK_DIR}/closure-route.request.json" "${CLOSURE_WORKSPACE_ETAG}" 200 closure-route-analysis
  jq -e '
    .complete == true and (.predicted_server_route_id | length) > 0 and
    (.predicted_location_route_id | length) > 0 and
    any(.servers[]; .disposition == "selected" and
      (.server_names | index("workspace-fixture.example.test")) != null) and
    any(.locations[]; .disposition == "selected" and .matcher == "/healthz")
  ' "${WORK_DIR}/closure-route-analysis.response.json" >/dev/null ||
    fail 'Route Lab static analysis did not select the fixture health route'

  api_mutation POST \
    "${BASE_URL}/api/v1/config/workspaces/${CLOSURE_WORKSPACE_ID}/route-tests" \
    "${WORK_DIR}/closure-route.request.json" "${CLOSURE_WORKSPACE_ETAG}" 202 closure-route-run
  CLOSURE_ROUTE_RUN_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/closure-route-run.response.json")
  wait_terminal_resource "${BASE_URL}/api/v1/route-tests/${CLOSURE_ROUTE_RUN_ID}" closure-route-run 90
  jq -e '
    .state == "succeeded" and .stage == "completed" and
    .terminal_result.agent_result.response.status_code == 200 and
    .terminal_result.agent_result.response.assertions.passed == true and
    .terminal_result.agent_result.response.assertions.complete == true and
    .terminal_result.agent_result.evidence.status_code == 200 and
    .terminal_result.agent_result.cleanup.master_reaped == true and
    .terminal_result.agent_result.cleanup.port_closed == true and
    .terminal_result.agent_result.cleanup.stage_removed == true
  ' "${TASK_FILE}" >/dev/null || fail 'isolated Route Lab execution or cleanup evidence failed'
  pass 'typed upstream/location edits and isolated Route Lab execution completed with cleanup evidence'
}

queue_workspace_release() {
  release_prefix=$1
  release_workspace_id=$2
  release_workspace_name=$3
  release_workspace_etag=$4
  jq -n '{}' >"${WORK_DIR}/${release_prefix}-check.request.json"
  api_mutation POST \
    "${BASE_URL}/api/v1/config/workspaces/${release_workspace_id}/publish-checks" \
    "${WORK_DIR}/${release_prefix}-check.request.json" "${release_workspace_etag}" 201 \
    "${release_prefix}-check"
  release_check_id=$(jq -er \
    'select(.state == "valid" and .diagnostic_count == 0 and .details.diagnostics == []) | .id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/${release_prefix}-check.response.json")
  jq -n --arg check_id "${release_check_id}" --arg name "${release_workspace_name}" \
    '{check_id:$check_id,confirm_name:$name}' >"${WORK_DIR}/${release_prefix}.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/workspaces/${release_workspace_id}/releases" \
    "${WORK_DIR}/${release_prefix}.request.json" "${release_workspace_etag}" 202 "${release_prefix}"
  QUEUED_RELEASE_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/${release_prefix}.response.json")
}

verify_repeated_recovery_resources() {
  repeat_backup_count=$((REPEAT_BATCH_ROUNDS * 2))
  repeat_audit_minimum=$((REPEAT_BATCH_ROUNDS * 2))

  api_get "${BASE_URL}/api/v1/config/workspaces" "${WORK_DIR}/repeat-workspaces.json"
  jq -e --argjson rounds "${REPEAT_BATCH_ROUNDS}" --arg prefix "repeat-${RUN_RANDOM}-" '
    (.workspaces | length) == $rounds and
    all(.workspaces[]; .state == "published" and (.name | startswith($prefix)))
  ' "${WORK_DIR}/repeat-workspaces.json" >/dev/null ||
    fail 'repeated recovery workspace evidence is incomplete'

  api_get "${BASE_URL}/api/v1/config/history/releases?limit=100" \
    "${WORK_DIR}/repeat-release-history.json"
  jq -e --argjson rounds "${REPEAT_BATCH_ROUNDS}" '
    (.items | length) == $rounds and all(.items[]; .state == "succeeded")
  ' "${WORK_DIR}/repeat-release-history.json" >/dev/null ||
    fail 'repeated recovery release history is incomplete'

  api_get "${BASE_URL}/api/v1/config/history/restores?limit=100" \
    "${WORK_DIR}/repeat-restore-history.json"
  jq -e --argjson rounds "${REPEAT_BATCH_ROUNDS}" '
    (.items | length) == $rounds and all(.items[]; .state == "succeeded")
  ' "${WORK_DIR}/repeat-restore-history.json" >/dev/null ||
    fail 'repeated recovery restore history is incomplete'

  api_get "${BASE_URL}/api/v1/config/backups?limit=100" "${WORK_DIR}/repeat-backups.json"
  jq -e --argjson expected "${repeat_backup_count}" '
    (.items | length) == $expected and
    all(.items[]; .state == "complete" and .body_present == true)
  ' "${WORK_DIR}/repeat-backups.json" >/dev/null ||
    fail 'repeated recovery backup inventory is incomplete'

  api_get "${BASE_URL}/api/v1/config/audit-events?limit=100" "${WORK_DIR}/repeat-audit.json"
  jq -e --argjson minimum "${repeat_audit_minimum}" '
    (.items | length) >= $minimum and all(.items[]; (.details | type) == "object")
  ' "${WORK_DIR}/repeat-audit.json" >/dev/null ||
    fail 'repeated recovery audit evidence is incomplete or not redacted'

  docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c '
    test -z "$(find /var/lib/nginx-uix/releases -maxdepth 1 -type d -name ".candidate-*" -print -quit)"
    test -z "$(find /var/lib/nginx-uix/restores -mindepth 2 -maxdepth 2 -type d -name validation -print -quit)"
    test -z "$(find /var/lib/nginx-uix/workspaces -mindepth 2 -maxdepth 2 -type d -name "base.stage-*" -print -quit)"
  ' || fail 'repeated recovery left a candidate, validation, or workspace stage behind'
}

verify_repeated_recovery_database() {
  stop_main_container 'repeated recovery database inspection'
  copy_database "${WORK_DIR}/repeat-database"
  repeat_database="${WORK_DIR}/repeat-database/nginx-uix.db"
  [ -z "$(sqlite3 "${repeat_database}" 'PRAGMA foreign_key_check;')" ] ||
    fail 'repeated recovery SQLite foreign_key_check failed'
  repeat_migrations=$(sqlite3 "${repeat_database}" \
    "SELECT COALESCE(group_concat(version, ','), '') FROM (SELECT version FROM schema_migrations ORDER BY version);")
  [ "${repeat_migrations}" = '1,2,3,4,5,6,7' ] ||
    fail 'repeated recovery database migrations are not exactly [1..7]'
  [ "$(sqlite3 "${repeat_database}" 'SELECT COUNT(*) FROM config_workspaces;')" = "${REPEAT_BATCH_ROUNDS}" ] ||
    fail 'repeated recovery database lost a published workspace'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_workspaces WHERE state = 'published';")" = "${REPEAT_BATCH_ROUNDS}" ] ||
    fail 'repeated recovery database contains a non-published workspace'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_releases WHERE state = 'succeeded';")" = "${REPEAT_BATCH_ROUNDS}" ] ||
    fail 'repeated recovery database lost a successful release'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_restores WHERE state = 'succeeded';")" = "${REPEAT_BATCH_ROUNDS}" ] ||
    fail 'repeated recovery database lost a successful restore'
  repeat_backup_count=$((REPEAT_BATCH_ROUNDS * 2))
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_backups WHERE state = 'complete' AND body_present = 1;")" = "${repeat_backup_count}" ] ||
    fail 'repeated recovery database lost a complete backup'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_release_stages WHERE stage = 'committed' AND result = 'success';")" = "${REPEAT_BATCH_ROUNDS}" ] ||
    fail 'repeated recovery database lost a committed release stage'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_restore_stages WHERE stage = 'succeeded' AND result = 'success';")" = "${REPEAT_BATCH_ROUNDS}" ] ||
    fail 'repeated recovery database lost a successful restore stage'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_releases WHERE state IN ('queued', 'running', 'rolling_back');")" = 0 ] ||
    fail 'repeated recovery database retained an active release'
  [ "$(sqlite3 "${repeat_database}" "SELECT COUNT(*) FROM config_restores WHERE state IN ('queued', 'running', 'rolling_back');")" = 0 ] ||
    fail 'repeated recovery database retained an active restore'
  [ "$(sqlite3 "${repeat_database}" \
    'SELECT COUNT(*) FROM config_production_lease WHERE owner_type IS NULL AND owner_id IS NULL AND acquired_at IS NULL;')" = 1 ] ||
    fail 'repeated recovery database retained the production lease'
  repeat_audit_minimum=$((REPEAT_BATCH_ROUNDS * 2))
  [ "$(sqlite3 "${repeat_database}" 'SELECT COUNT(*) FROM audit_events;')" -ge "${repeat_audit_minimum}" ] ||
    fail 'repeated recovery database lost audit history'

  start_main_container "${IMAGE}"
  login repeat-recreate-login
  while IFS= read -r repeat_release_id; do
    api_get "${BASE_URL}/api/v1/config/releases/${repeat_release_id}" \
      "${WORK_DIR}/repeat-release-${repeat_release_id}.json"
    jq -e '.state == "succeeded" and .stage == "committed"' \
      "${WORK_DIR}/repeat-release-${repeat_release_id}.json" >/dev/null ||
      fail 'recreated container lost a successful release'
  done <"${WORK_DIR}/repeat-release.ids"
  while IFS= read -r repeat_restore_id; do
    api_get "${BASE_URL}/api/v1/config/restores/${repeat_restore_id}" \
      "${WORK_DIR}/repeat-restore-${repeat_restore_id}.json"
    jq -e '.state == "succeeded" and .stage == "succeeded"' \
      "${WORK_DIR}/repeat-restore-${repeat_restore_id}.json" >/dev/null ||
      fail 'recreated container lost a successful restore'
  done <"${WORK_DIR}/repeat-restore.ids"
  while IFS= read -r repeat_backup_id; do
    api_get "${BASE_URL}/api/v1/config/backups/${repeat_backup_id}" \
      "${WORK_DIR}/repeat-backup-${repeat_backup_id}.json"
    jq -e '.state == "complete" and .body_present == true' \
      "${WORK_DIR}/repeat-backup-${repeat_backup_id}.json" >/dev/null ||
      fail 'recreated container lost a complete backup'
  done <"${WORK_DIR}/repeat-backup.ids"
  while IFS= read -r repeat_workspace_id; do
    api_get "${BASE_URL}/api/v1/config/workspaces/${repeat_workspace_id}" \
      "${WORK_DIR}/repeat-workspace-${repeat_workspace_id}.json"
    jq -e '.state == "published"' "${WORK_DIR}/repeat-workspace-${repeat_workspace_id}.json" >/dev/null ||
      fail 'recreated container lost a published workspace'
  done <"${WORK_DIR}/repeat-workspace.ids"
  api_get "${BASE_URL}/api/v1/nginx/effective-config" "${WORK_DIR}/repeat-recreated-effective.json"
  while IFS= read -r repeat_marker; do
    jq -e --arg marker "${repeat_marker}" \
      'all(.occurrences[]; ((.content | contains($marker)) | not))' \
      "${WORK_DIR}/repeat-recreated-effective.json" >/dev/null ||
      fail 'recreated container exposed a marker that should have been restored'
  done <"${WORK_DIR}/repeat-markers"
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'recreated container changed the restored production bytes'
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 30 ||
    fail 'recreated repeated recovery container was not ready'
  verify_repeated_recovery_resources
}

exercise_repeated_release_restore() {
  log "exercising repeated recovery batch ${REPEAT_BATCH}: ${REPEAT_BATCH_ROUNDS} fixed publish, reload, backup and manual-restore rounds"
  : >"${WORK_DIR}/repeat-release.ids"
  : >"${WORK_DIR}/repeat-restore.ids"
  : >"${WORK_DIR}/repeat-backup.ids"
  : >"${WORK_DIR}/repeat-workspace.ids"
  : >"${WORK_DIR}/repeat-markers"

  repeat_batch_round=1
  REPEAT_MASTER_PID=
  REPEAT_BASELINE_PRODUCTION_DIGEST=
  while [ "${repeat_batch_round}" -le "${REPEAT_BATCH_ROUNDS}" ]; do
    repeat_round=$(((REPEAT_BATCH - 1) * REPEAT_BATCH_ROUNDS + repeat_batch_round))
    repeat_prefix="repeat-${REPEAT_BATCH}-${repeat_batch_round}"
    repeat_workspace_name="repeat-${RUN_RANDOM}-${repeat_round}"
    repeat_marker="repeat-release-round-${repeat_round}-${RUN_RANDOM}"
    create_workspace "${repeat_workspace_name}" "${repeat_prefix}-create"
    repeat_workspace_id=${CREATE_WORKSPACE_ID}
    repeat_workspace_etag=${CREATE_WORKSPACE_ETAG}
    if [ -z "${REPEAT_BASELINE_PRODUCTION_DIGEST}" ]; then
      REPEAT_BASELINE_PRODUCTION_DIGEST=${CREATE_PRODUCTION_DIGEST}
    fi
    [ "${CREATE_PRODUCTION_DIGEST}" = "${REPEAT_BASELINE_PRODUCTION_DIGEST}" ] ||
      fail "repeat round ${repeat_round} did not start from the restored production digest"

    api_get "${BASE_URL}/api/v1/config/workspaces/${repeat_workspace_id}/files?path=conf.d%2Fsite.conf" \
      "${WORK_DIR}/${repeat_prefix}-site.json"
    jq --arg marker "${repeat_marker}" '{content:(.content + "\n# " + $marker + "\n")}' \
      "${WORK_DIR}/${repeat_prefix}-site.json" >"${WORK_DIR}/${repeat_prefix}-site.request.json"
    api_mutation PUT \
      "${BASE_URL}/api/v1/config/workspaces/${repeat_workspace_id}/files?path=conf.d%2Fsite.conf" \
      "${WORK_DIR}/${repeat_prefix}-site.request.json" "${repeat_workspace_etag}" 200 \
      "${repeat_prefix}-site"
    repeat_workspace_etag=$(workspace_etag_from_body "${WORK_DIR}/${repeat_prefix}-site.response.json")

    repeat_before_status=$(status_signature "${WORK_DIR}/${repeat_prefix}-before.status.json")
    repeat_before_master=$(printf '%s\n' "${repeat_before_status}" | jq -er '.master')
    repeat_before_workers=$(printf '%s\n' "${repeat_before_status}" | jq -ecS '.workers')
    if [ -z "${REPEAT_MASTER_PID}" ]; then
      REPEAT_MASTER_PID=${repeat_before_master}
    fi
    [ "${repeat_before_master}" = "${REPEAT_MASTER_PID}" ] ||
      fail "repeat round ${repeat_round} changed the Nginx master before publication"

    queue_workspace_release "${repeat_prefix}-release" "${repeat_workspace_id}" \
      "${repeat_workspace_name}" "${repeat_workspace_etag}"
    repeat_release_id=${QUEUED_RELEASE_ID}
    wait_terminal_resource "${BASE_URL}/api/v1/config/releases/${repeat_release_id}" \
      "${repeat_prefix}-release" 150
    if ! jq -e '
      .state == "succeeded" and .stage == "committed" and
      (.backup_id | test("^[0-9a-f]{32}$")) and
      any(.stages[]; .stage == "backup_verified" and .result == "success") and
      any(.stages[]; .stage == "reload_requested" and .result == "success") and
      any(.stages[]; .stage == "runtime_confirmed" and .result == "success")
    ' "${TASK_FILE}" >/dev/null; then
      jq -c '{state,stage,last_error_code,stages:[.stages[] | {stage,result,code}]}' "${TASK_FILE}" >&2
      fail "repeat round ${repeat_round} release did not commit"
    fi
    repeat_backup_id=$(jq -er '.backup_id' "${TASK_FILE}")
    printf '%s\n' "${repeat_release_id}" >>"${WORK_DIR}/repeat-release.ids"
    printf '%s\n' "${repeat_backup_id}" >>"${WORK_DIR}/repeat-backup.ids"
    printf '%s\n' "${repeat_workspace_id}" >>"${WORK_DIR}/repeat-workspace.ids"
    printf '%s\n' "${repeat_marker}" >>"${WORK_DIR}/repeat-markers"

    api_get "${BASE_URL}/api/v1/config/backups/${repeat_backup_id}" \
      "${WORK_DIR}/${repeat_prefix}-release-backup.json"
    jq -e '.state == "complete" and .body_present == true' \
      "${WORK_DIR}/${repeat_prefix}-release-backup.json" >/dev/null ||
      fail "repeat round ${repeat_round} release backup is incomplete"
    repeat_release_status=$(status_signature "${WORK_DIR}/${repeat_prefix}-release.status.json")
    repeat_release_master=$(printf '%s\n' "${repeat_release_status}" | jq -er '.master')
    repeat_release_workers=$(printf '%s\n' "${repeat_release_status}" | jq -ecS '.workers')
    [ "${repeat_release_master}" = "${REPEAT_MASTER_PID}" ] ||
      fail "repeat round ${repeat_round} publication replaced the Nginx master"
    [ "${repeat_release_workers}" != "${repeat_before_workers}" ] ||
      fail "repeat round ${repeat_round} publication did not replace Nginx workers"
    [ "$(fixture_digest)" != "${FIXTURE_DIGEST}" ] ||
      fail "repeat round ${repeat_round} publication did not change production bytes"
    api_get "${BASE_URL}/api/v1/nginx/effective-config" \
      "${WORK_DIR}/${repeat_prefix}-published-effective.json"
    jq -e --arg marker "${repeat_marker}" \
      'any(.occurrences[]; (.content | contains($marker)))' \
      "${WORK_DIR}/${repeat_prefix}-published-effective.json" >/dev/null ||
      fail "repeat round ${repeat_round} marker was absent after publication"

    jq -n --arg backup_id "${repeat_backup_id}" \
      --arg reason "v1.0 repeated recovery round ${repeat_round}" \
      '{attention_case_id:"",reason:$reason,confirm_backup_id:$backup_id}' \
      >"${WORK_DIR}/${repeat_prefix}-restore.request.json"
    api_mutation POST "${BASE_URL}/api/v1/config/backups/${repeat_backup_id}/restores" \
      "${WORK_DIR}/${repeat_prefix}-restore.request.json" '' 202 "${repeat_prefix}-restore"
    repeat_restore_id=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
      "${WORK_DIR}/${repeat_prefix}-restore.response.json")
    wait_terminal_resource "${BASE_URL}/api/v1/config/restores/${repeat_restore_id}" \
      "${repeat_prefix}-restore" 150
    if ! jq -e --arg backup_id "${repeat_backup_id}" '
      .state == "succeeded" and .stage == "succeeded" and .target_backup_id == $backup_id and
      (.safety_backup_id | test("^[0-9a-f]{32}$")) and
      any(.stages[]; .stage == "target_validated" and .result == "success") and
      any(.stages[]; .stage == "safety_backup_verified" and .result == "success") and
      any(.stages[]; .stage == "reload_requested" and .result == "success") and
      any(.stages[]; .stage == "runtime_confirmed" and .result == "success")
    ' "${TASK_FILE}" >/dev/null; then
      jq -c '{state,stage,last_error_code,stages:[.stages[] | {stage,result,code}]}' "${TASK_FILE}" >&2
      fail "repeat round ${repeat_round} restore did not succeed"
    fi
    repeat_safety_backup_id=$(jq -er '.safety_backup_id' "${TASK_FILE}")
    printf '%s\n' "${repeat_restore_id}" >>"${WORK_DIR}/repeat-restore.ids"
    printf '%s\n' "${repeat_safety_backup_id}" >>"${WORK_DIR}/repeat-backup.ids"
    api_get "${BASE_URL}/api/v1/config/backups/${repeat_safety_backup_id}" \
      "${WORK_DIR}/${repeat_prefix}-safety-backup.json"
    jq -e '.state == "complete" and .body_present == true' \
      "${WORK_DIR}/${repeat_prefix}-safety-backup.json" >/dev/null ||
      fail "repeat round ${repeat_round} safety backup is incomplete"

    repeat_restore_status=$(status_signature "${WORK_DIR}/${repeat_prefix}-restore.status.json")
    repeat_restore_master=$(printf '%s\n' "${repeat_restore_status}" | jq -er '.master')
    repeat_restore_workers=$(printf '%s\n' "${repeat_restore_status}" | jq -ecS '.workers')
    [ "${repeat_restore_master}" = "${REPEAT_MASTER_PID}" ] ||
      fail "repeat round ${repeat_round} restore replaced the Nginx master"
    [ "${repeat_restore_workers}" != "${repeat_release_workers}" ] ||
      fail "repeat round ${repeat_round} restore did not replace Nginx workers"
    [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
      fail "repeat round ${repeat_round} restore did not recover exact production bytes"
    wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 30 ||
      fail "repeat round ${repeat_round} lost readiness after restore"
    api_get "${BASE_URL}/api/v1/nginx/effective-config" \
      "${WORK_DIR}/${repeat_prefix}-restored-effective.json"
    jq -e --arg marker "${repeat_marker}" \
      'all(.occurrences[]; ((.content | contains($marker)) | not))' \
      "${WORK_DIR}/${repeat_prefix}-restored-effective.json" >/dev/null ||
      fail "repeat round ${repeat_round} marker survived restore"
    api_get "${BASE_URL}/api/v1/config/workspaces/${repeat_workspace_id}" \
      "${WORK_DIR}/${repeat_prefix}-workspace.json"
    jq -e '.state == "published"' "${WORK_DIR}/${repeat_prefix}-workspace.json" >/dev/null ||
      fail "repeat round ${repeat_round} lost published workspace evidence"

    pass "repeat round ${repeat_round}/${REPEAT_TOTAL_ROUNDS} published and restored exact production state"
    repeat_batch_round=$((repeat_batch_round + 1))
  done

  verify_repeated_recovery_resources
  verify_repeated_recovery_database
  pass "repeat batch ${REPEAT_BATCH} preserved runtime, data and evidence across ${REPEAT_BATCH_ROUNDS} rounds"
}

stability_service_pid() {
  soak_service_name=$1
  soak_service_output=$2
  run_bounded 5 docker exec "${MAIN_CONTAINER}" \
    /command/s6-svstat -o pid "/run/service/${soak_service_name}" >"${soak_service_output}" ||
    fail "could not read ${soak_service_name} service PID during stability sampling"
  SOAK_SERVICE_PID=$(tr -d '\r\n' <"${soak_service_output}")
  case "${SOAK_SERVICE_PID}" in
    ''|*[!0-9]*) fail "${soak_service_name} service PID was invalid during stability sampling" ;;
  esac
}

read_stability_metrics() {
  soak_metrics_output=$1
  run_bounded 5 docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c '
    memory_file=/sys/fs/cgroup/memory.current
    pids_file=/sys/fs/cgroup/pids.current
    test -r "${memory_file}"
    test -r "${pids_file}"
    oom_kills=$(awk '\''$1 == "oom_kill" { print $2 }'\'' /sys/fs/cgroup/memory.events)
    case "${oom_kills}" in ""|*[!0-9]*) exit 1 ;; esac
    printf "%s %s %s\n" "$(cat "${memory_file}")" "$(cat "${pids_file}")" "${oom_kills}"
  ' >"${soak_metrics_output}" ||
    fail 'could not read bounded cgroup v2 stability metrics'
  IFS=' ' read -r SOAK_MEMORY SOAK_PIDS SOAK_OOM_KILLS <"${soak_metrics_output}"
  for soak_metric in "${SOAK_MEMORY}" "${SOAK_PIDS}" "${SOAK_OOM_KILLS}"; do
    case "${soak_metric}" in
      ''|*[!0-9]*) fail 'container stability metric was not a non-negative integer' ;;
    esac
  done
}

wait_docker_healthy() {
  soak_health_count=0
  while [ "${soak_health_count}" -lt 30 ]; do
    [ "$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' \
      "${MAIN_CONTAINER}" 2>/dev/null || true)" = healthy ] && return 0
    soak_health_count=$((soak_health_count + 1))
    sleep 1
  done
  return 1
}

assert_stability_logs() {
  soak_log_path=$1
  capture_logs "${MAIN_CONTAINER}" "${soak_log_path}"
  if grep -Eiq \
    'panic:|fatal error:|level=error|"level":"error"|authentication state cleanup failed|finished with failure' \
    "${soak_log_path}"; then
    fail 'stability window contained a fatal, error, cleanup, or task-owner failure log'
  fi
}

assert_stability_sample() {
  soak_sample=$1
  soak_live_code=$(curl --silent --connect-timeout 1 --max-time 3 \
    --output /dev/null --write-out '%{http_code}' "${BASE_URL}/health/live" || true)
  [ "${soak_live_code}" = 200 ] || fail "stability sample ${soak_sample} lost liveness"
  soak_ready_code=$(curl --silent --connect-timeout 1 --max-time 3 \
    --output /dev/null --write-out '%{http_code}' "${BASE_URL}/health/ready" || true)
  [ "${soak_ready_code}" = 200 ] || fail "stability sample ${soak_sample} lost readiness"

  soak_status=$(status_signature "${WORK_DIR}/stability-status.json")
  [ "${soak_status}" = "${SOAK_BASELINE_STATUS}" ] ||
    fail "stability sample ${soak_sample} changed Nginx process identity"
  jq -e '.issues == [] and (.recovery == null or .recovery.permanent == false)' \
    "${WORK_DIR}/stability-status.json" >/dev/null ||
    fail "stability sample ${soak_sample} reported an issue or permanent recovery state"

  stability_service_pid nginx-uix "${WORK_DIR}/stability-ui.pid"
  [ "${SOAK_SERVICE_PID}" = "${SOAK_UI_PID}" ] ||
    fail "stability sample ${soak_sample} replaced the UI process"
  stability_service_pid nginx-uix-agent "${WORK_DIR}/stability-agent.pid"
  [ "${SOAK_SERVICE_PID}" = "${SOAK_AGENT_PID}" ] ||
    fail "stability sample ${soak_sample} replaced the Agent process"

  soak_container_state=$(docker inspect --format \
    '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{.RestartCount}}|{{.State.OOMKilled}}' \
    "${MAIN_CONTAINER}")
  [ "${soak_container_state}" = 'running|healthy|0|false' ] ||
    fail "stability sample ${soak_sample} found an unhealthy, restarted, or OOM-killed container"

  read_stability_metrics "${WORK_DIR}/stability-metrics.current"
  [ "${SOAK_MEMORY}" -le "${SOAK_MAX_MEMORY_BYTES}" ] ||
    fail "stability sample ${soak_sample} exceeded the fixed memory ceiling"
  [ "${SOAK_PIDS}" -le "${SOAK_MAX_PIDS}" ] ||
    fail "stability sample ${soak_sample} exceeded the fixed PID ceiling"
  [ "${SOAK_OOM_KILLS}" = 0 ] ||
    fail "stability sample ${soak_sample} observed an OOM kill"

  api_get "${BASE_URL}/api/v1/route-tests/${CLOSURE_ROUTE_RUN_ID}" \
    "${WORK_DIR}/stability-route.json"
  jq -e '
    .state == "succeeded" and .stage == "completed" and
    .terminal_result.agent_result.cleanup.master_reaped == true and
    .terminal_result.agent_result.cleanup.port_closed == true and
    .terminal_result.agent_result.cleanup.stage_removed == true
  ' "${WORK_DIR}/stability-route.json" >/dev/null ||
    fail "stability sample ${soak_sample} lost terminal Route Lab cleanup evidence"
  api_get "${BASE_URL}/api/v1/certificate-tasks?limit=100" \
    "${WORK_DIR}/stability-certificate-tasks.json"
  jq -e '.tasks == []' "${WORK_DIR}/stability-certificate-tasks.json" >/dev/null ||
    fail "stability sample ${soak_sample} found an unexpected certificate task"
  api_get "${BASE_URL}/api/v1/certificates?limit=100" \
    "${WORK_DIR}/stability-certificates.json"
  jq -e '.certificates == []' "${WORK_DIR}/stability-certificates.json" >/dev/null ||
    fail "stability sample ${soak_sample} found unexpected certificate inventory"
  for soak_history in releases restores restarts; do
    api_get "${BASE_URL}/api/v1/config/history/${soak_history}?limit=100" \
      "${WORK_DIR}/stability-${soak_history}.json"
    jq -e '.items == []' "${WORK_DIR}/stability-${soak_history}.json" >/dev/null ||
      fail "stability sample ${soak_sample} found unexpected ${soak_history} history"
  done

  run_bounded 5 docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c '
    test -z "$(find /var/lib/nginx-uix/route-lab -mindepth 1 -print -quit)"
    test -z "$(find /var/lib/nginx-uix/releases -maxdepth 1 -type d -name ".candidate-*" -print -quit)"
    test -z "$(find /var/lib/nginx-uix/restores -mindepth 2 -maxdepth 2 -type d -name validation -print -quit)"
    test -z "$(find /var/lib/nginx-uix/workspaces -mindepth 2 -maxdepth 2 -type d -name "base.stage-*" -print -quit)"
  ' || fail "stability sample ${soak_sample} found a residual sandbox or transaction stage"

  printf '%s\t%s\t%s\t%s\n' \
    "${soak_sample}" "${SOAK_MEMORY}" "${SOAK_PIDS}" "${SOAK_OOM_KILLS}" \
    >>"${WORK_DIR}/stability-samples.tsv"
}

verify_stability_database() {
  stop_main_container 'stability database inspection'
  copy_database "${WORK_DIR}/stability-database"
  soak_database="${WORK_DIR}/stability-database/nginx-uix.db"
  [ "$(sqlite3 "${soak_database}" 'PRAGMA integrity_check;')" = ok ] ||
    fail 'stability SQLite integrity_check failed'
  [ -z "$(sqlite3 "${soak_database}" 'PRAGMA foreign_key_check;')" ] ||
    fail 'stability SQLite foreign_key_check failed'
  soak_migrations=$(sqlite3 "${soak_database}" \
    "SELECT COALESCE(group_concat(version, ','), '') FROM (SELECT version FROM schema_migrations ORDER BY version);")
  [ "${soak_migrations}" = '1,2,3,4,5,6,7' ] ||
    fail 'stability database migrations are not exactly [1..7]'
  [ "$(sqlite3 "${soak_database}" 'SELECT COUNT(*) FROM config_workspaces;')" = 1 ] ||
    fail 'stability database lost its durable workspace'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM config_workspaces WHERE state = 'ready';")" = 1 ] ||
    fail 'stability database workspace did not remain ready'
  [ "$(sqlite3 "${soak_database}" 'SELECT COUNT(*) FROM route_lab_runs;')" = 1 ] ||
    fail 'stability database lost its Route Lab evidence'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM route_lab_runs WHERE state = 'succeeded' AND stage = 'completed';")" = 1 ] ||
    fail 'stability database Route Lab task was not terminal'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM route_lab_runs WHERE state IN ('queued', 'running');")" = 0 ] ||
    fail 'stability database retained an active Route Lab task'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM certificate_tasks WHERE state IN ('queued', 'running', 'cancelling');")" = 0 ] ||
    fail 'stability database retained an active certificate task'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM config_releases WHERE state IN ('queued', 'running', 'rolling_back');")" = 0 ] ||
    fail 'stability database retained an active release'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM config_restores WHERE state IN ('queued', 'running', 'rolling_back');")" = 0 ] ||
    fail 'stability database retained an active restore'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM config_restarts WHERE state IN ('queued', 'running');")" = 0 ] ||
    fail 'stability database retained an active restart'
  [ "$(sqlite3 "${soak_database}" "SELECT COUNT(*) FROM config_retention_runs WHERE state = 'executing';")" = 0 ] ||
    fail 'stability database retained an active retention run'
  [ "$(sqlite3 "${soak_database}" 'SELECT COUNT(*) FROM config_production_lease;')" = 1 ] ||
    fail 'stability database lost its singleton production lease'
  [ "$(sqlite3 "${soak_database}" \
    'SELECT COUNT(*) FROM config_production_lease WHERE owner_type IS NULL AND owner_id IS NULL AND acquired_at IS NULL;')" = 1 ] ||
    fail 'stability database retained a production-operation owner'

  start_main_container "${IMAGE}"
  login stability-recreate-login
  wait_docker_healthy || fail 'recreated stability container did not become Docker-healthy'
  [ -n "$(status_signature "${WORK_DIR}/stability-recreated-status.json")" ] ||
    fail 'recreated stability container did not expose healthy process evidence'
  api_get "${BASE_URL}/api/v1/route-tests/${CLOSURE_ROUTE_RUN_ID}" \
    "${WORK_DIR}/stability-recreated-route.json"
  jq -e '.state == "succeeded" and .stage == "completed"' \
    "${WORK_DIR}/stability-recreated-route.json" >/dev/null ||
    fail 'recreated stability container lost terminal Route Lab evidence'
  api_get "${BASE_URL}/api/v1/certificate-tasks?limit=100" \
    "${WORK_DIR}/stability-recreated-certificate-tasks.json"
  jq -e '.tasks == []' "${WORK_DIR}/stability-recreated-certificate-tasks.json" >/dev/null ||
    fail 'recreated stability container found an unexpected certificate task'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'stability recreation changed production fixture bytes'
  run_bounded 5 docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c \
    'test -z "$(find /var/lib/nginx-uix/route-lab -mindepth 1 -print -quit)"' ||
    fail 'recreated stability container retained a Route Lab sandbox'
  assert_stability_logs "${WORK_DIR}/stability-recreated.log"
}

exercise_stability_soak() {
  log "exercising fixed ${SOAK_DURATION_SECONDS}-second stability window with ${SOAK_EXPECTED_SAMPLES} samples"
  wait_docker_healthy || fail 'stability container did not become Docker-healthy'
  SOAK_BASELINE_STATUS=$(status_signature "${WORK_DIR}/stability-baseline-status.json")
  jq -e '.issues == [] and (.recovery == null or .recovery.permanent == false)' \
    "${WORK_DIR}/stability-baseline-status.json" >/dev/null ||
    fail 'stability baseline reported an issue or permanent recovery state'
  stability_service_pid nginx-uix "${WORK_DIR}/stability-ui-baseline.pid"
  SOAK_UI_PID=${SOAK_SERVICE_PID}
  stability_service_pid nginx-uix-agent "${WORK_DIR}/stability-agent-baseline.pid"
  SOAK_AGENT_PID=${SOAK_SERVICE_PID}
  read_stability_metrics "${WORK_DIR}/stability-metrics.baseline"
  SOAK_BASELINE_MEMORY=${SOAK_MEMORY}
  SOAK_BASELINE_PIDS=${SOAK_PIDS}
  [ "${SOAK_BASELINE_MEMORY}" -le "${SOAK_MAX_MEMORY_BYTES}" ] ||
    fail 'stability baseline exceeded the fixed memory ceiling'
  [ "${SOAK_BASELINE_PIDS}" -le "${SOAK_MAX_PIDS}" ] ||
    fail 'stability baseline exceeded the fixed PID ceiling'
  [ "${SOAK_OOM_KILLS}" = 0 ] || fail 'stability baseline already contained an OOM kill'

  printf 'sample\tmemory_bytes\tpids\toom_kills\n' >"${WORK_DIR}/stability-samples.tsv"
  soak_started=$(date +%s)
  soak_sample=1
  while [ "${soak_sample}" -le "${SOAK_EXPECTED_SAMPLES}" ]; do
    sleep "${SOAK_INTERVAL_SECONDS}"
    assert_stability_sample "${soak_sample}"
    if [ "$((soak_sample % 6))" = 0 ]; then
      pass "stability samples ${soak_sample}/${SOAK_EXPECTED_SAMPLES} remain healthy"
    fi
    soak_sample=$((soak_sample + 1))
  done
  soak_elapsed=$(($(date +%s) - soak_started))
  [ "${soak_elapsed}" -ge "${SOAK_DURATION_SECONDS}" ] ||
    fail 'stability observation window completed below its fixed duration'
  [ "$(($(wc -l <"${WORK_DIR}/stability-samples.tsv") - 1))" = "${SOAK_EXPECTED_SAMPLES}" ] ||
    fail 'stability evidence did not contain the fixed sample count'

  soak_memory_growth=0
  [ "${SOAK_MEMORY}" -le "${SOAK_BASELINE_MEMORY}" ] ||
    soak_memory_growth=$((SOAK_MEMORY - SOAK_BASELINE_MEMORY))
  [ "${soak_memory_growth}" -le "${SOAK_MAX_MEMORY_GROWTH_BYTES}" ] ||
    fail 'stability final memory growth exceeded the fixed allowance'
  soak_pid_growth=0
  [ "${SOAK_PIDS}" -le "${SOAK_BASELINE_PIDS}" ] ||
    soak_pid_growth=$((SOAK_PIDS - SOAK_BASELINE_PIDS))
  [ "${soak_pid_growth}" -le "${SOAK_MAX_PID_GROWTH}" ] ||
    fail 'stability final PID growth exceeded the fixed allowance'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'stability window changed production fixture bytes'
  assert_stability_logs "${WORK_DIR}/stability-window.log"
  verify_stability_database
  pass "fixed ${SOAK_DURATION_SECONDS}-second stability window preserved health, processes, tasks, data, and cleanup"
}

exercise_release_recovery_closure() {
  log 'exercising release, fixed restart, manual restore, automatic rollback, and durable history'
  queue_workspace_release closure-release "${CLOSURE_WORKSPACE_ID}" \
    "${CLOSURE_WORKSPACE_NAME}" "${CLOSURE_WORKSPACE_ETAG}"
  CLOSURE_RELEASE_ID=${QUEUED_RELEASE_ID}
  wait_terminal_resource "${BASE_URL}/api/v1/config/releases/${CLOSURE_RELEASE_ID}" closure-release 150
  if ! jq -e '
    .state == "succeeded" and .stage == "committed" and
    (.backup_id | test("^[0-9a-f]{32}$")) and
    any(.stages[]; .stage == "runtime_confirmed" and .result == "success")
  ' "${TASK_FILE}" >/dev/null; then
    jq -c '{state,stage,last_error_code,stages:[.stages[] | {stage,result,code}]}' "${TASK_FILE}" >&2
    fail 'typed workspace release did not commit successfully'
  fi
  CLOSURE_BACKUP_ID=$(jq -er '.backup_id' "${TASK_FILE}")
  PUBLISHED_FIXTURE_DIGEST=$(fixture_digest)
  [ "${PUBLISHED_FIXTURE_DIGEST}" != "${FIXTURE_DIGEST}" ] ||
    fail 'committed structured release did not change production bytes'
  BEFORE_RESTART_STATUS=$(status_signature "${WORK_DIR}/closure-before-restart-status.json")
  BEFORE_RESTART_MASTER=$(printf '%s\n' "${BEFORE_RESTART_STATUS}" | jq -er '.master')

  jq -n '{attention_case_id:"",reason:"v1.0 final-image restart verification",confirmation:"RESTART NGINX"}' \
    >"${WORK_DIR}/closure-restart.request.json"
  api_mutation POST "${BASE_URL}/api/v1/nginx/restarts" \
    "${WORK_DIR}/closure-restart.request.json" '' 202 closure-restart
  CLOSURE_RESTART_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/closure-restart.response.json")
  wait_terminal_resource "${BASE_URL}/api/v1/nginx/restarts/${CLOSURE_RESTART_ID}" closure-restart 90
  if ! jq -e --argjson before "${BEFORE_RESTART_MASTER}" '
    .state == "succeeded" and .stage == "succeeded" and
    .before_master_pid == $before and .after_master_pid != $before and
    .worker_count > 0 and .http_status >= 200 and .http_status < 400
  ' "${TASK_FILE}" >/dev/null; then
    jq -c '{state,stage,before_master_pid,after_master_pid,worker_count,http_status,last_error_code,stages:[.stages[] | {stage,result,code}]}' \
      "${TASK_FILE}" >&2
    printf '[workspace] restart live baseline master: %s\n' "${BEFORE_RESTART_MASTER}" >&2
    fail 'fixed supervised Nginx restart evidence was malformed'
  fi
  AFTER_RESTART_STATUS=$(status_signature "${WORK_DIR}/closure-after-restart-status.json")
  AFTER_RESTART_MASTER=$(printf '%s\n' "${AFTER_RESTART_STATUS}" | jq -er '.master')
  [ "${AFTER_RESTART_MASTER}" = "$(jq -er '.after_master_pid' "${TASK_FILE}")" ] ||
    fail 'fixed restart API evidence did not match the live Nginx master'

  jq -n --arg backup_id "${CLOSURE_BACKUP_ID}" \
    '{attention_case_id:"",reason:"v1.0 final-image restore verification",confirm_backup_id:$backup_id}' \
    >"${WORK_DIR}/closure-restore.request.json"
  api_mutation POST "${BASE_URL}/api/v1/config/backups/${CLOSURE_BACKUP_ID}/restores" \
    "${WORK_DIR}/closure-restore.request.json" '' 202 closure-restore
  CLOSURE_RESTORE_ID=$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' \
    "${WORK_DIR}/closure-restore.response.json")
  wait_terminal_resource "${BASE_URL}/api/v1/config/restores/${CLOSURE_RESTORE_ID}" closure-restore 150
  jq -e --arg backup_id "${CLOSURE_BACKUP_ID}" '
    .state == "succeeded" and .stage == "succeeded" and .target_backup_id == $backup_id and
    any(.stages[]; .stage == "runtime_confirmed" and .result == "success")
  ' "${TASK_FILE}" >/dev/null || fail 'manual restore did not succeed with runtime confirmation'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'manual restore did not recover the original production bytes'
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 30 || fail 'readiness failed after manual restore'

  ROLLBACK_WORKSPACE_NAME="rollback-${RUN_RANDOM}"
  create_workspace "${ROLLBACK_WORKSPACE_NAME}" rollback-create
  ROLLBACK_WORKSPACE_ID=${CREATE_WORKSPACE_ID}
  ROLLBACK_WORKSPACE_ETAG=${CREATE_WORKSPACE_ETAG}
  api_get "${BASE_URL}/api/v1/config/workspaces/${ROLLBACK_WORKSPACE_ID}/files?path=conf.d%2Fsite.conf" \
    "${WORK_DIR}/rollback-site.json"
  jq '{content:(.content + "\nserver { listen 9000; }\n")}' "${WORK_DIR}/rollback-site.json" \
    >"${WORK_DIR}/rollback-site.request.json"
  api_mutation PUT \
    "${BASE_URL}/api/v1/config/workspaces/${ROLLBACK_WORKSPACE_ID}/files?path=conf.d%2Fsite.conf" \
    "${WORK_DIR}/rollback-site.request.json" "${ROLLBACK_WORKSPACE_ETAG}" 200 rollback-site
  ROLLBACK_WORKSPACE_ETAG=$(workspace_etag_from_body "${WORK_DIR}/rollback-site.response.json")
  queue_workspace_release rollback-release "${ROLLBACK_WORKSPACE_ID}" \
    "${ROLLBACK_WORKSPACE_NAME}" "${ROLLBACK_WORKSPACE_ETAG}"
  ROLLBACK_RELEASE_ID=${QUEUED_RELEASE_ID}
  wait_terminal_resource "${BASE_URL}/api/v1/config/releases/${ROLLBACK_RELEASE_ID}" rollback-release 180
  jq -e '
    .state == "rolled_back" and .stage == "rolled_back" and
    any(.stages[]; .stage == "files_applied" and .result == "success") and
    any(.stages[]; .stage == "rollback_files_restored" and .result == "success") and
    any(.stages[]; .stage == "rolled_back" and .result == "warning" and (.code | length) > 0)
  ' "${TASK_FILE}" >/dev/null ||
    fail 'the bounded UI-port conflict did not produce a confirmed automatic rollback'
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'automatic rollback did not restore original production bytes'
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 30 || fail 'readiness failed after automatic rollback'

  verify_closure_history
  verify_certificate_boundary
  verify_closure_recreation
  pass 'release, restart, restore, automatic rollback, certificate boundary, and durable history passed'
}

verify_closure_history() {
  api_get "${BASE_URL}/api/v1/config/history/releases?limit=100" "${WORK_DIR}/closure-release-history.json"
  jq -e --arg committed "${CLOSURE_RELEASE_ID}" --arg rolled_back "${ROLLBACK_RELEASE_ID}" '
    any(.items[]; .id == $committed and .state == "succeeded") and
    any(.items[]; .id == $rolled_back and .state == "rolled_back")
  ' "${WORK_DIR}/closure-release-history.json" >/dev/null || fail 'release history omitted a closure task'
  api_get "${BASE_URL}/api/v1/config/history/restores?limit=100" "${WORK_DIR}/closure-restore-history.json"
  jq -e --arg id "${CLOSURE_RESTORE_ID}" 'any(.items[]; .id == $id and .state == "succeeded")' \
    "${WORK_DIR}/closure-restore-history.json" >/dev/null || fail 'restore history omitted the successful restore'
  api_get "${BASE_URL}/api/v1/config/history/restarts?limit=100" "${WORK_DIR}/closure-restart-history.json"
  jq -e --arg id "${CLOSURE_RESTART_ID}" 'any(.items[]; .id == $id and .state == "succeeded")' \
    "${WORK_DIR}/closure-restart-history.json" >/dev/null || fail 'restart history omitted the fixed restart'
  api_get "${BASE_URL}/api/v1/config/backups?limit=100" "${WORK_DIR}/closure-backups.json"
  jq -e --arg id "${CLOSURE_BACKUP_ID}" \
    'any(.items[]; .id == $id and .state == "complete" and .body_present == true)' \
    "${WORK_DIR}/closure-backups.json" >/dev/null || fail 'backup index omitted the verified release backup'
  api_get "${BASE_URL}/api/v1/route-tests?workspace_id=${CLOSURE_WORKSPACE_ID}&limit=100" \
    "${WORK_DIR}/closure-route-history.json"
  jq -e --arg id "${CLOSURE_ROUTE_RUN_ID}" 'any(.runs[]; .id == $id and .state == "succeeded")' \
    "${WORK_DIR}/closure-route-history.json" >/dev/null || fail 'Route Lab history omitted the isolated run'
  api_get "${BASE_URL}/api/v1/config/audit-events?limit=100" "${WORK_DIR}/closure-audit.json"
  jq -e --arg release "${ROLLBACK_RELEASE_ID}" --arg restore "${CLOSURE_RESTORE_ID}" \
    --arg restart "${CLOSURE_RESTART_ID}" '
    any(.items[]; .object_id == $release) and
    any(.items[]; .object_id == $restore) and
    any(.items[]; .object_id == $restart) and
    all(.items[]; (.details | type) == "object")
  ' "${WORK_DIR}/closure-audit.json" >/dev/null || fail 'redacted audit history omitted a closure operation'
}

verify_certificate_boundary() {
  api_get "${BASE_URL}/api/v1/certificates?limit=100" "${WORK_DIR}/closure-certificates.json"
  jq -e '.certificates == []' "${WORK_DIR}/closure-certificates.json" >/dev/null ||
    fail 'final-image certificate inventory was not initially empty'
  certificate_directories=$(docker exec "${MAIN_CONTAINER}" /bin/sh -eu -c '
    for path in /var/lib/nginx-uix/certs /var/lib/nginx-uix/certs/accounts \
      /var/lib/nginx-uix/certs/credentials /var/lib/nginx-uix/certs/certificates \
      /var/lib/nginx-uix/certs/staging; do
      stat -c "%u:%g:%a" "$path"
    done
  ')
  [ "$(printf '%s\n' "${certificate_directories}" | sed -n '/^10001:10001:700$/p' | wc -l | tr -d ' ')" = 5 ] ||
    fail 'certificate vault directories do not all use UID/GID 10001 and mode 0700'
  [ "$(docker exec "${MAIN_CONTAINER}" stat -c '%u:%g:%a:%s' /var/lib/nginx-uix/certs/master.key)" = \
    '10001:10001:600:32' ] || fail 'certificate vault master key does not use the required owner, mode, and size'
}

verify_closure_recreation() {
  stop_main_container 'closure history persistence recreation'
  start_main_container "${IMAGE}"
  login closure-recreate-login
  wait_ready "${MAIN_CONTAINER}" "${BASE_URL}" 30 || fail 'recreated closure container was not ready'
  verify_closure_history
  verify_certificate_boundary
  [ "$(fixture_digest)" = "${FIXTURE_DIGEST}" ] ||
    fail 'production bytes changed across final closure recreation'
}

validate_inputs() {
  case "${BUILD_IMAGE}" in auto|0|1) ;; *) fail 'BUILD_IMAGE must be auto, 0, or 1' ;; esac
  case "${WORKSPACE_PROFILE}" in
    full|closure|repeat|stability) ;;
    *) fail 'WORKSPACE_PROFILE must be full, closure, repeat, or stability' ;;
  esac
  if [ "${WORKSPACE_PROFILE}" = repeat ]; then
    [ "${REPEAT_TOTAL_ROUNDS}" = 10 ] ||
      fail 'repeat profile requires REPEAT_TOTAL_ROUNDS=10'
    case "${REPEAT_BATCH}" in 1|2) ;; *) fail 'repeat profile requires REPEAT_BATCH=1 or REPEAT_BATCH=2' ;; esac
    [ "$((REPEAT_BATCH_ROUNDS * 2))" = "${REPEAT_TOTAL_ROUNDS}" ] ||
      fail 'repeat batch definition must total ten rounds'
  fi
  [ "$((SOAK_INTERVAL_SECONDS * SOAK_EXPECTED_SAMPLES))" = "${SOAK_DURATION_SECONDS}" ] ||
    fail 'stability sample definition must total the fixed duration'
  case "${PLATFORM:-}" in ''|linux/amd64|linux/arm64) ;; *) fail 'PLATFORM must be linux/amd64 or linux/arm64' ;; esac
  for required_command in docker curl openssl git go jq sqlite3 tar awk sed grep cmp date uname stat chmod; do
    require_command "${required_command}"
  done
  [ "${PROJECT_VERSION}" = 1.0.0 ] || fail 'workspace acceptance requires project VERSION 1.0.0'
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
    fail 'v1.0 release image identity could not be ensured'
  pass 'v1.0 image has the exact deterministic source/platform identity'
  seed_fixture_volume
  FIXTURE_DIGEST=$(fixture_digest)
  [ -n "${FIXTURE_DIGEST}" ] || fail 'fixed production fixture digest is empty'

  if [ "${WORKSPACE_PROFILE}" = repeat ]; then
    start_main_container "${IMAGE}"
    login repeat-profile-login
    sanitize_release_fixture
    exercise_repeated_release_restore
    capture_logs "${MAIN_CONTAINER}" "${WORK_DIR}/repeat-final.log"
    pass 'focused repeated publication and recovery acceptance completed'
    return
  fi

  if [ "${WORKSPACE_PROFILE}" = stability ]; then
    start_main_container "${IMAGE}"
    login stability-profile-login
    sanitize_release_fixture
    exercise_structured_and_route_lab
    exercise_stability_soak
    pass 'focused continuous stability acceptance completed'
    return
  fi

  if [ "${WORKSPACE_PROFILE}" = closure ]; then
    start_main_container "${IMAGE}"
    login closure-profile-login
    BASELINE_STATUS=$(status_signature "${WORK_DIR}/closure-profile-status.json")
    sanitize_release_fixture
    exercise_structured_and_route_lab
    exercise_release_recovery_closure
    capture_logs "${MAIN_CONTAINER}" "${WORK_DIR}/main-final.log"
    pass 'focused workspace release and recovery acceptance completed'
    return
  fi

  ensure_v01_image
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
  sanitize_release_fixture
  exercise_structured_and_route_lab
  exercise_release_recovery_closure

  capture_logs "${MAIN_CONTAINER}" "${WORK_DIR}/main-final.log"
  pass 'workspace upgrade, persistence, safety, fault, browser, release and recovery acceptance completed'
}

main "$@"
