#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.1.0

set -eu

IMAGE=${IMAGE:-nginx-uix:0.1.0-test}
PLATFORM=${PLATFORM:-}
FAULT_CASE=${FAULT_CASE:-all}
RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$-$(openssl rand -hex 4)"
RESOURCE_PREFIX="nginx-uix-faults-${RUN_ID}"
WORK_DIR="${TMPDIR:-/tmp}/${RESOURCE_PREFIX}"
LABEL="nginx-uix.test.run=${RUN_ID}"
OWNED_CONTAINERS=
OWNED_VOLUMES=
ACTIVE_PID=
FAILED=0

say() {
    printf '%s\n' "faults: $*"
}

fail() {
    FAILED=1
    printf '%s\n' "faults: FAIL: $*" >&2
    exit 1
}

trap_run() {
    TR_DEADLINE=$(( $(date +%s) + 5 ))
    "$@" &
    TR_PID=$!
    while kill -0 "$TR_PID" 2>/dev/null; do
        if [ "$(date +%s)" -ge "$TR_DEADLINE" ]; then
            kill -TERM "$TR_PID" 2>/dev/null || true
            sleep 1
            kill -KILL "$TR_PID" 2>/dev/null || true
            break
        fi
        sleep 1
    done
    wait "$TR_PID" 2>/dev/null || true
}

diagnose_failure() {
    printf '%s\n' 'faults: failure diagnostics follow (bounded log tails)' >&2
    if [ -f "$WORK_DIR/status.json" ]; then
        printf '%s\n' 'faults: last authenticated status response:' >&2
        jq --compact-output . "$WORK_DIR/status.json" >&2 || true
    fi
    for DF_CONTAINER in $OWNED_CONTAINERS; do
        printf '%s\n' "faults: container=${DF_CONTAINER}" >&2
        trap_run docker inspect --format 'state={{.State.Status}} exit={{.State.ExitCode}} error={{.State.Error}}' "$DF_CONTAINER" >&2
        trap_run docker logs --tail 120 "$DF_CONTAINER" >&2
    done
}

cleanup() {
    CL_STATUS=$?
    trap - EXIT HUP INT TERM
    set +e
    if [ -n "$ACTIVE_PID" ]; then
        kill -TERM "$ACTIVE_PID" 2>/dev/null || true
        sleep 1
        kill -KILL "$ACTIVE_PID" 2>/dev/null || true
        wait "$ACTIVE_PID" 2>/dev/null || true
        ACTIVE_PID=
    fi
    if [ "$CL_STATUS" -ne 0 ] || [ "$FAILED" -ne 0 ]; then
        diagnose_failure
    fi
    for CL_CONTAINER in $OWNED_CONTAINERS; do
        trap_run docker rm -f -v "$CL_CONTAINER" >/dev/null 2>&1
    done
    for CL_VOLUME in $OWNED_VOLUMES; do
        trap_run docker volume rm "$CL_VOLUME" >/dev/null 2>&1
    done
    case "$WORK_DIR" in
        */nginx-uix-faults-*) rm -rf "$WORK_DIR" ;;
        *) printf '%s\n' "faults: refusing unsafe temporary cleanup path: ${WORK_DIR}" >&2 ;;
    esac
    exit "$CL_STATUS"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

run_bounded() {
    RB_LIMIT=$1
    RB_OUTPUT=$2
    shift 2
    : >"$RB_OUTPUT"
    "$@" >"$RB_OUTPUT" 2>&1 &
    ACTIVE_PID=$!
    RB_DEADLINE=$(( $(date +%s) + RB_LIMIT ))
    while kill -0 "$ACTIVE_PID" 2>/dev/null; do
        if [ "$(date +%s)" -ge "$RB_DEADLINE" ]; then
            kill -TERM "$ACTIVE_PID" 2>/dev/null || true
            sleep 1
            kill -KILL "$ACTIVE_PID" 2>/dev/null || true
            wait "$ACTIVE_PID" 2>/dev/null || true
            ACTIVE_PID=
            printf '%s\n' "faults: command timed out after ${RB_LIMIT}s: $*" >&2
            return 124
        fi
        sleep 1
    done
    if wait "$ACTIVE_PID"; then
        RB_STATUS=0
    else
        RB_STATUS=$?
    fi
    ACTIVE_PID=
    return "$RB_STATUS"
}

wait_until() {
    WU_DESCRIPTION=$1
    WU_LIMIT=$2
    shift 2
    WU_DEADLINE=$(( $(date +%s) + WU_LIMIT ))
    while [ "$(date +%s)" -lt "$WU_DEADLINE" ]; do
        if "$@"; then
            return 0
        fi
        sleep 1
    done
    fail "timed out waiting for ${WU_DESCRIPTION}"
}

