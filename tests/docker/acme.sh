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
PEBBLE_IMAGE='ghcr.io/letsencrypt/pebble@sha256:ddf230642b1a584f519f32e347de1b05a6e4c1f6c35c1863b33effeab5f78199'
PEBBLE_REVISION='b1e1ca4f3c30abb64111adaca4544bc5374cc306'
STAGING_ACME_HOST='acme-staging-v02.api.letsencrypt.org'
PRODUCTION_ACME_HOST='acme-v02.api.letsencrypt.org'

RUN_RANDOM=$(openssl rand -hex 4)
RUN_ID="acme-$$-${RUN_RANDOM}"
LABEL="io.nginx-uix.acme-run=${RUN_ID}"
RESOURCE_PREFIX="nginx-uix-${RUN_ID}"
NETWORK="${RESOURCE_PREFIX}-network"
CONFIG_VOLUME="${RESOURCE_PREFIX}-config"
DATA_VOLUME="${RESOURCE_PREFIX}-data"
APPLICATION_CONTAINER="${RESOURCE_PREFIX}-application"
PEBBLE_CONTAINER="${RESOURCE_PREFIX}-pebble"
PROXY_CONTAINER="${RESOURCE_PREFIX}-proxy"
ASSET_CONTAINER="${RESOURCE_PREFIX}-assets"
SEED_CONTAINER="${RESOURCE_PREFIX}-seed"
WORK_DIR="${TMPDIR:-/tmp}/nginx-uix-${RUN_ID}"
ADMIN_USERNAME="acme-admin-${RUN_RANDOM}"
IDENTIFIER="acme-${RUN_RANDOM}.example.test"

OWNED_CONTAINERS=
OWNED_VOLUMES=
NETWORK_CREATED=0
WORK_DIR_CREATED=0
ACTIVE_COMMAND_PID=
BASE_URL=
CSRF_TOKEN=
HTTP_CODE=

log() {
  printf '[acme] %s\n' "$1"
}

pass() {
  printf '[acme] PASS: %s\n' "$1"
}

fail() {
  printf '[acme] ERROR: %s\n' "$1" >&2
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
    if [ "${NETWORK_CREATED}" = 1 ]; then
      docker network rm "${NETWORK}" >/dev/null 2>&1 || true
    fi
  fi
  if [ "${BUILDX_CACHE_LOCK_OWNED:-0}" = 1 ]; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
  fi
  if [ "${WORK_DIR_CREATED}" = 1 ]; then
    case "${WORK_DIR}" in
      "${TMPDIR:-/tmp}"/nginx-uix-acme-*) rm -rf "${WORK_DIR}" ;;
      *) printf '[acme] refusing unsafe cleanup path: %s\n' "${WORK_DIR}" >&2 ;;
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

container_url() {
  mapped_port=$(docker port "$1" 9000/tcp | sed -n 's/^127\.0\.0\.1:\([0-9][0-9]*\)$/\1/p')
  case "${mapped_port}" in
    ''|*[!0-9]*) fail 'UI port is not mapped only to the host loopback address' ;;
  esac
  printf 'http://127.0.0.1:%s\n' "${mapped_port}"
}

wait_ready() {
  ready_deadline=$(( $(date +%s) + 90 ))
  while [ "$(date +%s)" -lt "${ready_deadline}" ]; do
    if [ "$(docker inspect --format '{{.State.Running}}' "${APPLICATION_CONTAINER}" 2>/dev/null || true)" = true ]; then
      ready_code=$(curl --silent --connect-timeout 1 --max-time 2 --output /dev/null \
        --write-out '%{http_code}' "${BASE_URL}/health/ready" || true)
      [ "${ready_code}" = 200 ] && return 0
    fi
    sleep 1
  done
  return 1
}

safe_error_code() {
  safe_file=$1
  jq -r '.error.code // "unavailable"' "${safe_file}" 2>/dev/null || printf 'unavailable\n'
}

assert_safe_response() {
  safe_file=$1
  [ -f "${safe_file}" ] || return 0
  if grep -F "${ADMIN_PASSWORD}" "${safe_file}" >/dev/null 2>&1 ||
    grep -Eq 'BEGIN (EC |RSA )?PRIVATE KEY|key_authorization|dns_txt_value' "${safe_file}"; then
    fail 'a response artifact contains secret material'
  fi
}

print_application_diagnostics() {
  diagnostic_file="${WORK_DIR}/application-diagnostic.log"
  docker logs --tail 300 "${APPLICATION_CONTAINER}" >"${diagnostic_file}" 2>&1 || return 0
  if grep -F "${ADMIN_PASSWORD}" "${diagnostic_file}" >/dev/null 2>&1 ||
    grep -Eq 'BEGIN (EC |RSA )?PRIVATE KEY|key_authorization|dns_txt_value' "${diagnostic_file}"; then
    printf '[acme] application diagnostics suppressed because secret-like material was detected\n' >&2
    return 0
  fi
  grep -Ei 'certificate|release|rollback|error|failed|failure' "${diagnostic_file}" | tail -80 >&2 || true
}

