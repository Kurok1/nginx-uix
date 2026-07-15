#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.1.0

set -eu

IMAGE=${IMAGE:-nginx-uix:0.1.0-test}
PLATFORM=${PLATFORM:-}
BUILD_IMAGE=${BUILD_IMAGE:-1}
SMOKE_PROFILE=${SMOKE_PROFILE:-full}
RUN_ID="${NGINX_UIX_SMOKE_RUN_ID:-$(date -u +%Y%m%d%H%M%S)-$$-$(openssl rand -hex 4)}"

case "$RUN_ID" in
    ''|*[!A-Za-z0-9_.-]*)
        printf '%s\n' 'FAIL: run ID must contain only letters, digits, dot, underscore, and hyphen' >&2
        exit 2
        ;;
esac
case "$BUILD_IMAGE" in
    0|1) ;;
    *)
        printf '%s\n' 'FAIL: BUILD_IMAGE must be 0 or 1' >&2
        exit 2
        ;;
esac
case "$SMOKE_PROFILE" in
    basic|full) ;;
    *)
        printf '%s\n' 'FAIL: SMOKE_PROFILE must be basic or full' >&2
        exit 2
        ;;
esac
case "$PLATFORM" in
    ''|linux/amd64|linux/arm64) ;;
    *)
        printf '%s\n' 'FAIL: PLATFORM must be empty, linux/amd64, or linux/arm64' >&2
        exit 2
        ;;
esac

ID_SHORT=$(printf '%s' "$RUN_ID" | tr -cd 'A-Za-z0-9' | cut -c1-16)
LABEL="io.nginx-uix.smoke-run=$RUN_ID"
MAIN_CONTAINER="nginx-uix-smoke-main-${RUN_ID}"
NONEMPTY_CONTAINER="nginx-uix-smoke-nonempty-${RUN_ID}"
HELPER_CONTAINER="nginx-uix-smoke-helper-${RUN_ID}"
CONFIG_VOLUME="nginx-uix-smoke-config-${RUN_ID}"
DATA_VOLUME="nginx-uix-smoke-data-${RUN_ID}"
NONEMPTY_CONFIG_VOLUME="nginx-uix-smoke-existing-config-${RUN_ID}"
NONEMPTY_DATA_VOLUME="nginx-uix-smoke-existing-data-${RUN_ID}"
NETWORK="nginx-uix-smoke-${RUN_ID}"
WORK_DIR="${TMPDIR:-/tmp}/nginx-uix-smoke-${RUN_ID}"
COMBINED_LOG="$WORK_DIR/container.log"

TRAFFIC_UI_PID=
TRAFFIC_STATUS_PID=
TRAFFIC_AGENT_PID=
TRAFFIC_NGINX_PID=
WORK_DIR_CREATED=0
MAIN_CONTAINER_CREATED=0
NONEMPTY_CONTAINER_CREATED=0
HELPER_CONTAINER_CREATED=0
CONFIG_VOLUME_CREATED=0
DATA_VOLUME_CREATED=0
NONEMPTY_CONFIG_VOLUME_CREATED=0
NONEMPTY_DATA_VOLUME_CREATED=0
NETWORK_CREATED=0

stop_traffic() {
    for traffic_pid in "$TRAFFIC_UI_PID" "$TRAFFIC_STATUS_PID" "$TRAFFIC_AGENT_PID" "$TRAFFIC_NGINX_PID"; do
        if [ -n "$traffic_pid" ]; then
            kill "$traffic_pid" >/dev/null 2>&1 || true
        fi
    done
    for traffic_pid in "$TRAFFIC_UI_PID" "$TRAFFIC_STATUS_PID" "$TRAFFIC_AGENT_PID" "$TRAFFIC_NGINX_PID"; do
        if [ -n "$traffic_pid" ]; then
            wait "$traffic_pid" >/dev/null 2>&1 || true
        fi
    done
    TRAFFIC_UI_PID=
    TRAFFIC_STATUS_PID=
    TRAFFIC_AGENT_PID=
    TRAFFIC_NGINX_PID=
}

cleanup() {
    stop_traffic
    if command -v docker >/dev/null 2>&1; then
        [ "$HELPER_CONTAINER_CREATED" = 0 ] || docker rm -f "$HELPER_CONTAINER" >/dev/null 2>&1 || true
        [ "$NONEMPTY_CONTAINER_CREATED" = 0 ] || docker rm -f "$NONEMPTY_CONTAINER" >/dev/null 2>&1 || true
        [ "$MAIN_CONTAINER_CREATED" = 0 ] || docker rm -f "$MAIN_CONTAINER" >/dev/null 2>&1 || true
        [ "$NONEMPTY_DATA_VOLUME_CREATED" = 0 ] || docker volume rm "$NONEMPTY_DATA_VOLUME" >/dev/null 2>&1 || true
        [ "$NONEMPTY_CONFIG_VOLUME_CREATED" = 0 ] || docker volume rm "$NONEMPTY_CONFIG_VOLUME" >/dev/null 2>&1 || true
        [ "$DATA_VOLUME_CREATED" = 0 ] || docker volume rm "$DATA_VOLUME" >/dev/null 2>&1 || true
        [ "$CONFIG_VOLUME_CREATED" = 0 ] || docker volume rm "$CONFIG_VOLUME" >/dev/null 2>&1 || true
        [ "$NETWORK_CREATED" = 0 ] || docker network rm "$NETWORK" >/dev/null 2>&1 || true
    fi
    if [ "$WORK_DIR_CREATED" = 1 ]; then
        case "$WORK_DIR" in
            "${TMPDIR:-/tmp}"/nginx-uix-smoke-*) rm -rf "$WORK_DIR" ;;
        esac
    fi
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

pass() {
    printf 'PASS: %s\n' "$1"
}

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required host command is unavailable: $1"
}

for required_command in docker curl openssl python3 sqlite3 awk sed grep cmp sort date; do
    require_command "$required_command"
done
docker info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'

[ ! -e "$WORK_DIR" ] || fail 'run-specific working directory already exists'
mkdir "$WORK_DIR"
WORK_DIR_CREATED=1
chmod 0700 "$WORK_DIR"
: >"$COMBINED_LOG"
chmod 0600 "$COMBINED_LOG"

ADMIN_USERNAME="smoke-admin-${ID_SHORT}"
BLOCKED_USERNAME="smoke-blocked-${ID_SHORT}"
REPLACEMENT_USERNAME="smoke-replacement-${ID_SHORT}"
NONEMPTY_USERNAME="smoke-existing-${ID_SHORT}"
ADMIN_PASSWORD=$(openssl rand -hex 16)
FALLBACK_PASSWORD=$(openssl rand -hex 16)
REPLACEMENT_PASSWORD=$(openssl rand -hex 16)
REPLACEMENT_FALLBACK=$(openssl rand -hex 16)
WRONG_PASSWORD=$(openssl rand -hex 16)