assert_stays() {
    AS_DESCRIPTION=$1
    AS_LIMIT=$2
    shift 2
    AS_DEADLINE=$(( $(date +%s) + AS_LIMIT ))
    while [ "$(date +%s)" -lt "$AS_DEADLINE" ]; do
        if ! "$@"; then
            fail "${AS_DESCRIPTION} did not remain true"
        fi
        sleep 1
    done
}

register_container() {
    OWNED_CONTAINERS="$1 $OWNED_CONTAINERS"
}

register_volume() {
    OWNED_VOLUMES="$1 $OWNED_VOLUMES"
}

create_volume() {
    CV_NAME=$1
    register_volume "$CV_NAME"
    run_bounded 10 "$WORK_DIR/docker-volume.out" \
        docker volume create --label "$LABEL" "$CV_NAME" || fail "create volume ${CV_NAME}"
    [ "$(sed -n '1p' "$WORK_DIR/docker-volume.out")" = "$CV_NAME" ] || fail "unexpected volume name for ${CV_NAME}"
}

create_container() {
    CC_NAME=$1
    shift
    register_container "$CC_NAME"
    if [ -n "$PLATFORM" ]; then
        run_bounded 15 "$WORK_DIR/docker-create.out" \
            docker create --platform "$PLATFORM" --label "$LABEL" --name "$CC_NAME" "$@" "$IMAGE" \
            || fail "create container ${CC_NAME}"
    else
        run_bounded 15 "$WORK_DIR/docker-create.out" \
            docker create --label "$LABEL" --name "$CC_NAME" "$@" "$IMAGE" \
            || fail "create container ${CC_NAME}"
    fi
}

start_container() {
    run_bounded 15 "$WORK_DIR/docker-start.out" docker start "$1" || fail "start container $1"
}

remove_container() {
    run_bounded 15 "$WORK_DIR/docker-remove.out" docker rm -f -v "$1" || fail "remove container $1"
}

stop_container() {
    run_bounded 20 "$WORK_DIR/docker-stop.out" docker stop --time 10 "$1" || fail "stop container $1"
}

seed_config() {
    SC_VOLUME=$1
    SC_KIND=$2
    SC_HELPER="${RESOURCE_PREFIX}-seed-${SC_KIND}"
    register_container "$SC_HELPER"
    case "$SC_KIND" in
        invalid)
            SC_COMMAND="umask 077; printf '%s\\n' '# @author hanchao <hanchao@66yunlian.com>' '# @since 0.1.0' 'invalid_directive;' > /target/nginx.conf"
            ;;
        missing)
            SC_COMMAND="umask 077; mkdir /target/conf.d; printf '%s\\n' 'intentional missing entry configuration' > /target/.sentinel"
            ;;
        *) fail "unknown seed kind ${SC_KIND}" ;;
    esac
    if [ -n "$PLATFORM" ]; then
        run_bounded 15 "$WORK_DIR/docker-seed-create.out" \
            docker create --platform "$PLATFORM" --label "$LABEL" --name "$SC_HELPER" \
            --entrypoint /bin/sh --mount "type=volume,src=${SC_VOLUME},dst=/target" \
            "$IMAGE" -ec "$SC_COMMAND" || fail "create ${SC_KIND} config helper"
    else
        run_bounded 15 "$WORK_DIR/docker-seed-create.out" \
            docker create --label "$LABEL" --name "$SC_HELPER" \
            --entrypoint /bin/sh --mount "type=volume,src=${SC_VOLUME},dst=/target" \
            "$IMAGE" -ec "$SC_COMMAND" || fail "create ${SC_KIND} config helper"
    fi
    if ! run_bounded 15 "$WORK_DIR/docker-seed-start.out" docker start --attach "$SC_HELPER"; then
        cat "$WORK_DIR/docker-seed-start.out" >&2
        fail "seed ${SC_KIND} config"
    fi
    remove_container "$SC_HELPER"
}

standard_main_container() {
    SM_NAME=$1
    SM_CONFIG=$2
    SM_DATA=$3
    create_container "$SM_NAME" \
        -p 127.0.0.1::9000 -p 127.0.0.1::80 \
        -e NGINX_UIX_ADMIN_USERNAME=admin \
        -e NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/admin_password \
        --mount "type=bind,src=${PASSWORD_FILE},dst=/run/secrets/admin_password,readonly" \
        --mount "type=volume,src=${SM_CONFIG},dst=/etc/nginx" \
        --mount "type=volume,src=${SM_DATA},dst=/var/lib/nginx-uix"
}