print_acme_peer_diagnostics() {
  for diagnostic_container in "${PEBBLE_CONTAINER}" "${PROXY_CONTAINER}"; do
    docker container inspect "${diagnostic_container}" >/dev/null 2>&1 || continue
    diagnostic_name=$(basename "${diagnostic_container}")
    diagnostic_file="${WORK_DIR}/${diagnostic_name}-diagnostic.log"
    docker logs --tail 300 "${diagnostic_container}" >"${diagnostic_file}" 2>&1 || continue
    if grep -F "${ADMIN_PASSWORD}" "${diagnostic_file}" >/dev/null 2>&1 ||
      grep -Eqi 'BEGIN (EC |RSA )?PRIVATE KEY|key.?authorization|dns.?txt.?value' "${diagnostic_file}"; then
      printf '[acme] %s diagnostics suppressed because secret-like material was detected\n' \
        "${diagnostic_name}" >&2
      continue
    fi
    printf '[acme] safe %s error diagnostics:\n' "${diagnostic_name}" >&2
    case "${diagnostic_container}" in
      "${PEBBLE_CONTAINER}")
        grep -Ei 'error|fail|problem|invalid|finaliz|panic|Pebble .*(GET|POST|HEAD) /|Order .*status' \
          "${diagnostic_file}" | tail -120 >&2 || true
        ;;
      "${PROXY_CONTAINER}")
        grep -E '"(GET|POST|HEAD) [^ ]+ HTTP/[0-9.]+" [0-9]{3} ' \
          "${diagnostic_file}" | tail -120 >&2 || true
        ;;
    esac
  done
}

print_release_diagnostics() {
  release_file="${WORK_DIR}/release-diagnostic.json"
  diagnostic_code=$(curl --silent --connect-timeout 2 --max-time 10 \
    --output "${release_file}" --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" \
    "${BASE_URL}/api/v1/config/history/releases?limit=10" 2>/dev/null || true)
  [ "${diagnostic_code}" = 200 ] || return 0
  assert_safe_response "${release_file}"
  jq -c '{items:[.items[] | {id,state,stage,last_error_code,request_id,stages}]}' \
    "${release_file}" >&2 || true
  release_id=$(jq -r '.items[0].id // empty' "${release_file}")
  [ "${#release_id}" -eq 32 ] || return 0
  case "${release_id}" in *[!0-9a-f]*) return 0 ;; esac
  release_detail="${WORK_DIR}/release-detail-diagnostic.json"
  diagnostic_code=$(curl --silent --connect-timeout 2 --max-time 10 \
    --output "${release_detail}" --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" \
    "${BASE_URL}/api/v1/config/releases/${release_id}" 2>/dev/null || true)
  [ "${diagnostic_code}" = 200 ] || return 0
  assert_safe_response "${release_detail}"
  jq -c '{id,state,stage,last_error_code,stages:[.stages[] | {sequence,stage,result,code,details}]}' \
    "${release_detail}" >&2 || true
}

login() {
  jq -n --arg username "${ADMIN_USERNAME}" --arg password "${ADMIN_PASSWORD}" \
    '{username:$username,password:$password}' >"${WORK_DIR}/login.request.json"
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 20 \
    --output "${WORK_DIR}/login.response.json" \
    --dump-header "${WORK_DIR}/login.headers" \
    --write-out '%{http_code}' \
    --cookie-jar "${WORK_DIR}/session.cookie" \
    --header "Origin: ${BASE_URL}" \
    --header 'Content-Type: application/json' \
    --data-binary "@${WORK_DIR}/login.request.json" \
    "${BASE_URL}/api/v1/auth/session")
  [ "${HTTP_CODE}" = 200 ] || fail "login returned HTTP ${HTTP_CODE}"
  CSRF_TOKEN=$(jq -er '.csrf_token | select(type == "string" and length > 20)' \
    "${WORK_DIR}/login.response.json")
  assert_safe_response "${WORK_DIR}/login.response.json"
}

api_get() {
  get_url=$1
  get_output=$2
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 30 \
    --output "${get_output}" --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" "${get_url}")
  [ "${HTTP_CODE}" = 200 ] || fail "authenticated GET returned HTTP ${HTTP_CODE}"
  assert_safe_response "${get_output}"
}

api_mutation() {
  mutation_method=$1
  mutation_url=$2
  mutation_body=$3
  mutation_expected=$4
  mutation_prefix=$5
  HTTP_CODE=$(curl --silent --show-error --connect-timeout 2 --max-time 90 \
    --request "${mutation_method}" \
    --output "${WORK_DIR}/${mutation_prefix}.response.json" \
    --dump-header "${WORK_DIR}/${mutation_prefix}.headers" \
    --write-out '%{http_code}' \
    --cookie "${WORK_DIR}/session.cookie" \
    --header "Origin: ${BASE_URL}" \
    --header "X-CSRF-Token: ${CSRF_TOKEN}" \
    --header 'Content-Type: application/json' \
    --data-binary "@${mutation_body}" \
    "${mutation_url}")
  assert_safe_response "${WORK_DIR}/${mutation_prefix}.response.json"
  [ "${HTTP_CODE}" = "${mutation_expected}" ] ||
    fail "${mutation_prefix} returned HTTP ${HTTP_CODE} ($(safe_error_code "${WORK_DIR}/${mutation_prefix}.response.json"))"
}