printf '%s\n' "$ADMIN_PASSWORD" >"$WORK_DIR/admin-password"
printf '%s\n' "$REPLACEMENT_PASSWORD" >"$WORK_DIR/replacement-password"
chmod 0444 "$WORK_DIR/admin-password" "$WORK_DIR/replacement-password"

if [ "$BUILD_IMAGE" = 1 ]; then
    if [ -n "$PLATFORM" ]; then
        docker build --platform "$PLATFORM" -f deploy/docker/Dockerfile -t "$IMAGE" .
    else
        docker build -f deploy/docker/Dockerfile -t "$IMAGE" .
    fi
    pass 'release image builds from the pinned Dockerfile'
fi
docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "image does not exist: $IMAGE"
if [ -n "$PLATFORM" ]; then
    IMAGE_ARCH=$(docker image inspect "$IMAGE" --format '{{.Architecture}}')
    case "$PLATFORM:$IMAGE_ARCH" in
        linux/amd64:amd64|linux/arm64:arm64) ;;
        *) fail 'loaded image architecture does not match PLATFORM' ;;
    esac
fi

for reserved_container in "$MAIN_CONTAINER" "$NONEMPTY_CONTAINER" "$HELPER_CONTAINER"; do
    if docker container inspect "$reserved_container" >/dev/null 2>&1; then
        fail 'run-specific container name already exists'
    fi
done
for reserved_volume in "$CONFIG_VOLUME" "$DATA_VOLUME" "$NONEMPTY_CONFIG_VOLUME" "$NONEMPTY_DATA_VOLUME"; do
    if docker volume inspect "$reserved_volume" >/dev/null 2>&1; then
        fail 'run-specific volume name already exists'
    fi
done
if docker network inspect "$NETWORK" >/dev/null 2>&1; then
    fail 'run-specific network name already exists'
fi

docker network create --label "$LABEL" "$NETWORK" >/dev/null
NETWORK_CREATED=1
docker volume create --label "$LABEL" "$CONFIG_VOLUME" >/dev/null
CONFIG_VOLUME_CREATED=1
docker volume create --label "$LABEL" "$DATA_VOLUME" >/dev/null
DATA_VOLUME_CREATED=1
docker volume create --label "$LABEL" "$NONEMPTY_CONFIG_VOLUME" >/dev/null
NONEMPTY_CONFIG_VOLUME_CREATED=1
docker volume create --label "$LABEL" "$NONEMPTY_DATA_VOLUME" >/dev/null
NONEMPTY_DATA_VOLUME_CREATED=1
pass 'unique labelled network and volumes are isolated for this run'

run_application_container() {
    application_name=$1
    application_config_volume=$2
    application_data_volume=$3
    application_secret=$4
    application_username=$5
    application_fallback=$6

    case "$application_name" in
        "$MAIN_CONTAINER") MAIN_CONTAINER_CREATED=1 ;;
        "$NONEMPTY_CONTAINER") NONEMPTY_CONTAINER_CREATED=1 ;;
        *) fail 'application container name is outside this run' ;;
    esac
    set -- docker run -d --name "$application_name" --label "$LABEL" --hostname "$application_name"
    if [ -n "$PLATFORM" ]; then
        set -- "$@" --platform "$PLATFORM"
    fi
    set -- "$@" \
        --network "$NETWORK" \
        -p 127.0.0.1::9000 \
        --mount "type=volume,src=$application_config_volume,dst=/etc/nginx" \
        --mount "type=volume,src=$application_data_volume,dst=/var/lib/nginx-uix" \
        --mount "type=bind,src=$application_secret,dst=/run/secrets/nginx-uix-admin-password,readonly" \
        -e "NGINX_UIX_ADMIN_USERNAME=$application_username" \
        -e NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin-password \
        -e "NGINX_UIX_ADMIN_PASSWORD=$application_fallback" \
        "$IMAGE"
    "$@" >/dev/null
}

container_base_url() {
    published=$(docker port "$1" 9000/tcp | sed -n 's/^127\.0\.0\.1:\([0-9][0-9]*\)$/\1/p')
    case "$published" in
        ''|*[!0-9]*) fail 'UI port was not dynamically mapped to 127.0.0.1' ;;
    esac
    printf 'http://127.0.0.1:%s\n' "$published"
}

wait_for_http_code() {
    wait_url=$1
    wait_code=$2
    wait_seconds=$3
    wait_attempt=0
    while [ "$wait_attempt" -lt "$wait_seconds" ]; do
        observed=$(curl -sS --connect-timeout 1 --max-time 2 -o /dev/null -w '%{http_code}' "$wait_url" 2>/dev/null || true)
        if [ "$observed" = "$wait_code" ]; then
            return 0
        fi
        if [ "$(docker inspect "$4" --format '{{.State.Running}}' 2>/dev/null || true)" != true ]; then
            return 1
        fi
        wait_attempt=$((wait_attempt + 1))
        sleep 1
    done
    return 1
}