readonly_data_container() {
    RD_NAME=$1
    RD_CONFIG=$2
    RD_DATA=$3
    create_container "$RD_NAME" \
        -p 127.0.0.1::9000 -p 127.0.0.1::80 \
        -e NGINX_UIX_ADMIN_USERNAME=admin \
        -e NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/admin_password \
        --mount "type=bind,src=${PASSWORD_FILE},dst=/run/secrets/admin_password,readonly" \
        --mount "type=volume,src=${RD_CONFIG},dst=/etc/nginx" \
        --mount "type=volume,src=${RD_DATA},dst=/var/lib/nginx-uix,readonly"
}

full_data_container() {
    FD_NAME=$1
    FD_CONFIG=$2
    create_container "$FD_NAME" \
        -p 127.0.0.1::9000 -p 127.0.0.1::80 \
        -e NGINX_UIX_ADMIN_USERNAME=admin \
        -e NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/admin_password \
        --mount "type=bind,src=${PASSWORD_FILE},dst=/run/secrets/admin_password,readonly" \
        --mount "type=volume,src=${FD_CONFIG},dst=/etc/nginx" \
        --mount type=tmpfs,destination=/var/lib/nginx-uix,tmpfs-size=4096,tmpfs-mode=0700
}

container_running() {
    CR_NAME=$1
    if ! run_bounded 5 "$WORK_DIR/docker-inspect.out" docker inspect --format '{{.State.Running}}' "$CR_NAME"; then
        return 1
    fi
    [ "$(sed -n '1p' "$WORK_DIR/docker-inspect.out")" = true ]
}

container_endpoint() {
    CE_NAME=$1
    CE_PORT=$2
    CE_OUTPUT=$3
    run_bounded 5 "$WORK_DIR/docker-port.out" docker port "$CE_NAME" "${CE_PORT}/tcp" || fail "inspect published port ${CE_PORT} for ${CE_NAME}"
    CE_ADDRESS=$(sed -n '1p' "$WORK_DIR/docker-port.out")
    [ -n "$CE_ADDRESS" ] || fail "empty published port ${CE_PORT} for ${CE_NAME}"
    printf 'http://%s\n' "$CE_ADDRESS" >"$CE_OUTPUT"
}

http_get() {
    HG_URL=$1
    HG_BODY=$2
    if ! run_bounded 5 "$WORK_DIR/http-status.out" \
        curl --silent --show-error --connect-timeout 1 --max-time 3 \
        --output "$HG_BODY" --write-out '%{http_code}' "$HG_URL"; then
        return 1
    fi
    return 0
}

http_status_is() {
    HS_URL=$1
    HS_EXPECTED=$2
    http_get "$HS_URL" "$WORK_DIR/http-body.out" || return 1
    [ "$(cat "$WORK_DIR/http-status.out")" = "$HS_EXPECTED" ]
}

ui_live() {
    UL_URL=$1
    http_get "${UL_URL}/health/live" "$WORK_DIR/live-body.json" || return 1
    [ "$(cat "$WORK_DIR/http-status.out")" = 200 ] || return 1
    [ "$(cat "$WORK_DIR/live-body.json")" = '{"status":"ok"}' ]
}

ui_unavailable() {
    UU_URL=$1
    if http_get "${UU_URL}/health/live" "$WORK_DIR/unavailable-body.out"; then
        return 1
    fi
    return 0
}

readiness_is() {
    RI_URL=$1
    RI_STATUS=$2
    RI_BODY=$3
    http_get "${RI_URL}/health/ready" "$WORK_DIR/readiness-body.json" || return 1
    [ "$(cat "$WORK_DIR/http-status.out")" = "$RI_STATUS" ] || return 1
    [ "$(cat "$WORK_DIR/readiness-body.json")" = "$RI_BODY" ]
}

nginx_ready() {
    NR_URL=$1
    http_get "${NR_URL}/" "$WORK_DIR/nginx-body.html" || return 1
    [ "$(cat "$WORK_DIR/http-status.out")" = 200 ] || return 1
    grep -q '<h1>Nginx UIX</h1>' "$WORK_DIR/nginx-body.html"
}

login() {
    LI_URL=$1
    LI_COOKIE=$2
    if ! run_bounded 8 "$WORK_DIR/login-status.out" \
        curl --silent --show-error --connect-timeout 1 --max-time 6 \
        --request POST --header "Origin: ${LI_URL}" --header 'Content-Type: application/json' \
        --data-binary "@${LOGIN_FILE}" --cookie-jar "$LI_COOKIE" \
        --output "$WORK_DIR/login-response.json" --write-out '%{http_code}' \
        "${LI_URL}/api/v1/auth/session"; then
        fail "login transport failed at ${LI_URL}"
    fi
    [ "$(cat "$WORK_DIR/login-status.out")" = 200 ] || fail "login returned HTTP $(cat "$WORK_DIR/login-status.out")"
    grep -q 'nginx_uix_session' "$LI_COOKIE" || fail 'login did not issue the session cookie'
    jq -e '.user.username == "admin" and (.csrf_token | type == "string" and length > 20)' \
        "$WORK_DIR/login-response.json" >/dev/null || fail 'login response contract mismatch'
}