poll_task() {
  task_id=$1
  task_prefix=$2
  task_deadline=$(( $(date +%s) + 180 ))
  while [ "$(date +%s)" -lt "${task_deadline}" ]; do
    api_get "${BASE_URL}/api/v1/certificate-tasks/${task_id}" "${WORK_DIR}/${task_prefix}.json"
    task_state=$(jq -er '.state' "${WORK_DIR}/${task_prefix}.json")
    case "${task_state}" in
      succeeded) return 0 ;;
      failed|cancelled|needs_attention)
        task_error=$(jq -r '.last_error_code // "unavailable"' "${WORK_DIR}/${task_prefix}.json")
        jq -c '{state,stage,last_error_code,stages:[.stages[] | {sequence,stage,result,code,details}]}' \
          "${WORK_DIR}/${task_prefix}.json" >&2
        print_release_diagnostics
        print_application_diagnostics
        print_acme_peer_diagnostics
        fail "certificate task ${task_prefix} ended in ${task_state} (${task_error})"
        ;;
      queued|running|cancelling) ;;
      *) fail "certificate task ${task_prefix} returned an unknown state" ;;
    esac
    sleep 1
  done
  fail "certificate task ${task_prefix} did not finish within 180 seconds"
}

prepare_pebble_assets() {
  register_container "${ASSET_CONTAINER}"
  run_bounded 30 "${WORK_DIR}/assets-create.log" \
    docker create --name "${ASSET_CONTAINER}" --label "${LABEL}" "${PEBBLE_IMAGE}" ||
    fail 'could not create the stopped Pebble asset container'
  docker cp "${ASSET_CONTAINER}:/test/certs/pebble.minica.pem" "${WORK_DIR}/pebble.minica.pem"
  docker cp "${ASSET_CONTAINER}:/test/certs/pebble.minica.key.pem" "${WORK_DIR}/pebble.minica.key.pem"
  docker cp "${ASSET_CONTAINER}:/test/config/pebble-config.json" "${WORK_DIR}/pebble-default.json"
  docker rm "${ASSET_CONTAINER}" >/dev/null

  chmod 0600 "${WORK_DIR}/pebble.minica.key.pem"
  chmod 0644 "${WORK_DIR}/pebble.minica.pem"
  jq '.pebble.httpPort = 80 | .pebble.retryAfter.authz = 1 | .pebble.retryAfter.order = 1' \
    "${WORK_DIR}/pebble-default.json" >"${WORK_DIR}/pebble.json"

  cat >"${WORK_DIR}/proxy-cert.ext" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:${STAGING_ACME_HOST},DNS:${PRODUCTION_ACME_HOST}
EOF
  openssl req -new -newkey rsa:2048 -nodes \
    -keyout "${WORK_DIR}/proxy-key.pem" \
    -out "${WORK_DIR}/proxy.csr" \
    -subj "/CN=${STAGING_ACME_HOST}" >/dev/null 2>&1
  openssl x509 -req -sha256 -days 2 -set_serial 700 \
    -in "${WORK_DIR}/proxy.csr" \
    -CA "${WORK_DIR}/pebble.minica.pem" \
    -CAkey "${WORK_DIR}/pebble.minica.key.pem" \
    -extfile "${WORK_DIR}/proxy-cert.ext" \
    -out "${WORK_DIR}/proxy-cert.pem" >/dev/null 2>&1
  chmod 0600 "${WORK_DIR}/proxy-key.pem"
  chmod 0644 "${WORK_DIR}/proxy-cert.pem" "${WORK_DIR}/pebble.json"

  cat >"${WORK_DIR}/proxy-nginx.conf" <<EOF
worker_processes 1;
pid /tmp/nginx-uix-acme-proxy.pid;
error_log stderr notice;

events {
    worker_connections 128;
}

http {
    log_format acme '\$remote_addr - - [\$time_local] "\$request" \$status \$body_bytes_sent '
                    'location="\$sent_http_location" upstream_location="\$upstream_http_location"';
    access_log /dev/stdout acme;

    server {
        listen 443 ssl;
        server_name ${STAGING_ACME_HOST} ${PRODUCTION_ACME_HOST};

        ssl_certificate /test-config/proxy-cert.pem;
        ssl_certificate_key /test-config/proxy-key.pem;
        ssl_protocols TLSv1.2 TLSv1.3;

        location = /terms {
            default_type text/plain;
            return 200 "Nginx UIX isolated Pebble terms";
        }

        location = /directory {
            proxy_set_header Host \$host;
            proxy_set_header Accept-Encoding "";
            proxy_ssl_name pebble;
            proxy_ssl_server_name on;
            proxy_ssl_trusted_certificate /test-config/pebble.minica.pem;
            proxy_ssl_verify on;
            proxy_ssl_verify_depth 2;
            proxy_pass https://pebble:14000/dir;
            sub_filter_types application/json;
            sub_filter_once on;
            sub_filter 'data:text/plain,Do%20what%20thou%20wilt' 'https://\$host/terms';
        }

        # Pebble 2.10.1 finalizes asynchronously without returning the order
        # Location that x/crypto/acme needs for its follow-up poll. The order
        # URL is deterministic from this isolated Pebble request path.
        location ~ ^/finalize-order/(?<order_id>[A-Za-z0-9_-]+)$ {
            proxy_set_header Host \$host;
            proxy_ssl_name pebble;
            proxy_ssl_server_name on;
            proxy_ssl_trusted_certificate /test-config/pebble.minica.pem;
            proxy_ssl_verify on;
            proxy_ssl_verify_depth 2;
            proxy_pass https://pebble:14000;
            proxy_hide_header Location;
            add_header Location "https://\$host/my-order/\$order_id" always;
        }

        location / {
            proxy_set_header Host \$host;
            proxy_ssl_name pebble;
            proxy_ssl_server_name on;
            proxy_ssl_trusted_certificate /test-config/pebble.minica.pem;
            proxy_ssl_verify on;
            proxy_ssl_verify_depth 2;
            proxy_pass https://pebble:14000;
        }
    }
}
EOF
  chmod 0644 "${WORK_DIR}/proxy-nginx.conf"
}