wait_for_healthy() {
    health_attempt=0
    while [ "$health_attempt" -lt 45 ]; do
        observed_health=$(docker inspect "$1" --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' 2>/dev/null || true)
        if [ "$observed_health" = healthy ]; then
            return 0
        fi
        health_attempt=$((health_attempt + 1))
        sleep 1
    done
    return 1
}

append_container_logs() {
    docker logs "$1" >>"$COMBINED_LOG" 2>&1 || fail 'could not capture bounded container logs for audit'
}

HTTP_CODE=
request_get() {
    get_url=$1
    get_cookie_jar=$2
    get_body=$3
    get_headers=$4
    set -- curl -sS --connect-timeout 2 --max-time 12 -D "$get_headers" -o "$get_body" -w '%{http_code}'
    if [ -n "$get_cookie_jar" ]; then
        set -- "$@" -b "$get_cookie_jar"
    fi
    set -- "$@" "$get_url"
    if ! HTTP_CODE=$("$@" 2>"$WORK_DIR/curl-error"); then
        fail 'HTTP GET failed before an acceptance status was returned'
    fi
}

LOGIN_CODE=
login_request() {
    login_origin=$1
    login_username=$2
    login_password=$3
    login_cookie_jar=$4
    login_body=$5
    login_headers=$6
    if ! LOGIN_CODE=$(
        printf '{"username":"%s","password":"%s"}' "$login_username" "$login_password" |
            curl -sS --connect-timeout 2 --max-time 12 \
                -D "$login_headers" -o "$login_body" -w '%{http_code}' \
                -c "$login_cookie_jar" \
                -H "Origin: $login_origin" \
                -H 'Content-Type: application/json' \
                --data-binary @- \
                "$BASE_URL/api/v1/auth/session" \
                2>"$WORK_DIR/curl-error"
    ); then
        fail 'login request failed before an acceptance status was returned'
    fi
    chmod 0600 "$login_cookie_jar" "$login_body" "$login_headers"
}

DELETE_CODE=
delete_session_request() {
    delete_cookie_jar=$1
    delete_csrf=$2
    delete_body=$3
    delete_headers=$4
    set -- curl -sS --connect-timeout 2 --max-time 12 \
        -D "$delete_headers" -o "$delete_body" -w '%{http_code}' \
        -b "$delete_cookie_jar" \
        -H "Origin: $BASE_URL" \
        -X DELETE
    if [ -n "$delete_csrf" ]; then
        set -- "$@" -H "X-CSRF-Token: $delete_csrf"
    fi
    set -- "$@" "$BASE_URL/api/v1/auth/session"
    if ! DELETE_CODE=$("$@" 2>"$WORK_DIR/curl-error"); then
        fail 'logout request failed before an acceptance status was returned'
    fi
}

extract_csrf() {
    python3 - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    value = json.load(source).get("csrf_token")
if not isinstance(value, str) or not value:
    raise SystemExit("missing CSRF token")
print(value)
PY
}

extract_session_cookie() {
    awk '$6 == "nginx_uix_session" {value = $7} END {if (value != "") print value}' "$1"
}

assert_session_user() {
    python3 - "$1" "$2" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    payload = json.load(source)
assert payload["user"]["username"] == sys.argv[2], "unexpected authenticated user"
assert isinstance(payload.get("csrf_token"), str) and payload["csrf_token"], "missing CSRF token"
PY
}

snapshot_volume() {
    snapshot_volume_name=$1
    snapshot_output=$2
    HELPER_CONTAINER_CREATED=1
    set -- docker run --rm --name "$HELPER_CONTAINER" --label "$LABEL" --network none --read-only --tmpfs /tmp
    if [ -n "$PLATFORM" ]; then
        set -- "$@" --platform "$PLATFORM"
    fi
    set -- "$@" \
        --mount "type=volume,src=$snapshot_volume_name,dst=/snapshot,readonly" \
        --entrypoint /bin/sh "$IMAGE" -c '
            set -eu
            find /snapshot -mindepth 1 -printf "%P\n" | LC_ALL=C sort | while IFS= read -r relative; do
                path="/snapshot/$relative"
                metadata=$(stat -c "%F|%a|%u|%g|%Y|%s" "$path")
                payload=-
                if [ -L "$path" ]; then
                    payload=$(readlink "$path")
                elif [ -f "$path" ]; then
                    payload=$(sha256sum "$path" | awk "{print \$1}")
                fi
                printf "%s|%s|%s\n" "$relative" "$metadata" "$payload"
            done
        '
    "$@" >"$snapshot_output"
    HELPER_CONTAINER_CREATED=0
    chmod 0600 "$snapshot_output"
}

snapshot_digest() {
    openssl dgst -sha256 "$1" | awk '{print $NF}'
}

assert_default_tree() {
    docker exec "$MAIN_CONTAINER" /usr/bin/find /etc/nginx -mindepth 1 -printf '%P\n' |
        LC_ALL=C sort >"$WORK_DIR/default-tree.actual"
    printf '%s\n' \
        'conf.d' \
        'conf.d/default.conf' \
        'html' \
        'html/index.html' \
        'nginx.conf' >"$WORK_DIR/default-tree.expected"
    cmp -s "$WORK_DIR/default-tree.expected" "$WORK_DIR/default-tree.actual" ||
        fail 'empty configuration volume did not receive exactly the default tree'
    docker exec "$MAIN_CONTAINER" /usr/bin/curl -fsS --max-time 5 http://127.0.0.1/ >"$WORK_DIR/nginx-welcome"
    grep -Fq '<h1>Nginx UIX</h1>' "$WORK_DIR/nginx-welcome" || fail 'default Nginx HTTP service did not serve the welcome page'
}

assert_status_matches_proc() {
    python3 - "$WORK_DIR/status.json" "$WORK_DIR/status-master" "$WORK_DIR/status-workers" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    payload = json.load(source)
assert payload["components"] == {"ui": "healthy", "agent": "healthy", "nginx": "running"}, "components are not healthy"
assert payload["issues"] == [], "healthy status contains issues"
assert payload["master"]["role"] == "master", "master is not verified"
workers = payload["workers"]
assert workers and all(worker["role"] == "worker" for worker in workers), "workers are not verified"
assert [worker["pid"] for worker in workers] == sorted(worker["pid"] for worker in workers), "workers are not sorted"
assert payload["build"]["version"], "Nginx version is absent"
assert payload["build"]["configure_arguments"], "configure arguments are absent"
assert payload["startup_validation"]["valid"] is True, "startup validation is not valid"
with open(sys.argv[2], "w", encoding="ascii") as target:
    target.write(str(payload["master"]["pid"]))
with open(sys.argv[3], "w", encoding="ascii") as target:
    target.write("".join(f"{worker['pid']}\n" for worker in workers))
PY
    master_pid=$(cat "$WORK_DIR/status-master")
    runtime_master=$(docker exec "$MAIN_CONTAINER" /bin/sh -c 'cat /run/nginx-uix/nginx.pid')
    [ "$runtime_master" = "$master_pid" ] || fail 'API master PID differs from the fixed runtime PID file'
    docker exec "$MAIN_CONTAINER" /bin/sh -c '
        set -eu
        master=$1
        [ "$(readlink "/proc/$master/exe")" = /usr/sbin/nginx ]
        tr "\000" " " <"/proc/$master/cmdline" | grep -q "^nginx: master process "
        for process in /proc/[0-9]*; do
            pid=${process#/proc/}
            [ -r "$process/status" ] || continue
            [ "$(awk "\$1 == \"PPid:\" {print \$2}" "$process/status")" = "$master" ] || continue
            command=$(tr "\000" " " <"$process/cmdline" 2>/dev/null || true)
            case "$command" in
                "nginx: worker process"*) printf "%s\n" "$pid" ;;
            esac
        done | sort -n
    ' sh "$master_pid" >"$WORK_DIR/proc-workers"
    cmp -s "$WORK_DIR/status-workers" "$WORK_DIR/proc-workers" || fail 'API worker PIDs differ from the container process tree'
}

assert_effective_config_matches_nginx() {
    docker exec "$MAIN_CONTAINER" /usr/sbin/nginx -T -c /etc/nginx/nginx.conf \
        >"$WORK_DIR/nginx-t.stdout" 2>"$WORK_DIR/nginx-t.stderr"
    python3 - "$WORK_DIR/effective-config.json" "$WORK_DIR/nginx-t.stdout" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    payload = json.load(source)
raw = open(sys.argv[2], "rb").read().replace(b"\r\n", b"\n")
prefix = b"# configuration file "
occurrences = []
body_start = None
position = 0
while position < len(raw):
    newline = raw.find(b"\n", position)
    line_end = len(raw) if newline < 0 else newline
    next_line = len(raw) if newline < 0 else newline + 1
    line = raw[position:line_end]
    if line.startswith(prefix):
        assert line.endswith(b":"), "malformed nginx -T marker"
        if body_start is not None:
            occurrences[-1]["content"] = raw[body_start:position].decode("utf-8")
        order = len(occurrences) + 1
        occurrences.append({
            "id": f"occurrence-{order:06d}",
            "load_order": order,
            "path": line[len(prefix):-1].decode("utf-8"),
            "content": "",
        })
        body_start = next_line
    position = next_line
assert body_start is not None, "nginx -T emitted no configuration marker"
occurrences[-1]["content"] = raw[body_start:].decode("utf-8")
assert payload["entry_config_path"] == "/etc/nginx/nginx.conf", "wrong public entry path"
assert payload["occurrence_count"] == len(occurrences), "wrong occurrence count"
assert payload["occurrences"] == occurrences, "API occurrences differ from the same effective nginx configuration"
PY
}

assert_process_and_listener_boundaries() {
    docker exec "$MAIN_CONTAINER" /bin/sh -c '
        set -eu
        find_one() {
            prefix=$1
            found=
            count=0
            for process in /proc/[0-9]*; do
                [ -r "$process/cmdline" ] || continue
                command=$(tr "\000" " " <"$process/cmdline" 2>/dev/null || true)
                case "$command" in
                    "$prefix"*)
                        found=${process#/proc/}
                        count=$((count + 1))
                        ;;
                esac
            done
            [ "$count" -eq 1 ]
            printf "%s\n" "$found"
        }
        uid_of() { awk "\$1 == \"Uid:\" {print \$2}" "/proc/$1/status"; }
        gid_of() { awk "\$1 == \"Gid:\" {print \$2}" "/proc/$1/status"; }
        groups_of() { awk "\$1 == \"Groups:\" {\$1=\"\"; sub(/^ /, \"\"); print}" "/proc/$1/status"; }

        ui=$(find_one "/usr/local/bin/nginx-uix serve ")
        agent=$(find_one "/usr/local/bin/nginx-uix-agent serve ")
        master=$(find_one "nginx: master process ")
        [ "$(uid_of 1)" = 0 ]
        [ "$(uid_of "$ui")" = 10001 ]
        [ "$(gid_of "$ui")" = 10001 ]
        case " $(groups_of "$ui") " in *" 10002 "*) ;; *) exit 1 ;; esac
        [ "$(uid_of "$agent")" = 0 ]
        [ "$(uid_of "$master")" = 0 ]

        worker_count=0
        for process in /proc/[0-9]*; do
            [ -r "$process/status" ] || continue
            [ "$(awk "\$1 == \"PPid:\" {print \$2}" "$process/status")" = "$master" ] || continue
            command=$(tr "\000" " " <"$process/cmdline" 2>/dev/null || true)
            case "$command" in
                "nginx: worker process"*)
                    worker_count=$((worker_count + 1))
                    [ "$(uid_of "${process#/proc/}")" != 0 ]
                    case " $(groups_of "${process#/proc/}") " in *" 10002 "*) exit 1 ;; esac
                    ;;
            esac
        done
        [ "$worker_count" -gt 0 ]
        [ "$(stat -c "%a:%u:%g" /run/nginx-uix/agent.sock)" = 660:0:10002 ]
        [ "$(stat -c "%a:%u:%g" /var/lib/nginx-uix)" = 700:10001:10001 ]
        [ "$(stat -c "%a:%u:%g" /var/lib/nginx-uix/nginx-uix.db)" = 600:10001:10001 ]

        awk "\$2 ~ /:2328$/ && \$4 == \"0A\" {found=1} END {exit !found}" /proc/net/tcp /proc/net/tcp6
        awk "\$2 ~ /:0050$/ && \$4 == \"0A\" {found=1} END {exit !found}" /proc/net/tcp /proc/net/tcp6
        grep -q "/run/nginx-uix/agent.sock$" /proc/net/unix
        for descriptor in "/proc/$agent"/fd/*; do
            target=$(readlink "$descriptor" 2>/dev/null || true)
            case "$target" in
                socket:\[*\])
                    inode=${target#socket:[}
                    inode=${inode%]}
                    if awk -v inode="$inode" "\$10 == inode {found=1} END {exit !found}" /proc/net/tcp /proc/net/tcp6; then
                        exit 1
                    fi
                    ;;
            esac
        done
    ' || fail 'process identities, groups, permissions, or listener boundaries differ from the image contract'

    docker exec --user 10001:10002 "$MAIN_CONTAINER" /usr/bin/curl -fsS --max-time 5 \
        --unix-socket /run/nginx-uix/agent.sock http://localhost/v1/health >/dev/null ||
        fail 'authorized UI UID could not call the Agent socket'
    if docker exec --user 65534:10002 "$MAIN_CONTAINER" /usr/bin/curl -fsS --max-time 5 \
        --unix-socket /run/nginx-uix/agent.sock http://localhost/v1/health \
        >"$WORK_DIR/unauthorized-agent" 2>/dev/null; then
        fail 'Agent peer-credential check accepted an unauthorized UID'
    fi
    if docker port "$MAIN_CONTAINER" 80/tcp >/dev/null 2>&1; then
        fail 'Nginx port 80 was unexpectedly published by the smoke deployment'
    fi
    if docker port "$MAIN_CONTAINER" 443/tcp >/dev/null 2>&1; then
        fail 'Nginx port 443 was unexpectedly published by the smoke deployment'
    fi
    return 0
}

populate_nonempty_volume() {
    HELPER_CONTAINER_CREATED=1
    set -- docker run --rm --name "$HELPER_CONTAINER" --label "$LABEL" --network none
    if [ -n "$PLATFORM" ]; then
        set -- "$@" --platform "$PLATFORM"
    fi
    set -- "$@" --mount "type=volume,src=$NONEMPTY_CONFIG_VOLUME,dst=/target" --entrypoint /bin/sh "$IMAGE" -c '
        set -eu
        umask 077
        printf "%s\n" "user-owned regular entry" > /target/regular.conf
        printf "%s\n" "user-owned hidden entry" > /target/.hidden
        mkdir /target/nested
        printf "%s\n" "user-owned nested entry" > /target/nested/inside.conf
        ln -s regular.conf /target/regular.link
        chmod 0640 /target/regular.conf
        chmod 0600 /target/.hidden
        chmod 0750 /target/nested
        chmod 0644 /target/nested/inside.conf
        touch -t 202607140101.01 /target/regular.conf /target/.hidden /target/nested/inside.conf
        touch -h -t 202607140101.01 /target/regular.link
        touch -t 202607140101.01 /target/nested
    '
    "$@" >/dev/null
    HELPER_CONTAINER_CREATED=0
}

test_nonempty_volume() {
    populate_nonempty_volume
    snapshot_volume "$NONEMPTY_CONFIG_VOLUME" "$WORK_DIR/nonempty-before"
    before_digest=$(snapshot_digest "$WORK_DIR/nonempty-before")

    run_application_container "$NONEMPTY_CONTAINER" "$NONEMPTY_CONFIG_VOLUME" "$NONEMPTY_DATA_VOLUME" \
        "$WORK_DIR/admin-password" "$NONEMPTY_USERNAME" "$FALLBACK_PASSWORD"
    NONEMPTY_BASE=$(container_base_url "$NONEMPTY_CONTAINER")
    wait_for_http_code "$NONEMPTY_BASE/health/live" 200 45 "$NONEMPTY_CONTAINER" ||
        fail 'UI did not remain available for a nonempty configuration missing nginx.conf'
    snapshot_volume "$NONEMPTY_CONFIG_VOLUME" "$WORK_DIR/nonempty-after-start"
    cmp -s "$WORK_DIR/nonempty-before" "$WORK_DIR/nonempty-after-start" ||
        fail 'nonempty Nginx volume changed during first startup'
    [ "$(curl -sS -o /dev/null -w '%{http_code}' "$NONEMPTY_BASE/health/ready")" = 503 ] ||
        fail 'nonempty invalid configuration unexpectedly became ready'
    docker stop --time 15 "$NONEMPTY_CONTAINER" >/dev/null
    append_container_logs "$NONEMPTY_CONTAINER"
    docker rm "$NONEMPTY_CONTAINER" >/dev/null
    NONEMPTY_CONTAINER_CREATED=0

    run_application_container "$NONEMPTY_CONTAINER" "$NONEMPTY_CONFIG_VOLUME" "$NONEMPTY_DATA_VOLUME" \
        "$WORK_DIR/replacement-password" "$REPLACEMENT_USERNAME" "$REPLACEMENT_FALLBACK"
    NONEMPTY_BASE=$(container_base_url "$NONEMPTY_CONTAINER")
    wait_for_http_code "$NONEMPTY_BASE/health/live" 200 45 "$NONEMPTY_CONTAINER" ||
        fail 'UI did not return when the nonempty volume container was recreated'
    snapshot_volume "$NONEMPTY_CONFIG_VOLUME" "$WORK_DIR/nonempty-after-recreate"
    cmp -s "$WORK_DIR/nonempty-before" "$WORK_DIR/nonempty-after-recreate" ||
        fail 'nonempty Nginx volume changed during container recreation'
    after_digest=$(snapshot_digest "$WORK_DIR/nonempty-after-recreate")
    [ "$before_digest" = "$after_digest" ] || fail 'nonempty volume metadata/content digest changed'
    docker stop --time 15 "$NONEMPTY_CONTAINER" >/dev/null
    append_container_logs "$NONEMPTY_CONTAINER"
    docker rm "$NONEMPTY_CONTAINER" >/dev/null
    NONEMPTY_CONTAINER_CREATED=0
    pass 'regular, hidden, directory, and symlink entries keep identical metadata/content digests across two starts'
}

start_traffic() {
    (
        while :; do
            curl -sS --connect-timeout 1 --max-time 2 "$BASE_URL/health/live" >/dev/null 2>&1 || true
            sleep 0.2
        done
    ) &
    TRAFFIC_UI_PID=$!
    (
        while :; do
            curl -sS --connect-timeout 1 --max-time 2 -b "$PRIMARY_JAR" \
                "$BASE_URL/api/v1/system/status" >/dev/null 2>&1 || true
            sleep 0.2
        done
    ) &
    TRAFFIC_STATUS_PID=$!
    (
        while :; do
            docker exec --user 10001:10002 "$MAIN_CONTAINER" /usr/bin/curl -fsS --max-time 2 \
                --unix-socket /run/nginx-uix/agent.sock http://localhost/v1/health >/dev/null 2>&1 || true
            sleep 0.2
        done
    ) &
    TRAFFIC_AGENT_PID=$!
    (
        while :; do
            docker exec "$MAIN_CONTAINER" /usr/bin/curl -fsS --max-time 2 http://127.0.0.1/ >/dev/null 2>&1 || true
            sleep 0.2
        done
    ) &
    TRAFFIC_NGINX_PID=$!
    sleep 1
}

graceful_stop_main() {
    stop_label=$1
    docker top "$MAIN_CONTAINER" -eo pid,ppid,user,group,comm,args >"$WORK_DIR/processes-before-${stop_label}"
    start_traffic
    stop_started=$(date +%s)
    docker stop --time 15 "$MAIN_CONTAINER" >/dev/null
    stop_elapsed=$(($(date +%s) - stop_started))
    stop_traffic
    [ "$stop_elapsed" -le 15 ] || fail 'docker stop exceeded the 15-second bound'
    [ "$(docker inspect "$MAIN_CONTAINER" --format '{{.State.ExitCode}}')" = 0 ] || fail 'container did not exit with the expected zero status'
    [ "$(docker inspect "$MAIN_CONTAINER" --format '{{.State.OOMKilled}}')" = false ] || fail 'container was OOM-killed during stop'
    [ "$(docker inspect "$MAIN_CONTAINER" --format '{{.State.Running}}:{{.State.Pid}}')" = false:0 ] ||
        fail 'container still reports a running process after stop'
    if docker top "$MAIN_CONTAINER" >/dev/null 2>&1; then
        fail 'docker top still reports processes after container stop'
    fi
    pass "${stop_label}: active UI, Agent, and Nginx traffic stops gracefully within 15 seconds"
}

copy_and_check_database() {
    database_target=$1
    mkdir -p "$database_target"
    docker cp "$MAIN_CONTAINER:/var/lib/nginx-uix/." "$database_target" >/dev/null
    database_file="$database_target/nginx-uix.db"
    [ -f "$database_file" ] || fail 'SQLite database was absent from the stopped data volume'
    [ "$(sqlite3 "$database_file" 'PRAGMA integrity_check;')" = ok ] || fail 'SQLite integrity_check failed after graceful stop'
    [ "$(sqlite3 "$database_file" 'PRAGMA journal_mode;')" = wal ] || fail 'SQLite database is no longer in WAL mode'
    sqlite3 "$database_file" 'PRAGMA wal_checkpoint(PASSIVE);' >/dev/null || fail 'SQLite WAL checkpoint failed after graceful stop'
}

assert_final_image_hygiene() {
    HELPER_CONTAINER_CREATED=1
    set -- docker run --rm --name "$HELPER_CONTAINER" --label "$LABEL" --network none --read-only --tmpfs /tmp
    if [ -n "$PLATFORM" ]; then
        set -- "$@" --platform "$PLATFORM"
    fi
    set -- "$@" --entrypoint /bin/sh "$IMAGE" -c '
        set -eu
        for command_name in go node npm npx playwright chromium chromium-browser; do
            ! command -v "$command_name" >/dev/null 2>&1
        done
        for forbidden_path in \
            /usr/local/go /go/pkg/mod /root/.npm /root/.cache/go-build /workspace \
            /ms-playwright /playwright /app/node_modules /tmp/s6-overlay-noarch.tar.xz \
            /tmp/s6-overlay-x86_64.tar.xz /tmp/s6-overlay-aarch64.tar.xz; do
            [ ! -e "$forbidden_path" ]
        done
        [ -z "$(find / -xdev -type d \( -name node_modules -o -name playwright-report -o -name test-results \) -print -quit 2>/dev/null)" ]
        [ -z "$(find / -xdev -path "*/tests/fixtures*" -print -quit 2>/dev/null)" ]
        [ -z "$(find /tmp -maxdepth 1 -name "s6-overlay*.tar.xz" -print -quit 2>/dev/null)" ]
    '
    "$@" >/dev/null || fail 'release filesystem contains build, source, browser, fixture, archive, or package-cache material'
    HELPER_CONTAINER_CREATED=0

    docker history --no-trunc --format '{{.CreatedBy}}' "$IMAGE" >"$WORK_DIR/image-history"
    if grep -Eiq 'npm ci|go mod download|go build|/workspace|playwright|node_modules|s6-overlay[^ ]*\.tar\.xz' "$WORK_DIR/image-history"; then
        fail 'release image history exposes a discarded build or browser layer'
    fi
    if grep -aF -f "$WORK_DIR/sensitive-patterns" "$WORK_DIR/image-history" >/dev/null; then
        fail 'release image history contains a smoke credential or token'
    fi
    docker save -o "$WORK_DIR/release-image.tar" "$IMAGE"
    if grep -aF -f "$WORK_DIR/sensitive-patterns" "$WORK_DIR/release-image.tar" >/dev/null; then
        fail 'release image filesystem layers contain a smoke credential or token'
    fi
    pass 'final filesystem and history exclude toolchains, caches, source, browsers, fixtures, archives, and credentials'
}

audit_logs_and_database() {
    : >"$WORK_DIR/sensitive-patterns"
    for sensitive_value in \
        "$ADMIN_PASSWORD" "$FALLBACK_PASSWORD" "$REPLACEMENT_PASSWORD" "$REPLACEMENT_FALLBACK" \
        "$WRONG_PASSWORD" "$PRIMARY_CSRF" "$SECONDARY_CSRF" "$PRIMARY_SESSION_TOKEN" "$RESTORED_SESSION_TOKEN"; do
        [ -n "$sensitive_value" ] || fail 'a sensitive audit value was unexpectedly empty'
        printf '%s\n' "$sensitive_value" >>"$WORK_DIR/sensitive-patterns"
    done
    chmod 0600 "$WORK_DIR/sensitive-patterns"
    printf '%s\n' \
        'worker_processes auto;' \
        'user-owned regular entry' \
        '<h1>Nginx UIX</h1>' >"$WORK_DIR/config-patterns"
    chmod 0600 "$WORK_DIR/config-patterns"

    if grep -aF -f "$WORK_DIR/sensitive-patterns" "$COMBINED_LOG" >/dev/null; then
        fail 'container logs contain a password, Secret, Cookie, CSRF token, or raw session token'
    fi
    if grep -aF -f "$WORK_DIR/config-patterns" "$COMBINED_LOG" >/dev/null; then
        fail 'container logs contain Nginx configuration body text'
    fi
    for database_snapshot in \
        "$WORK_DIR/database-first" \
        "$WORK_DIR/database-final" \
        "$WORK_DIR/database-final-restart"; do
        if find "$database_snapshot" -type f -exec grep -aF -f "$WORK_DIR/sensitive-patterns" '{}' + >/dev/null 2>&1; then
            fail 'SQLite files contain a raw credential or token'
        fi
        if find "$database_snapshot" -type f -exec grep -aF -f "$WORK_DIR/config-patterns" '{}' + >/dev/null 2>&1; then
            fail 'SQLite files contain Nginx configuration body text'
        fi
    done
    pass 'logs and SQLite contain neither configuration bodies nor raw credentials/tokens'
}

run_application_container "$MAIN_CONTAINER" "$CONFIG_VOLUME" "$DATA_VOLUME" \
    "$WORK_DIR/admin-password" "$ADMIN_USERNAME" "$FALLBACK_PASSWORD"
BASE_URL=$(container_base_url "$MAIN_CONTAINER")
wait_for_http_code "$BASE_URL/health/ready" 200 60 "$MAIN_CONTAINER" || fail 'fresh all-in-one container did not become ready'
wait_for_healthy "$MAIN_CONTAINER" || fail 'Docker readiness healthcheck did not become healthy'
pass 'fresh all-in-one container reaches HTTP readiness and Docker healthy state'

PORT_LIST=$(docker port "$MAIN_CONTAINER")
[ "$(printf '%s\n' "$PORT_LIST" | awk 'NF {count++} END {print count+0}')" -eq 1 ] || fail 'deployment published more than the UI port'
printf '%s\n' "$PORT_LIST" | grep -Eq '^9000/tcp -> 127\.0\.0\.1:[0-9]+$' || fail 'UI was not published only on a dynamic 127.0.0.1 port'
pass 'management UI is dynamically mapped only on 127.0.0.1'

request_get "$BASE_URL/api/v1/system/status" '' "$WORK_DIR/anonymous-status" "$WORK_DIR/anonymous-status.headers"
[ "$HTTP_CODE" = 401 ] || fail 'anonymous status request was not rejected'
request_get "$BASE_URL/api/v1/nginx/effective-config" '' "$WORK_DIR/anonymous-config" "$WORK_DIR/anonymous-config.headers"
[ "$HTTP_CODE" = 401 ] || fail 'anonymous effective-config request was not rejected'
pass 'anonymous clients cannot read status or configuration'

login_request 'http://invalid.example' "$ADMIN_USERNAME" "$ADMIN_PASSWORD" \
    "$WORK_DIR/wrong-origin.jar" "$WORK_DIR/wrong-origin.json" "$WORK_DIR/wrong-origin.headers"
[ "$LOGIN_CODE" = 403 ] || fail 'cross-origin login was not rejected'
login_request "$BASE_URL" "$ADMIN_USERNAME" "$FALLBACK_PASSWORD" \
    "$WORK_DIR/fallback.jar" "$WORK_DIR/fallback.json" "$WORK_DIR/fallback.headers"
[ "$LOGIN_CODE" = 401 ] || fail 'plain environment fallback overrode the mounted Secret file'

PRIMARY_JAR="$WORK_DIR/primary.jar"
login_request "$BASE_URL" "$ADMIN_USERNAME" "$ADMIN_PASSWORD" \
    "$PRIMARY_JAR" "$WORK_DIR/primary-login.json" "$WORK_DIR/primary-login.headers"
[ "$LOGIN_CODE" = 200 ] || fail 'Secret-file administrator could not log in'
grep -Eiq '^Set-Cookie: .*nginx_uix_session=.*; Path=/; HttpOnly; SameSite=Strict' "$WORK_DIR/primary-login.headers" ||
    fail 'session Cookie is missing Path, HttpOnly, or SameSite=Strict'
if grep -Eiq '^Set-Cookie: .*; Secure' "$WORK_DIR/primary-login.headers"; then
    fail 'HTTP loopback session Cookie was unexpectedly marked Secure'
fi
grep -Eiq '^Cache-Control: no-store' "$WORK_DIR/primary-login.headers" || fail 'login response is cacheable'
PRIMARY_CSRF=$(extract_csrf "$WORK_DIR/primary-login.json")
PRIMARY_SESSION_TOKEN=$(extract_session_cookie "$PRIMARY_JAR")
[ -n "$PRIMARY_SESSION_TOKEN" ] || fail 'session Cookie jar did not receive the opaque token'
assert_session_user "$WORK_DIR/primary-login.json" "$ADMIN_USERNAME"
pass 'real Origin/Cookie flow logs in with Secret-file-first bootstrap credentials'

request_get "$BASE_URL/api/v1/auth/session" "$PRIMARY_JAR" "$WORK_DIR/current-session.json" "$WORK_DIR/current-session.headers"
[ "$HTTP_CODE" = 200 ] || fail 'authenticated session could not be restored'
assert_session_user "$WORK_DIR/current-session.json" "$ADMIN_USERNAME"

SECONDARY_JAR="$WORK_DIR/secondary.jar"
login_request "$BASE_URL" "$ADMIN_USERNAME" "$ADMIN_PASSWORD" \
    "$SECONDARY_JAR" "$WORK_DIR/secondary-login.json" "$WORK_DIR/secondary-login.headers"
[ "$LOGIN_CODE" = 200 ] || fail 'secondary session login failed'
SECONDARY_CSRF=$(extract_csrf "$WORK_DIR/secondary-login.json")
delete_session_request "$SECONDARY_JAR" '' "$WORK_DIR/logout-missing.json" "$WORK_DIR/logout-missing.headers"
[ "$DELETE_CODE" = 403 ] || fail 'logout without CSRF was not rejected'
delete_session_request "$SECONDARY_JAR" invalid-csrf "$WORK_DIR/logout-wrong.json" "$WORK_DIR/logout-wrong.headers"
[ "$DELETE_CODE" = 403 ] || fail 'logout with invalid CSRF was not rejected'
delete_session_request "$SECONDARY_JAR" "$SECONDARY_CSRF" "$WORK_DIR/logout.json" "$WORK_DIR/logout.headers"
[ "$DELETE_CODE" = 204 ] || fail 'logout with Cookie, Origin, and CSRF did not succeed'
pass 'unsafe session operation requires matching Origin, Cookie, and CSRF token'

request_get "$BASE_URL/api/v1/system/status" "$PRIMARY_JAR" "$WORK_DIR/status.json" "$WORK_DIR/status.headers"
[ "$HTTP_CODE" = 200 ] || fail 'authenticated status request failed'
grep -Eiq '^Cache-Control: no-store' "$WORK_DIR/status.headers" || fail 'status response is cacheable'
assert_status_matches_proc
pass 'status master/workers exactly match the same container process tree'

request_get "$BASE_URL/api/v1/nginx/effective-config" "$PRIMARY_JAR" \
    "$WORK_DIR/effective-config.json" "$WORK_DIR/effective-config.headers"
[ "$HTTP_CODE" = 200 ] || fail 'authenticated effective-config request failed'
grep -Eiq '^Cache-Control: no-store' "$WORK_DIR/effective-config.headers" || fail 'effective-config response is cacheable'
assert_effective_config_matches_nginx
pass 'effective-config occurrences exactly match an in-container nginx -T snapshot'

assert_default_tree
snapshot_volume "$CONFIG_VOLUME" "$WORK_DIR/main-config-initial"
MAIN_CONFIG_DIGEST=$(snapshot_digest "$WORK_DIR/main-config-initial")
pass 'empty Nginx volume receives the complete minimal defaults and serves real HTTP'

if [ "$SMOKE_PROFILE" = full ]; then
    assert_process_and_listener_boundaries
    pass 'UID/GID tree, socket/data/DB modes, listeners, and Agent peer credentials enforce the privilege boundary'

    throttle_attempt=1
    while [ "$throttle_attempt" -le 5 ]; do
        login_request "$BASE_URL" "$BLOCKED_USERNAME" "$WRONG_PASSWORD" \
            "$WORK_DIR/throttle.jar" "$WORK_DIR/throttle-${throttle_attempt}.json" "$WORK_DIR/throttle-${throttle_attempt}.headers"
        if [ "$throttle_attempt" -lt 5 ]; then
            [ "$LOGIN_CODE" = 401 ] || fail 'pre-limit invalid login did not return the generic 401 response'
        else
            [ "$LOGIN_CODE" = 429 ] || fail 'fifth invalid login did not enter the persisted throttle'
            retry_after=$(sed -n 's/^[Rr]etry-[Aa]fter: *\([0-9][0-9]*\).*/\1/p' "$WORK_DIR/throttle-${throttle_attempt}.headers" | tr -d '\r')
            case "$retry_after" in ''|0|*[!0-9]*) fail 'rate-limit response has no positive Retry-After' ;; esac
        fi
        throttle_attempt=$((throttle_attempt + 1))
    done
    pass 'fifth invalid login enters a bounded persisted throttle'

    test_nonempty_volume
