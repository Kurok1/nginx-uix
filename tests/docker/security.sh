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
RUN_RANDOM=$(openssl rand -hex 4)
RUN_ID="security-$$-${RUN_RANDOM}"
LABEL="io.nginx-uix.security-run=${RUN_ID}"
RESOURCE_PREFIX="nginx-uix-${RUN_ID}"
WORK_DIR="${TMPDIR:-/tmp}/nginx-uix-${RUN_ID}"
ADMIN_USERNAME="security-admin-${RUN_RANDOM}"
REQUIRED_CAPABILITIES='CHOWN DAC_OVERRIDE FOWNER KILL SETGID SETUID'

OWNED_CONTAINERS=
OWNED_VOLUMES=
WORK_DIR_CREATED=0
ACTIVE_COMMAND_PID=

log() {
  printf '[security] %s\n' "$1"
}

pass() {
  printf '[security] PASS: %s\n' "$1"
}

fail() {
  printf '[security] ERROR: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is unavailable: $1"
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
      "${TMPDIR:-/tmp}"/nginx-uix-security-*) rm -rf "${WORK_DIR}" ;;
      *) printf '[security] refusing unsafe cleanup path: %s\n' "${WORK_DIR}" >&2 ;;
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
  ready_container=$1
  ready_url=$2
  ready_limit=$3
  ready_deadline=$(( $(date +%s) + ready_limit ))
  while [ "$(date +%s)" -lt "${ready_deadline}" ]; do
    [ "$(docker inspect --format '{{.State.Running}}' "${ready_container}" 2>/dev/null || true)" = true ] || {
      sleep 1
      continue
    }
    if [ -n "${ready_url}" ]; then
      ready_code=$(curl --silent --connect-timeout 1 --max-time 2 --output /dev/null \
        --write-out '%{http_code}' "${ready_url}/health/ready" || true)
      [ "${ready_code}" = 200 ] && return 0
    elif docker exec "${ready_container}" /usr/local/bin/nginx-uix healthcheck >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

container_url() {
  mapped_port=$(docker port "$1" 9000/tcp | sed -n 's/^127\.0\.0\.1:\([0-9][0-9]*\)$/\1/p')
  case "${mapped_port}" in
    ''|*[!0-9]*) fail 'UI port is not mapped only to the host loopback address' ;;
  esac
  printf 'http://127.0.0.1:%s\n' "${mapped_port}"
}

run_with_capabilities() {
  capability_container=$1
  capability_config=$2
  capability_data=$3
  capability_omitted=$4
  capability_publish=$5

  set -- docker run --detach --name "${capability_container}" --label "${LABEL}" \
    --cap-drop ALL \
    --mount "type=volume,src=${capability_config},dst=/etc/nginx" \
    --mount "type=volume,src=${capability_data},dst=/var/lib/nginx-uix" \
    --mount "type=bind,src=${WORK_DIR}/admin-password,dst=/run/secrets/nginx-uix-admin,readonly" \
    --env "NGINX_UIX_ADMIN_USERNAME=${ADMIN_USERNAME}" \
    --env 'NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin'
  [ "${capability_publish}" = 1 ] && set -- "$@" --publish '127.0.0.1::9000'
  [ -z "${PLATFORM}" ] || set -- "$@" --platform "${PLATFORM}"
  for capability_name in ${REQUIRED_CAPABILITIES}; do
    [ "${capability_name}" = "${capability_omitted}" ] || set -- "$@" --cap-add "${capability_name}"
  done
  set -- "$@" "${IMAGE}"
  run_bounded 30 "${WORK_DIR}/${capability_container}.run.log" "$@" ||
    fail "could not create capability case ${capability_omitted:-complete}"
}