seed_configuration() {
  register_container "${SEED_CONTAINER}"
  run_bounded 30 "${WORK_DIR}/seed.log" \
    docker run --rm --name "${SEED_CONTAINER}" --label "${LABEL}" \
      --entrypoint /bin/sh \
      --mount "type=volume,src=${CONFIG_VOLUME},dst=/target" \
      --env "TEST_IDENTIFIER=${IDENTIFIER}" \
      "${IMAGE}" -eu -c '
        test -z "$(find /target -mindepth 1 -print -quit)"
        cp -R /usr/share/nginx-uix/default-nginx/. /target/
        sed -i "s/server_name _;/server_name ${TEST_IDENTIFIER};/" /target/conf.d/default.conf
        grep -F "server_name ${TEST_IDENTIFIER};" /target/conf.d/default.conf >/dev/null
        find /target -type d -exec chmod 0755 "{}" +
        find /target -type f -exec chmod 0644 "{}" +
      ' || fail 'could not seed the isolated HTTP-01 Nginx configuration'
}

start_pebble() {
  register_container "${PEBBLE_CONTAINER}"
  run_bounded 30 "${WORK_DIR}/pebble-start.log" \
    docker run --detach --name "${PEBBLE_CONTAINER}" --label "${LABEL}" \
      --network "${NETWORK}" --network-alias pebble \
      --env PEBBLE_VA_NOSLEEP=1 \
      --env PEBBLE_WFE_NONCEREJECT=0 \
      --env PEBBLE_AUTHZREUSE=0 \
      --mount "type=bind,src=${WORK_DIR}/pebble.json,dst=/test-config/pebble.json,readonly" \
      "${PEBBLE_IMAGE}" -config /test-config/pebble.json || fail 'could not start Pebble'
}

start_proxy() {
  register_container "${PROXY_CONTAINER}"
  run_bounded 30 "${WORK_DIR}/proxy-start.log" \
    docker run --detach --name "${PROXY_CONTAINER}" --label "${LABEL}" \
      --network "${NETWORK}" \
      --network-alias "${STAGING_ACME_HOST}" \
      --network-alias "${PRODUCTION_ACME_HOST}" \
      --entrypoint /usr/sbin/nginx \
      --mount "type=bind,src=${WORK_DIR}/proxy-nginx.conf,dst=/test-config/nginx.conf,readonly" \
      --mount "type=bind,src=${WORK_DIR}/proxy-cert.pem,dst=/test-config/proxy-cert.pem,readonly" \
      --mount "type=bind,src=${WORK_DIR}/proxy-key.pem,dst=/test-config/proxy-key.pem,readonly" \
      --mount "type=bind,src=${WORK_DIR}/pebble.minica.pem,dst=/test-config/pebble.minica.pem,readonly" \
      "${IMAGE}" -c /test-config/nginx.conf -g 'daemon off;' || fail 'could not start the staging directory proxy'

  proxy_deadline=$(( $(date +%s) + 60 ))
  while [ "$(date +%s)" -lt "${proxy_deadline}" ]; do
    proxy_ready=1
    for directory_host in "${STAGING_ACME_HOST}" "${PRODUCTION_ACME_HOST}"; do
      directory_file="/tmp/directory-${directory_host}.json"
      if ! docker exec "${PROXY_CONTAINER}" /bin/sh -c \
        "wget -qO '${directory_file}' --no-check-certificate 'https://${directory_host}/directory'" >/dev/null 2>&1; then
        proxy_ready=0
        break
      fi
      docker cp "${PROXY_CONTAINER}:${directory_file}" "${WORK_DIR}/directory-${directory_host}.json"
      jq -e --arg host "${directory_host}" '
        .meta.termsOfService == ("https://" + $host + "/terms") and
        ([.newAccount, .newNonce, .newOrder] | all(startswith("https://" + $host + "/")))
      ' "${WORK_DIR}/directory-${directory_host}.json" >/dev/null ||
        fail 'proxied Pebble directory escaped a fixed ACME host'
    done
    [ "${proxy_ready}" = 1 ] && return 0
    sleep 1
  done
  fail 'isolated staging and production directory proxy did not become ready'
}