authenticated_get() {
    AG_URL=$1
    AG_COOKIE=$2
    AG_PATH=$3
    AG_OUTPUT=$4
    if ! run_bounded 8 "$WORK_DIR/auth-status.out" \
        curl --silent --show-error --connect-timeout 1 --max-time 6 \
        --cookie "$AG_COOKIE" --output "$AG_OUTPUT" --write-out '%{http_code}' \
        "${AG_URL}${AG_PATH}"; then
        return 1
    fi
    [ "$(cat "$WORK_DIR/auth-status.out")" = 200 ]
}

healthy_status() {
    HST_URL=$1
    HST_COOKIE=$2
    authenticated_get "$HST_URL" "$HST_COOKIE" /api/v1/system/status "$WORK_DIR/status.json" || return 1
    jq -e '
        .components.ui == "healthy" and
        .components.agent == "healthy" and
        .components.nginx == "running" and
        (.master.pid | type == "number" and . > 0) and
        (.workers | length > 0) and
        .issues == []
    ' "$WORK_DIR/status.json" >/dev/null
}

partial_agent_status() {
    PAS_URL=$1
    PAS_COOKIE=$2
    authenticated_get "$PAS_URL" "$PAS_COOKIE" /api/v1/system/status "$WORK_DIR/status.json" || return 1
    jq -e '
        .components == {"ui":"healthy","agent":"unavailable","nginx":"unknown"} and
        .master == null and .workers == [] and .build == null and
        .startup_validation == null and .recovery == null and
        .issues == ["AGENT_UNAVAILABLE"]
    ' "$WORK_DIR/status.json" >/dev/null
}

invalid_status() {
    IS_URL=$1
    IS_COOKIE=$2
    authenticated_get "$IS_URL" "$IS_COOKIE" /api/v1/system/status "$WORK_DIR/status.json" || return 1
    jq -e '
        .components.ui == "healthy" and
        .components.agent == "healthy" and
        .components.nginx == "stopped" and
        .master == null and .workers == [] and
        .startup_validation.valid == false and
        .recovery.permanent == true and
        .recovery.last_result == "invalid_config"
    ' "$WORK_DIR/status.json" >/dev/null
}

permanent_status() {
    PS_URL=$1
    PS_COOKIE=$2
    authenticated_get "$PS_URL" "$PS_COOKIE" /api/v1/system/status "$WORK_DIR/status.json" || return 1
    jq -e '
        .components.ui == "healthy" and
        .components.agent == "healthy" and
        .components.nginx == "stopped" and
        .master == null and .workers == [] and
        .recovery.count == 5 and
        .recovery.permanent == true and
        .recovery.last_result == "permanent_failure"
    ' "$WORK_DIR/status.json" >/dev/null
}

service_pid() {
    SP_CONTAINER=$1
    SP_SERVICE=$2
    SP_OUTPUT=$3
    run_bounded 5 "$SP_OUTPUT" docker exec "$SP_CONTAINER" \
        /command/s6-svstat -o pid "/run/service/${SP_SERVICE}" || return 1
    SP_VALUE=$(tr -d '\r\n' <"$SP_OUTPUT")
    case "$SP_VALUE" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ "$SP_VALUE" -gt 0 ]
}

live_master_pid() {
    LMP_CONTAINER=$1
    LMP_OUTPUT=$2
    run_bounded 5 "$LMP_OUTPUT" docker exec "$LMP_CONTAINER" /bin/sh -ec '
        pid=$(cat /run/nginx-uix/nginx.pid)
        test -r "/proc/${pid}/cmdline"
        cmd=$(tr "\\000" " " < "/proc/${pid}/cmdline")
        case "$cmd" in *"nginx: master process"*) ;; *) exit 1 ;; esac
        service_pid=$(/command/s6-svstat -o pid /run/service/nginx)
        test "$service_pid" = "$pid"
        printf "%s\\n" "$pid"
    ' || return 1
}

no_live_master() {
    NLM_CONTAINER=$1
    if ! run_bounded 5 "$WORK_DIR/no-master.out" docker exec "$NLM_CONTAINER" /bin/sh -ec '
        service_pid=$(/command/s6-svstat -o pid /run/service/nginx)
        printf "service_pid=%s\\n" "$service_pid"
        test "$service_pid" = -1
        if test -r /run/nginx-uix/nginx.pid; then
            stale_pid=$(cat /run/nginx-uix/nginx.pid)
            printf "pidfile=%s proc_exists=%s\\n" "$stale_pid" "$(test -d "/proc/${stale_pid}" && printf yes || printf no)"
            test ! -d "/proc/${stale_pid}"
        fi
    '; then
        cat "$WORK_DIR/no-master.out" >&2
        return 1
    fi
}