assert_container_contract() {
  contract_container=$1
  docker inspect "${contract_container}" >"${WORK_DIR}/baseline-inspect.json"
  if ! jq -e --argjson expected '["CAP_CHOWN","CAP_DAC_OVERRIDE","CAP_FOWNER","CAP_KILL","CAP_SETGID","CAP_SETUID"]' '
    .[0] as $container |
    ($container.HostConfig.Privileged == false) and
    (($container.HostConfig.CapDrop | sort) == ["ALL"]) and
    (($container.HostConfig.CapAdd | sort) == ($expected | sort)) and
    ($container.HostConfig.PidMode != "host") and
    ($container.HostConfig.NetworkMode != "host") and
    (($container.HostConfig.Devices // []) | length == 0) and
    (all($container.Mounts[];
      (.Source | contains("docker.sock") | not) and
      (.Destination | contains("docker.sock") | not))) and
    (($container.NetworkSettings.Ports["9000/tcp"] | length) == 1) and
    ($container.NetworkSettings.Ports["9000/tcp"][0].HostIp == "127.0.0.1") and
    ($container.NetworkSettings.Ports["80/tcp"] == null) and
    ($container.NetworkSettings.Ports["443/tcp"] == null)
  ' "${WORK_DIR}/baseline-inspect.json" >/dev/null; then
    jq '.[0] | {
      privileged:.HostConfig.Privileged,
      cap_add:.HostConfig.CapAdd,
      cap_drop:.HostConfig.CapDrop,
      pid_mode:.HostConfig.PidMode,
      network_mode:.HostConfig.NetworkMode,
      devices:.HostConfig.Devices,
      ports:.NetworkSettings.Ports,
      mounts:[.Mounts[] | {type:.Type,destination:.Destination,rw:.RW}]
    }' "${WORK_DIR}/baseline-inspect.json" >&2
    fail 'container host settings exceed the documented security boundary'
  fi

  docker exec "${contract_container}" /bin/sh -eu -c '
    find_one() {
      prefix=$1
      found=
      count=0
      for process in /proc/[0-9]*; do
        [ -r "${process}/cmdline" ] || continue
        command=$(tr "\000" " " <"${process}/cmdline" 2>/dev/null || true)
        case "${command}" in
          "${prefix}"*) found=${process#/proc/}; count=$((count + 1)) ;;
        esac
      done
      [ "${count}" -eq 1 ]
      printf "%s\n" "${found}"
    }
    uid_of() { awk "\$1 == \"Uid:\" {print \$2}" "/proc/$1/status"; }
    gid_of() { awk "\$1 == \"Gid:\" {print \$2}" "/proc/$1/status"; }
    groups_of() { awk "\$1 == \"Groups:\" {\$1=\"\"; sub(/^ /, \"\"); print}" "/proc/$1/status"; }

    ui=$(find_one "/usr/local/bin/nginx-uix serve ")
    agent=$(find_one "/usr/local/bin/nginx-uix-agent serve ")
    master=$(find_one "nginx: master process ")
    [ "$(uid_of 1)" = 0 ]
    [ "$(uid_of "${ui}")" = 10001 ]
    [ "$(gid_of "${ui}")" = 10001 ]
    case " $(groups_of "${ui}") " in *" 10002 "*) ;; *) exit 1 ;; esac
    [ "$(uid_of "${agent}")" = 0 ]
    [ "$(uid_of "${master}")" = 0 ]
    [ "$(stat -c "%a:%u:%g" /run/nginx-uix/agent.sock)" = 660:0:10002 ]
    [ "$(stat -c "%a:%u:%g" /var/lib/nginx-uix)" = 700:10001:10001 ]
    [ "$(stat -c "%a:%u:%g" /var/lib/nginx-uix/nginx-uix.db)" = 600:10001:10001 ]
    for directory in /var/lib/nginx-uix/certs /var/lib/nginx-uix/certs/accounts \
      /var/lib/nginx-uix/certs/credentials /var/lib/nginx-uix/certs/certificates \
      /var/lib/nginx-uix/certs/staging; do
      [ "$(stat -c "%a:%u:%g" "${directory}")" = 700:10001:10001 ]
    done
    [ "$(stat -c "%a:%u:%g:%s" /var/lib/nginx-uix/certs/master.key)" = 600:10001:10001:32 ]
    grep -q "/run/nginx-uix/agent.sock$" /proc/net/unix
  ' || fail 'process identities or persistent-file permissions differ from the contract'

  docker exec --user 10001:10002 "${contract_container}" /usr/bin/timeout 5 \
    /command/s6-ipcclient /run/nginx-uix/agent.sock /bin/sh -ec '
      printf "GET /v1/health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" >&7
      IFS= read -r status <&6
      case "${status}" in "HTTP/1.1 200"*) exit 0 ;; *) exit 1 ;; esac
    ' >/dev/null || fail 'the UI UID and Agent group could not call the fixed Unix Socket'

  if docker exec --user 65534:10002 "${contract_container}" /usr/bin/timeout 5 \
    /command/s6-ipcclient /run/nginx-uix/agent.sock /bin/sh -ec '
      printf "GET /v1/health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" >&7
      IFS= read -r status <&6
      case "${status}" in "HTTP/1.1 200"*) exit 0 ;; *) exit 1 ;; esac
    ' >/dev/null 2>&1; then
    fail 'Agent peer credentials accepted a non-UI UID from the allowed filesystem group'
  fi
  if docker exec --user 65534:65534 "${contract_container}" /usr/bin/timeout 5 \
    /command/s6-ipcclient /run/nginx-uix/agent.sock /bin/true >/dev/null 2>&1; then
    fail 'a non-group UID connected to the Agent socket'
  fi
}