start_application() {
  register_container "${APPLICATION_CONTAINER}"
  run_bounded 30 "${WORK_DIR}/application-start.log" \
    docker run --detach --name "${APPLICATION_CONTAINER}" --label "${LABEL}" \
      --network "${NETWORK}" --network-alias "${IDENTIFIER}" \
      --publish '127.0.0.1::9000' \
      --mount "type=volume,src=${CONFIG_VOLUME},dst=/etc/nginx" \
      --mount "type=volume,src=${DATA_VOLUME},dst=/var/lib/nginx-uix" \
      --mount "type=bind,src=${WORK_DIR}/admin-password,dst=/run/secrets/nginx-uix-admin,readonly" \
      --mount "type=bind,src=${WORK_DIR}/pebble.minica.pem,dst=/run/nginx-uix-test/pebble-ca.pem,readonly" \
      --env "NGINX_UIX_ADMIN_USERNAME=${ADMIN_USERNAME}" \
      --env 'NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin' \
      --env 'SSL_CERT_FILE=/run/nginx-uix-test/pebble-ca.pem' \
      "${IMAGE}" || fail 'could not start the Nginx UIX application container'
  BASE_URL=$(container_url "${APPLICATION_CONTAINER}")
  wait_ready || fail 'application did not become ready with the isolated ACME network'
}

remove_application() {
  docker rm "${APPLICATION_CONTAINER}" >/dev/null
  updated_containers=
  for owned_container in ${OWNED_CONTAINERS}; do
    [ "${owned_container}" = "${APPLICATION_CONTAINER}" ] ||
      updated_containers="${updated_containers} ${owned_container}"
  done
  OWNED_CONTAINERS=${updated_containers}
}

verify_material() {
  verify_certificate_id=$1
  verify_version_id=$2
  verify_task_id=$3
  docker exec --env "TEST_CERTIFICATE_ID=${verify_certificate_id}" \
    --env "TEST_VERSION_ID=${verify_version_id}" \
    --env "TEST_TASK_ID=${verify_task_id}" \
    --env "TEST_IDENTIFIER=${IDENTIFIER}" \
    "${APPLICATION_CONTAINER}" /bin/sh -eu -c '
      material_root="/var/lib/nginx-uix/certs/certificates/${TEST_CERTIFICATE_ID}/versions/${TEST_VERSION_ID}"
      for material_name in fullchain.pem leaf.pem privkey.pem; do
        test -f "${material_root}/${material_name}"
        test ! -L "${material_root}/${material_name}"
        test "$(stat -c %a "${material_root}/${material_name}")" = 600
      done
      test "$(stat -c %a "${material_root}")" = 700
      test ! -e "/etc/nginx/nginx-uix-acme-${TEST_TASK_ID}.conf"
      ! grep -F "/etc/nginx/nginx-uix-acme-${TEST_TASK_ID}.conf" /etc/nginx/conf.d/default.conf >/dev/null
      grep -F "ssl_certificate ${material_root}/fullchain.pem;" /etc/nginx/conf.d/default.conf >/dev/null
      grep -F "ssl_certificate_key ${material_root}/privkey.pem;" /etc/nginx/conf.d/default.conf >/dev/null
      /usr/sbin/nginx -t -c /etc/nginx/nginx.conf >/dev/null
    ' || fail 'certificate material, permissions, binding, cleanup, SAN, or nginx -t is invalid'
  verify_leaf_path="${WORK_DIR}/leaf-${verify_version_id}.pem"
  [ ! -e "${verify_leaf_path}" ] || fail 'run-owned leaf certificate path already exists'
  docker cp \
    "${APPLICATION_CONTAINER}:/var/lib/nginx-uix/certs/certificates/${verify_certificate_id}/versions/${verify_version_id}/leaf.pem" \
    "${verify_leaf_path}" >/dev/null || fail 'could not copy the public leaf certificate for host-side verification'
  chmod 0600 "${verify_leaf_path}"
  openssl x509 -in "${verify_leaf_path}" -noout -ext subjectAltName |
    grep -F "DNS:${IDENTIFIER}" >/dev/null || fail 'issued certificate SAN does not match the isolated identifier'
}