else
    SECONDARY_CSRF=$PRIMARY_CSRF
fi

snapshot_volume "$CONFIG_VOLUME" "$WORK_DIR/main-config-before-recreate"
cmp -s "$WORK_DIR/main-config-initial" "$WORK_DIR/main-config-before-recreate" || fail 'main Nginx defaults changed before recreation'
graceful_stop_main first-stop
append_container_logs "$MAIN_CONTAINER"
copy_and_check_database "$WORK_DIR/database-first"
docker rm "$MAIN_CONTAINER" >/dev/null
MAIN_CONTAINER_CREATED=0

run_application_container "$MAIN_CONTAINER" "$CONFIG_VOLUME" "$DATA_VOLUME" \
    "$WORK_DIR/replacement-password" "$REPLACEMENT_USERNAME" "$REPLACEMENT_FALLBACK"
BASE_URL=$(container_base_url "$MAIN_CONTAINER")
wait_for_http_code "$BASE_URL/health/ready" 200 60 "$MAIN_CONTAINER" || fail 'recreated all-in-one container did not become ready'

request_get "$BASE_URL/api/v1/auth/session" "$PRIMARY_JAR" "$WORK_DIR/persisted-session.json" "$WORK_DIR/persisted-session.headers"
[ "$HTTP_CODE" = 200 ] || fail 'old session did not survive container recreation with the same data volume'
assert_session_user "$WORK_DIR/persisted-session.json" "$ADMIN_USERNAME"