service_restarted_with_nginx_preserved() {
    SR_CONTAINER=$1
    SR_NGINX_URL=$2
    service_pid "$SR_CONTAINER" nginx-uix "$WORK_DIR/new-ui-pid.out" || return 1
    SR_NEW_UI_PID=$(tr -d '\r\n' <"$WORK_DIR/new-ui-pid.out")
    [ "$SR_NEW_UI_PID" != "$CRASH_OLD_UI_PID" ] || return 1
    live_master_pid "$SR_CONTAINER" "$WORK_DIR/current-nginx-pid.out" || return 1
    [ "$(tr -d '\r\n' <"$WORK_DIR/current-nginx-pid.out")" = "$CRASH_NGINX_PID" ] || return 1
    nginx_ready "$SR_NGINX_URL"
}

new_master_after() {
    NM_CONTAINER=$1
    NM_OLD_PID=$2
    NM_URL=$3
    live_master_pid "$NM_CONTAINER" "$WORK_DIR/new-master-pid.out" || return 1
    NM_NEW_PID=$(tr -d '\r\n' <"$WORK_DIR/new-master-pid.out")
    [ "$NM_NEW_PID" != "$NM_OLD_PID" ] || return 1
    nginx_ready "$NM_URL"
}

create_case_resources() {
    CCR_SUFFIX=$1
    CASE_CONTAINER="${RESOURCE_PREFIX}-${CCR_SUFFIX}"
    CASE_CONFIG="${RESOURCE_PREFIX}-${CCR_SUFFIX}-config"
    CASE_DATA="${RESOURCE_PREFIX}-${CCR_SUFFIX}-data"
    create_volume "$CASE_CONFIG"
    create_volume "$CASE_DATA"
}

scenario_invalid_config() {
    SIC_KIND=$1
    say "scenario ${SIC_KIND} nginx.conf"
    create_case_resources "$SIC_KIND"
    seed_config "$CASE_CONFIG" "$SIC_KIND"
    standard_main_container "$CASE_CONTAINER" "$CASE_CONFIG" "$CASE_DATA"
    start_container "$CASE_CONTAINER"
    wait_until "${SIC_KIND} container to remain running" 15 container_running "$CASE_CONTAINER"
    container_endpoint "$CASE_CONTAINER" 9000 "$WORK_DIR/ui-url"
    SIC_UI_URL=$(cat "$WORK_DIR/ui-url")
    wait_until "UI liveness with ${SIC_KIND} configuration" 30 ui_live "$SIC_UI_URL"
    login "$SIC_UI_URL" "$WORK_DIR/${SIC_KIND}-cookies.txt"
    wait_until "invalid startup status for ${SIC_KIND}" 30 invalid_status "$SIC_UI_URL" "$WORK_DIR/${SIC_KIND}-cookies.txt"
    readiness_is "$SIC_UI_URL" 503 '{"status":"not_ready"}' || fail "${SIC_KIND} readiness did not fail generically"
    no_live_master "$CASE_CONTAINER" || fail "Nginx master exists for ${SIC_KIND} configuration"
    service_pid "$CASE_CONTAINER" nginx-uix-agent "$WORK_DIR/agent-pid.out" || fail "Agent unavailable for ${SIC_KIND} configuration"
    say "PASS ${SIC_KIND}: UI and Agent available, Nginx absent, readiness 503"
}

scenario_readonly_data() {
    say 'scenario read-only data volume'
    create_case_resources readonly-base
    RD_BASE_CONTAINER=$CASE_CONTAINER
    standard_main_container "$RD_BASE_CONTAINER" "$CASE_CONFIG" "$CASE_DATA"
    start_container "$RD_BASE_CONTAINER"
    wait_until 'baseline UI liveness before read-only remount' 30 container_running "$RD_BASE_CONTAINER"
    container_endpoint "$RD_BASE_CONTAINER" 9000 "$WORK_DIR/ui-url"
    RD_BASE_UI=$(cat "$WORK_DIR/ui-url")
    wait_until 'baseline UI liveness before read-only remount' 30 ui_live "$RD_BASE_UI"
    stop_container "$RD_BASE_CONTAINER"
    remove_container "$RD_BASE_CONTAINER"

    RD_FAULT_CONTAINER="${RESOURCE_PREFIX}-readonly"
    readonly_data_container "$RD_FAULT_CONTAINER" "$CASE_CONFIG" "$CASE_DATA"
    start_container "$RD_FAULT_CONTAINER"
    wait_until 'read-only-data container to remain running' 15 container_running "$RD_FAULT_CONTAINER"
    container_endpoint "$RD_FAULT_CONTAINER" 9000 "$WORK_DIR/ui-url"
    container_endpoint "$RD_FAULT_CONTAINER" 80 "$WORK_DIR/nginx-url"
    RD_UI_URL=$(cat "$WORK_DIR/ui-url")
    RD_NGINX_URL=$(cat "$WORK_DIR/nginx-url")
    wait_until 'Nginx with read-only UI data' 30 nginx_ready "$RD_NGINX_URL"
    assert_stays 'UI remains unbound with read-only data while Nginx responds' 5 ui_unavailable "$RD_UI_URL"
    nginx_ready "$RD_NGINX_URL" || fail 'Nginx stopped during read-only data failure'
    say 'PASS read-only-data: UI unbound and valid Nginx preserved'
}