for required_command in docker curl jq openssl go git sed grep; do
  require_command "${required_command}"
done

PLATFORM=$(normalize_image_platform "${PLATFORM}") || fail 'native platform is unsupported'
ensure_test_image "${IMAGE}" "${BUILD_IMAGE}" "${PLATFORM}" ||
  fail 'release image identity could not be ensured'
pass 'release image has the exact deterministic source and native platform identity'

mkdir "${WORK_DIR}"
WORK_DIR_CREATED=1
chmod 0700 "${WORK_DIR}"

if ! docker image inspect "${PEBBLE_IMAGE}" >/dev/null 2>&1; then
  run_bounded 180 "${WORK_DIR}/pebble-pull.log" docker pull "${PEBBLE_IMAGE}" ||
    fail 'fixed Pebble image is unavailable'
fi
pebble_identity=$(docker image inspect --format \
  '{{.Os}}/{{.Architecture}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{index .Config.Labels "org.opencontainers.image.source"}}' \
  "${PEBBLE_IMAGE}")
[ "${pebble_identity}" = "${PLATFORM}|${PEBBLE_REVISION}|https://github.com/letsencrypt/pebble" ] ||
  fail 'fixed Pebble image has an unexpected platform or source identity'

ADMIN_PASSWORD="Acme-${RUN_RANDOM}-$(openssl rand -hex 12)"
printf '%s\n' "${ADMIN_PASSWORD}" >"${WORK_DIR}/admin-password"
chmod 0600 "${WORK_DIR}/admin-password"

prepare_pebble_assets
docker network inspect "${NETWORK}" >/dev/null 2>&1 && fail 'owned network name already exists'
docker network create --label "${LABEL}" "${NETWORK}" >/dev/null
NETWORK_CREATED=1
create_volume "${CONFIG_VOLUME}"
create_volume "${DATA_VOLUME}"
seed_configuration
start_pebble
start_proxy
start_application
login

jq -n --arg email "operator-${RUN_RANDOM}@example.com" \
  '{environment:"staging",email:$email,terms_of_service_agreed:true}' \
  >"${WORK_DIR}/staging-account.request.json"
api_mutation POST "${BASE_URL}/api/v1/acme/accounts" \
  "${WORK_DIR}/staging-account.request.json" 201 staging-account