login_request "$BASE_URL" "$ADMIN_USERNAME" "$ADMIN_PASSWORD" \
    "$WORK_DIR/restored-login.jar" "$WORK_DIR/restored-login.json" "$WORK_DIR/restored-login.headers"
[ "$LOGIN_CODE" = 200 ] || fail 'original administrator credentials did not survive recreation'
RESTORED_SESSION_TOKEN=$(extract_session_cookie "$WORK_DIR/restored-login.jar")
[ -n "$RESTORED_SESSION_TOKEN" ] || fail 'restored login did not issue a session Cookie'
login_request "$BASE_URL" "$REPLACEMENT_USERNAME" "$REPLACEMENT_PASSWORD" \
    "$WORK_DIR/replacement-login.jar" "$WORK_DIR/replacement-login.json" "$WORK_DIR/replacement-login.headers"
[ "$LOGIN_CODE" = 401 ] || fail 'changed bootstrap environment created or reset an administrator'

if [ "$SMOKE_PROFILE" = full ]; then
    login_request "$BASE_URL" "$BLOCKED_USERNAME" "$WRONG_PASSWORD" \
        "$WORK_DIR/throttle-persisted.jar" "$WORK_DIR/throttle-persisted.json" "$WORK_DIR/throttle-persisted.headers"
    [ "$LOGIN_CODE" = 429 ] || fail 'login throttle did not survive container recreation'