scenario_full_data() {
    say 'scenario full data storage'
    create_volume "${RESOURCE_PREFIX}-full-config"
    FD_CONFIG="${RESOURCE_PREFIX}-full-config"
    FD_CONTAINER="${RESOURCE_PREFIX}-full"
    full_data_container "$FD_CONTAINER" "$FD_CONFIG"
    start_container "$FD_CONTAINER"
    wait_until 'full-data container to remain running' 15 container_running "$FD_CONTAINER"
    container_endpoint "$FD_CONTAINER" 9000 "$WORK_DIR/ui-url"
    container_endpoint "$FD_CONTAINER" 80 "$WORK_DIR/nginx-url"
    FD_UI_URL=$(cat "$WORK_DIR/ui-url")
    FD_NGINX_URL=$(cat "$WORK_DIR/nginx-url")
    wait_until 'Nginx with full UI data storage' 30 nginx_ready "$FD_NGINX_URL"
    assert_stays 'UI remains unbound with full data while Nginx responds' 5 ui_unavailable "$FD_UI_URL"
    nginx_ready "$FD_NGINX_URL" || fail 'Nginx stopped during full data failure'
    say 'PASS full-data: UI unbound and valid Nginx preserved'
}

create_port_occupier() {
    PO_NAME=$1
    register_container "$PO_NAME"
    PO_COMMAND='printf "%s\n" "events {}" "http { server { listen 9000; return 204; } }" > /tmp/occupied.conf; exec /usr/sbin/nginx -g "daemon off;" -c /tmp/occupied.conf'
    if [ -n "$PLATFORM" ]; then
        run_bounded 15 "$WORK_DIR/occupier-create.out" \
            docker create --platform "$PLATFORM" --label "$LABEL" --name "$PO_NAME" \
            -p 127.0.0.1::9000 -p 127.0.0.1::80 --entrypoint /bin/sh \
            "$IMAGE" -ec "$PO_COMMAND" || fail 'create UI-port occupier'
    else
        run_bounded 15 "$WORK_DIR/occupier-create.out" \
            docker create --label "$LABEL" --name "$PO_NAME" \
            -p 127.0.0.1::9000 -p 127.0.0.1::80 --entrypoint /bin/sh \
            "$IMAGE" -ec "$PO_COMMAND" || fail 'create UI-port occupier'
    fi
}

scenario_occupied_ui() {
    say 'scenario occupied UI bind address'
    OU_OCCUPIER="${RESOURCE_PREFIX}-occupier"
    create_port_occupier "$OU_OCCUPIER"
    start_container "$OU_OCCUPIER"
    container_endpoint "$OU_OCCUPIER" 9000 "$WORK_DIR/ui-url"
    OU_UI_URL=$(cat "$WORK_DIR/ui-url")
    wait_until 'known process to occupy UI address' 20 http_status_is "${OU_UI_URL}/" 204

    create_volume "${RESOURCE_PREFIX}-occupied-config"
    create_volume "${RESOURCE_PREFIX}-occupied-data"
    OU_MAIN="${RESOURCE_PREFIX}-occupied"
    create_container "$OU_MAIN" \
        --network "container:${OU_OCCUPIER}" \
        -e NGINX_UIX_ADMIN_USERNAME=admin \
        -e NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/admin_password \
        --mount "type=bind,src=${PASSWORD_FILE},dst=/run/secrets/admin_password,readonly" \
        --mount "type=volume,src=${RESOURCE_PREFIX}-occupied-config,dst=/etc/nginx" \
        --mount "type=volume,src=${RESOURCE_PREFIX}-occupied-data,dst=/var/lib/nginx-uix"
    start_container "$OU_MAIN"
    wait_until 'occupied-port main container to remain running' 15 container_running "$OU_MAIN"
    container_endpoint "$OU_OCCUPIER" 80 "$WORK_DIR/nginx-url"
    OU_NGINX_URL=$(cat "$WORK_DIR/nginx-url")
    wait_until 'Nginx while UI address is occupied' 30 nginx_ready "$OU_NGINX_URL"
    assert_stays 'known occupier retains UI address' 5 http_status_is "${OU_UI_URL}/health/live" 204
    nginx_ready "$OU_NGINX_URL" || fail 'Nginx stopped during UI bind conflict'
    say 'PASS occupied-ui: UI cannot bind and valid Nginx is preserved'
}