STAGING_ACCOUNT_ID=$(jq -er --arg directory "https://${STAGING_ACME_HOST}/directory" '
  select(.environment == "staging" and .directory_url == $directory and .status == "valid") |
  .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/staging-account.response.json")
pass 'final container registered a protected account through the fixed staging directory policy'

api_get "${BASE_URL}/api/v1/certificate-server-candidates" "${WORK_DIR}/candidates.json"
jq -e --arg identifier "${IDENTIFIER}" '
  [.candidates[] | select(.editable == true and (.ref.server_names | index($identifier)))] | length == 1
' "${WORK_DIR}/candidates.json" >/dev/null || fail 'isolated HTTP server candidate is missing or ambiguous'
jq --arg identifier "${IDENTIFIER}" --arg account_id "${STAGING_ACCOUNT_ID}" '
  {
    identifiers:[$identifier],
    challenge:"http_01",
    account_id:$account_id,
    server_refs:[.candidates[] | select(.editable == true and (.ref.server_names | index($identifier))) | .ref]
  }
' "${WORK_DIR}/candidates.json" >"${WORK_DIR}/staging-plan.request.json"
api_mutation POST "${BASE_URL}/api/v1/certificate-order-plans" \
  "${WORK_DIR}/staging-plan.request.json" 201 staging-plan
STAGING_PLAN_ID=$(jq -er --arg identifier "${IDENTIFIER}" --arg account_id "${STAGING_ACCOUNT_ID}" '
  select(
    .state == "planned" and .environment == "staging" and .challenge == "http_01" and
    .account_id == $account_id and .primary_identifier == $identifier and
    .staging_evidence == false and .requires_risk_confirmation == false
  ) | .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/staging-plan.response.json")

jq -n --arg confirmation "${IDENTIFIER}" \
  '{confirmation:$confirmation,production_risk_confirmation:""}' \
  >"${WORK_DIR}/staging-execute.request.json"
api_mutation POST "${BASE_URL}/api/v1/certificate-order-plans/${STAGING_PLAN_ID}/executions" \
  "${WORK_DIR}/staging-execute.request.json" 202 staging-execute
STAGING_TASK_ID=$(jq -er '
  select(.kind == "issue" and .state == "queued" and .challenge == "http_01") |
  .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/staging-execute.response.json")
poll_task "${STAGING_TASK_ID}" staging-task
jq -e '
  .kind == "issue" and .state == "succeeded" and .stage == "completed" and
  ((.release_id // "") == "") and
  ([.stages[].stage] | index("authorizing") != null) and
  ([.stages[].stage] | index("cleaning") != null) and
  ([.stages[].stage] | index("finalizing") != null) and
  ([.stages[].stage] | index("validating") != null) and
  ([.stages[].stage] | index("deploying") == null)
' "${WORK_DIR}/staging-task.json" >/dev/null || fail 'staging task lacks complete preflight-only ACME evidence'
pass 'real isolated staging HTTP-01 authorization and finalization produced reusable preflight evidence'

jq -n --arg email "production-${RUN_RANDOM}@example.com" \
  '{environment:"production",email:$email,terms_of_service_agreed:true}' \
  >"${WORK_DIR}/production-account.request.json"
api_mutation POST "${BASE_URL}/api/v1/acme/accounts" \
  "${WORK_DIR}/production-account.request.json" 201 production-account
PRODUCTION_ACCOUNT_ID=$(jq -er --arg directory "https://${PRODUCTION_ACME_HOST}/directory" '
  select(.environment == "production" and .directory_url == $directory and .status == "valid") |
  .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/production-account.response.json")
pass 'fixed production directory policy was exercised only through the isolated Pebble alias'

jq --arg identifier "${IDENTIFIER}" \
  --arg account_id "${PRODUCTION_ACCOUNT_ID}" \
  --arg staging_account_id "${STAGING_ACCOUNT_ID}" '
  {
    identifiers:[$identifier],
    challenge:"http_01",
    account_id:$account_id,
    staging_account_id:$staging_account_id,
    server_refs:[.candidates[] | select(.editable == true and (.ref.server_names | index($identifier))) | .ref]
  }
' "${WORK_DIR}/candidates.json" >"${WORK_DIR}/production-plan.request.json"
api_mutation POST "${BASE_URL}/api/v1/certificate-order-plans" \
  "${WORK_DIR}/production-plan.request.json" 201 production-plan
PRODUCTION_PLAN_ID=$(jq -er \
  --arg identifier "${IDENTIFIER}" \
  --arg account_id "${PRODUCTION_ACCOUNT_ID}" \
  --arg staging_account_id "${STAGING_ACCOUNT_ID}" '
  select(
    .state == "planned" and .environment == "production" and .challenge == "http_01" and
    .account_id == $account_id and .staging_account_id == $staging_account_id and
    .primary_identifier == $identifier and .staging_evidence == true and
    .requires_risk_confirmation == false
  ) | .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/production-plan.response.json")

jq -n --arg confirmation "${IDENTIFIER}" \
  '{confirmation:$confirmation,production_risk_confirmation:""}' \
  >"${WORK_DIR}/production-execute.request.json"
api_mutation POST "${BASE_URL}/api/v1/certificate-order-plans/${PRODUCTION_PLAN_ID}/executions" \
  "${WORK_DIR}/production-execute.request.json" 202 production-execute
ISSUE_TASK_ID=$(jq -er '
  select(.kind == "issue" and .state == "queued" and .challenge == "http_01") |
  .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/production-execute.response.json")
poll_task "${ISSUE_TASK_ID}" issue-task
jq -e '
  .kind == "issue" and .state == "succeeded" and .stage == "completed" and
  (.certificate_id | test("^[0-9a-f]{32}$")) and
  (.version_id | test("^[0-9a-f]{32}$")) and
  (.release_id | test("^[0-9a-f]{32}$")) and
  ([.stages[].stage] | index("authorizing") != null) and
  ([.stages[].stage] | index("finalizing") != null) and
  ([.stages[].stage] | index("deploying") != null) and
  ([.stages[].stage] | index("cleaning") != null)
' "${WORK_DIR}/issue-task.json" >/dev/null || fail 'issuance task lacks the complete durable ACME stage evidence'
CERTIFICATE_ID=$(jq -er '.certificate_id' "${WORK_DIR}/issue-task.json")
ISSUE_VERSION_ID=$(jq -er '.version_id' "${WORK_DIR}/issue-task.json")
verify_material "${CERTIFICATE_ID}" "${ISSUE_VERSION_ID}" "${ISSUE_TASK_ID}"
pass 'isolated production-path HTTP-01 issuance, immutable material, binding, reload and cleanup passed'

jq -n --arg confirmation "${IDENTIFIER}" '{confirmation:$confirmation}' \
  >"${WORK_DIR}/renew.request.json"
api_mutation POST "${BASE_URL}/api/v1/certificates/${CERTIFICATE_ID}/renewals" \
  "${WORK_DIR}/renew.request.json" 202 renew
RENEW_TASK_ID=$(jq -er '
  select(.kind == "renew" and .state == "queued" and .challenge == "http_01") |
  .id | select(test("^[0-9a-f]{32}$"))
' "${WORK_DIR}/renew.response.json")
poll_task "${RENEW_TASK_ID}" renew-task
RENEW_VERSION_ID=$(jq -er --arg old_version "${ISSUE_VERSION_ID}" '
  select(.kind == "renew" and .state == "succeeded" and .stage == "completed") |
  .version_id | select(test("^[0-9a-f]{32}$") and . != $old_version)
' "${WORK_DIR}/renew-task.json")
verify_material "${CERTIFICATE_ID}" "${RENEW_VERSION_ID}" "${RENEW_TASK_ID}"

api_get "${BASE_URL}/api/v1/certificates/${CERTIFICATE_ID}" "${WORK_DIR}/certificate.json"
jq -e --arg identifier "${IDENTIFIER}" --arg active "${RENEW_VERSION_ID}" --arg old "${ISSUE_VERSION_ID}" '
  .primary_identifier == $identifier and .challenge == "http_01" and .state == "active" and
  .active_version_id == $active and
  ([.versions[] | select(.id == $active and .state == "active")] | length == 1) and
  ([.versions[] | select(.id == $old and .state == "superseded")] | length == 1) and
  (.bindings | length == 1)
' "${WORK_DIR}/certificate.json" >/dev/null || fail 'renewal did not atomically supersede and rebind certificate material'
pass 'forced non-reused HTTP-01 renewal produced a new active version and retained the old version'

MATERIAL_DIGEST_BEFORE=$(docker exec "${APPLICATION_CONTAINER}" sha256sum \
  "/var/lib/nginx-uix/certs/certificates/${CERTIFICATE_ID}/versions/${RENEW_VERSION_ID}/fullchain.pem" |
  awk '{print $1}')
run_bounded 30 "${WORK_DIR}/application-stop.log" docker stop --time 15 "${APPLICATION_CONTAINER}" ||
  fail 'application did not stop gracefully after real ACME issuance'
[ "$(docker inspect --format '{{.State.ExitCode}}' "${APPLICATION_CONTAINER}")" = 0 ] ||
  fail 'application exited nonzero after real ACME issuance'
remove_application
start_application
api_get "${BASE_URL}/api/v1/certificates/${CERTIFICATE_ID}" "${WORK_DIR}/certificate-restarted.json"
MATERIAL_DIGEST_AFTER=$(docker exec "${APPLICATION_CONTAINER}" sha256sum \
  "/var/lib/nginx-uix/certs/certificates/${CERTIFICATE_ID}/versions/${RENEW_VERSION_ID}/fullchain.pem" |
  awk '{print $1}')
[ "${MATERIAL_DIGEST_BEFORE}" = "${MATERIAL_DIGEST_AFTER}" ] ||
  fail 'certificate material changed across same-volume recreation'
jq -e --arg active "${RENEW_VERSION_ID}" '.state == "active" and .active_version_id == $active' \
  "${WORK_DIR}/certificate-restarted.json" >/dev/null || fail 'active certificate metadata did not survive recreation'
pass 'account, session, certificate versions, binding and exact material survived recreation'

jq -n '{}' >"${WORK_DIR}/deactivate.request.json"
api_mutation POST "${BASE_URL}/api/v1/acme/accounts/${PRODUCTION_ACCOUNT_ID}/deactivations" \
  "${WORK_DIR}/deactivate.request.json" 200 deactivate-production
jq -e --arg account_id "${PRODUCTION_ACCOUNT_ID}" '.id == $account_id and .status == "deactivated"' \
  "${WORK_DIR}/deactivate-production.response.json" >/dev/null || fail 'production-path account did not reach deactivated state'
api_mutation POST "${BASE_URL}/api/v1/acme/accounts/${STAGING_ACCOUNT_ID}/deactivations" \
  "${WORK_DIR}/deactivate.request.json" 200 deactivate-staging
jq -e --arg account_id "${STAGING_ACCOUNT_ID}" '.id == $account_id and .status == "deactivated"' \
  "${WORK_DIR}/deactivate-staging.response.json" >/dev/null || fail 'staging account did not reach deactivated state'
api_get "${BASE_URL}/api/v1/certificates/${CERTIFICATE_ID}" "${WORK_DIR}/certificate-after-deactivation.json"
jq -e --arg active "${RENEW_VERSION_ID}" '.state == "active" and .active_version_id == $active' \
  "${WORK_DIR}/certificate-after-deactivation.json" >/dev/null || fail 'certificate became unavailable after account deactivation'
pass 'both remote Pebble accounts were deactivated while the issued certificate remained available'

PLATFORM=$(normalize_image_platform "${PLATFORM}") || fail 'native platform changed during ACME acceptance'
docker_build_metadata || fail 'could not recompute final source identity'
assert_image_identity "${IMAGE}" "${BUILD_IDENTITY}" "${SOURCE_FINGERPRINT}" ||
  fail 'release image identity changed during ACME acceptance'

printf '\nDocker real local-ACME lifecycle acceptance: PASS\n'
printf 'source_fingerprint=%s build_identity=%s platform=%s image_digest=%s\n' \
  "${SOURCE_FINGERPRINT}" "${BUILD_IDENTITY}" "${PLATFORM}" "${IMAGE_DIGEST}"
printf 'pebble_image=%s pebble_revision=%s\n' "${PEBBLE_IMAGE}" "${PEBBLE_REVISION}"
printf 'challenge=http_01 staging_preflight=passed local_production_path=passed renewal=passed recreation=passed deactivation=passed external_production_quota=unused\n'