fi

snapshot_volume "$CONFIG_VOLUME" "$WORK_DIR/main-config-after-recreate"
cmp -s "$WORK_DIR/main-config-initial" "$WORK_DIR/main-config-after-recreate" || fail 'Nginx volume changed during recreation'
[ "$(snapshot_digest "$WORK_DIR/main-config-after-recreate")" = "$MAIN_CONFIG_DIGEST" ] || fail 'Nginx volume digest changed during recreation'
if [ "$SMOKE_PROFILE" = full ]; then
    pass 'credentials, session, throttle, and Nginx bytes persist while changed bootstrap inputs are ignored'
else
    pass 'credentials, session, and Nginx bytes persist while changed bootstrap inputs are ignored'
fi

graceful_stop_main second-stop
copy_and_check_database "$WORK_DIR/database-final"
[ "$(sqlite3 "$WORK_DIR/database-final/nginx-uix.db" 'SELECT count(*) FROM users;')" = 1 ] || fail 'bootstrap immutability left more than one administrator'
[ "$(sqlite3 "$WORK_DIR/database-final/nginx-uix.db" 'SELECT username FROM users LIMIT 1;')" = "$ADMIN_USERNAME" ] ||
    fail 'bootstrap immutability changed the administrator identity'
if [ "$SMOKE_PROFILE" = full ]; then
    blocked_rows=$(sqlite3 "$WORK_DIR/database-final/nginx-uix.db" "SELECT count(*) FROM login_throttles WHERE normalized_name = '$BLOCKED_USERNAME' AND blocked_until IS NOT NULL;")
    [ "$blocked_rows" = 1 ] || fail 'persisted throttle row is absent from SQLite'