scenario_agent_down() {
    say 'scenario stopped Agent'
    create_case_resources agent
    standard_main_container "$CASE_CONTAINER" "$CASE_CONFIG" "$CASE_DATA"
    start_container "$CASE_CONTAINER"
    wait_until 'Agent scenario container' 15 container_running "$CASE_CONTAINER"
    container_endpoint "$CASE_CONTAINER" 9000 "$WORK_DIR/ui-url"
    container_endpoint "$CASE_CONTAINER" 80 "$WORK_DIR/nginx-url"
    AD_UI_URL=$(cat "$WORK_DIR/ui-url")
    AD_NGINX_URL=$(cat "$WORK_DIR/nginx-url")
    wait_until 'healthy readiness before stopping Agent' 40 readiness_is "$AD_UI_URL" 200 '{"status":"ready"}'
    login "$AD_UI_URL" "$WORK_DIR/agent-cookies.txt"
    wait_until 'healthy status before stopping Agent' 20 healthy_status "$AD_UI_URL" "$WORK_DIR/agent-cookies.txt"
    run_bounded 10 "$WORK_DIR/agent-down.out" docker exec "$CASE_CONTAINER" \
        /command/s6-svc -d -wD -T 5000 /run/service/nginx-uix-agent || fail 'stop Agent through s6'
    wait_until 'partial Dashboard after Agent stop' 20 partial_agent_status "$AD_UI_URL" "$WORK_DIR/agent-cookies.txt"
    readiness_is "$AD_UI_URL" 503 '{"status":"not_ready"}' || fail 'readiness did not fail after Agent stop'
    nginx_ready "$AD_NGINX_URL" || fail 'Nginx stopped with Agent'
    run_bounded 15 "$WORK_DIR/agent-up.out" docker exec "$CASE_CONTAINER" \
        /command/s6-svc -u -wU -T 10000 /run/service/nginx-uix-agent || fail 'restore Agent through s6'
    wait_until 'healthy readiness after Agent restore' 30 readiness_is "$AD_UI_URL" 200 '{"status":"ready"}'
    say 'PASS agent-down: partial Dashboard/readiness failure, then clean recovery'
}

scenario_ui_crash() {
    say 'scenario killed UI process'
    create_case_resources ui-crash
    standard_main_container "$CASE_CONTAINER" "$CASE_CONFIG" "$CASE_DATA"
    start_container "$CASE_CONTAINER"
    wait_until 'UI-crash readiness baseline' 40 container_running "$CASE_CONTAINER"
    container_endpoint "$CASE_CONTAINER" 9000 "$WORK_DIR/ui-url"
    container_endpoint "$CASE_CONTAINER" 80 "$WORK_DIR/nginx-url"
    UC_UI_URL=$(cat "$WORK_DIR/ui-url")
    UC_NGINX_URL=$(cat "$WORK_DIR/nginx-url")
    wait_until 'UI-crash healthy readiness baseline' 40 readiness_is "$UC_UI_URL" 200 '{"status":"ready"}'
    login "$UC_UI_URL" "$WORK_DIR/ui-cookies.txt"
    service_pid "$CASE_CONTAINER" nginx-uix "$WORK_DIR/old-ui-pid.out" || fail 'capture original UI PID'
    CRASH_OLD_UI_PID=$(tr -d '\r\n' <"$WORK_DIR/old-ui-pid.out")
    live_master_pid "$CASE_CONTAINER" "$WORK_DIR/old-nginx-pid.out" || fail 'capture original Nginx master PID'
    CRASH_NGINX_PID=$(tr -d '\r\n' <"$WORK_DIR/old-nginx-pid.out")
    run_bounded 8 "$WORK_DIR/ui-kill.out" docker exec "$CASE_CONTAINER" \
        /command/s6-svc -k /run/service/nginx-uix || fail 'kill UI through s6'
    wait_until 's6 to restore UI without replacing Nginx' 30 \
        service_restarted_with_nginx_preserved "$CASE_CONTAINER" "$UC_NGINX_URL"
    authenticated_get "$UC_UI_URL" "$WORK_DIR/ui-cookies.txt" /api/v1/auth/session "$WORK_DIR/session.json" \
        || fail 'restored UI did not accept the existing session'
    readiness_is "$UC_UI_URL" 200 '{"status":"ready"}' || fail 'readiness did not recover after UI restart'
    say 'PASS ui-crash: Nginx master/HTTP preserved and s6 restored UI/session'
}