verify_capability_necessity() {
  for omitted_capability in ${REQUIRED_CAPABILITIES}; do
    omission_slug=$(printf '%s' "${omitted_capability}" | tr 'A-Z_' 'a-z-')
    omission_container="${RESOURCE_PREFIX}-without-${omission_slug}"
    omission_config="${RESOURCE_PREFIX}-without-${omission_slug}-config"
    omission_data="${RESOURCE_PREFIX}-without-${omission_slug}-data"
    register_container "${omission_container}"
    create_volume "${omission_config}"
    create_volume "${omission_data}"
    run_with_capabilities "${omission_container}" "${omission_config}" "${omission_data}" \
      "${omitted_capability}" 0

    if [ "${omitted_capability}" = KILL ]; then
      wait_ready "${omission_container}" '' 30 ||
        fail 'the KILL omission case did not reach the lifecycle phase under test'
      run_bounded 15 "${WORK_DIR}/${omission_container}.stop.log" \
        docker stop --time 5 "${omission_container}" >/dev/null || true
      omission_exit=$(docker inspect --format '{{.State.ExitCode}}' "${omission_container}")
      [ "${omission_exit}" != 0 ] ||
        fail 'KILL was not demonstrated necessary for bounded cross-UID shutdown'
    else
      if wait_ready "${omission_container}" '' 25; then
        fail "container unexpectedly became ready without ${omitted_capability}"
      fi
    fi
    docker logs --tail 120 "${omission_container}" >"${WORK_DIR}/${omission_slug}.log" 2>&1 || true
    pass "removing ${omitted_capability} breaks its required startup or lifecycle responsibility"
  done
}

validate_inputs() {
  case "${BUILD_IMAGE}" in auto|0|1) ;; *) fail 'BUILD_IMAGE must be auto, 0, or 1' ;; esac
  case "${PLATFORM}" in ''|linux/amd64|linux/arm64) ;; *) fail 'PLATFORM must be empty, linux/amd64, or linux/arm64' ;; esac
  for required_command in docker curl openssl jq awk sed grep tr date git go; do
    require_command "${required_command}"
  done
  [ "${PROJECT_VERSION}" = 1.0.0 ] || fail 'security acceptance requires VERSION 1.0.0'
  docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'
}

main() {
  validate_inputs
  [ ! -e "${WORK_DIR}" ] && [ ! -L "${WORK_DIR}" ] || fail 'run-specific work directory exists'
  mkdir "${WORK_DIR}"
  WORK_DIR_CREATED=1
  chmod 0700 "${WORK_DIR}"
  openssl rand -hex 24 >"${WORK_DIR}/admin-password"
  chmod 0444 "${WORK_DIR}/admin-password"

  ensure_test_image "${IMAGE}" "${BUILD_IMAGE}" "${PLATFORM}" ||
    fail 'release image identity could not be ensured'
  pass 'release image has the exact deterministic source and platform identity'

  baseline_container="${RESOURCE_PREFIX}-baseline"
  baseline_config="${RESOURCE_PREFIX}-baseline-config"
  baseline_data="${RESOURCE_PREFIX}-baseline-data"
  register_container "${baseline_container}"
  create_volume "${baseline_config}"
  create_volume "${baseline_data}"
  run_with_capabilities "${baseline_container}" "${baseline_config}" "${baseline_data}" '' 1
  baseline_url=$(container_url "${baseline_container}")
  wait_ready "${baseline_container}" "${baseline_url}" 60 ||
    fail 'minimal-capability baseline did not become ready'
  assert_container_contract "${baseline_container}"
  pass 'non-root UI, root Agent, Unix Socket, loopback mapping, mounts and strict file modes passed'

  run_bounded 25 "${WORK_DIR}/baseline-stop.log" docker stop --time 15 "${baseline_container}" ||
    fail 'minimal-capability baseline did not stop within its bound'
  [ "$(docker inspect --format '{{.State.ExitCode}}' "${baseline_container}")" = 0 ] ||
    fail 'minimal-capability baseline did not exit cleanly'
  pass 'the six-capability baseline starts, serves, and stops cleanly'

  verify_capability_necessity
  pass 'exact capability set is CHOWN,DAC_OVERRIDE,FOWNER,KILL,SETGID,SETUID; NET_BIND_SERVICE is not required on this native daemon'
  printf '\nDocker security and minimum-capability acceptance: PASS\n'
}

main "$@"