fi

docker start "$MAIN_CONTAINER" >/dev/null
BASE_URL=$(container_base_url "$MAIN_CONTAINER")
wait_for_http_code "$BASE_URL/health/ready" 200 60 "$MAIN_CONTAINER" || fail 'same stopped container did not regain readiness with its volumes'
request_get "$BASE_URL/api/v1/auth/session" "$PRIMARY_JAR" "$WORK_DIR/restarted-session.json" "$WORK_DIR/restarted-session.headers"
[ "$HTTP_CODE" = 200 ] || fail 'persisted session was invalid after same-volume restart'
assert_session_user "$WORK_DIR/restarted-session.json" "$ADMIN_USERNAME"
login_request "$BASE_URL" "$ADMIN_USERNAME" "$ADMIN_PASSWORD" \
    "$WORK_DIR/restarted-login.jar" "$WORK_DIR/restarted-login.json" "$WORK_DIR/restarted-login.headers"
[ "$LOGIN_CODE" = 200 ] || fail 'administrator login failed after same-volume restart'
pass 'same-volume restart regains readiness and accepts the original session and credentials'

graceful_stop_main final-stop
append_container_logs "$MAIN_CONTAINER"
copy_and_check_database "$WORK_DIR/database-final-restart"
snapshot_volume "$CONFIG_VOLUME" "$WORK_DIR/main-config-final"
cmp -s "$WORK_DIR/main-config-initial" "$WORK_DIR/main-config-final" || fail 'Nginx volume changed after graceful restart cycle'
pass 'all graceful stops leave an integrity-checked WAL database and unchanged Nginx volume'

if [ "$SMOKE_PROFILE" = full ]; then
    audit_logs_and_database
    assert_final_image_hygiene
else
    pass 'basic profile completed the architecture boot, login, status/config, persistence, and graceful-stop contract'
fi

printf 'Docker smoke (%s, %s): PASS\n' "$SMOKE_PROFILE" "${PLATFORM:-native}"