scenario_nginx_permanent() {
    say 'scenario fifth Nginx death reaches permanent failure'
    create_case_resources nginx-death
    standard_main_container "$CASE_CONTAINER" "$CASE_CONFIG" "$CASE_DATA"
    start_container "$CASE_CONTAINER"
    wait_until 'Nginx-death container' 15 container_running "$CASE_CONTAINER"
    container_endpoint "$CASE_CONTAINER" 9000 "$WORK_DIR/ui-url"
    container_endpoint "$CASE_CONTAINER" 80 "$WORK_DIR/nginx-url"
    NP_UI_URL=$(cat "$WORK_DIR/ui-url")
    NP_NGINX_URL=$(cat "$WORK_DIR/nginx-url")
    wait_until 'healthy Nginx before death injection' 40 readiness_is "$NP_UI_URL" 200 '{"status":"ready"}'
    login "$NP_UI_URL" "$WORK_DIR/nginx-cookies.txt"
    NP_SEEN=
    NP_INDEX=1
    while [ "$NP_INDEX" -le 5 ]; do
        live_master_pid "$CASE_CONTAINER" "$WORK_DIR/master-before-kill.out" || fail "capture Nginx master before death ${NP_INDEX}"
        NP_PID=$(tr -d '\r\n' <"$WORK_DIR/master-before-kill.out")
        case " $NP_SEEN " in
            *" ${NP_PID} "*) fail "Nginx master PID ${NP_PID} was reused during bounded recovery" ;;
        esac
        NP_SEEN="${NP_SEEN} ${NP_PID}"
        run_bounded 8 "$WORK_DIR/nginx-kill.out" docker exec "$CASE_CONTAINER" \
            /command/s6-svc -K /run/service/nginx || fail "kill Nginx process group death ${NP_INDEX}"
        if [ "$NP_INDEX" -lt 5 ]; then
            wait_until "new Nginx master after death ${NP_INDEX}" 30 \
                new_master_after "$CASE_CONTAINER" "$NP_PID" "$NP_NGINX_URL"
        fi
        NP_INDEX=$((NP_INDEX + 1))
    done
    wait_until 'permanent recovery state after fifth death' 30 \
        permanent_status "$NP_UI_URL" "$WORK_DIR/nginx-cookies.txt"
    assert_stays 'no sixth Nginx master after permanent failure' 6 no_live_master "$CASE_CONTAINER"
    readiness_is "$NP_UI_URL" 503 '{"status":"not_ready"}' || fail 'readiness did not fail after permanent Nginx death'
    say 'PASS nginx-death: fifth death permanent, no sixth master, readiness 503'
}

run_selected() {
    RS_CASE=$1
    case "$RS_CASE" in
        invalid) scenario_invalid_config invalid ;;
        missing) scenario_invalid_config missing ;;
        readonly-data) scenario_readonly_data ;;
        full-data) scenario_full_data ;;
        occupied-ui) scenario_occupied_ui ;;
        agent) scenario_agent_down ;;
        ui) scenario_ui_crash ;;
        nginx) scenario_nginx_permanent ;;
        *) fail "unknown FAULT_CASE=${RS_CASE}" ;;
    esac
}

for REQUIREMENT in docker curl jq openssl; do
    command -v "$REQUIREMENT" >/dev/null 2>&1 || fail "required command not found: ${REQUIREMENT}"
done

case "$PLATFORM" in
    ''|linux/amd64|linux/arm64) ;;
    *) fail "unsupported PLATFORM=${PLATFORM}" ;;
esac

case "$FAULT_CASE" in
    all|invalid|missing|readonly-data|full-data|occupied-ui|agent|ui|nginx) ;;
    *) fail "unknown FAULT_CASE=${FAULT_CASE}" ;;
esac

mkdir -p "$WORK_DIR"
run_bounded 10 "$WORK_DIR/image-inspect.out" docker image inspect "$IMAGE" || fail "image is unavailable: ${IMAGE}"
PASSWORD_FILE="$WORK_DIR/admin-password"
run_bounded 5 "$PASSWORD_FILE" openssl rand -hex 18
chmod 0444 "$PASSWORD_FILE"
ADMIN_PASSWORD=$(tr -d '\r\n' <"$PASSWORD_FILE")
LOGIN_FILE="$WORK_DIR/login.json"
printf '{"username":"admin","password":"%s"}\n' "$ADMIN_PASSWORD" >"$LOGIN_FILE"
chmod 0600 "$LOGIN_FILE"
unset ADMIN_PASSWORD

say "image=${IMAGE} platform=${PLATFORM:-native} run_id=${RUN_ID}"
if [ "$FAULT_CASE" = all ]; then
    for SELECTED_CASE in invalid missing readonly-data full-data occupied-ui agent ui nginx; do
        run_selected "$SELECTED_CASE"
    done
else
    run_selected "$FAULT_CASE"
fi
say 'PASS all selected Docker fault scenarios'
