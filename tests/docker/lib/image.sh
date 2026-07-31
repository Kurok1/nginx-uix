#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

REPOSITORY_ROOT=${REPOSITORY_ROOT:-$(pwd)}
BUILDX_CACHE_SEED_DIR=${BUILDX_CACHE_SEED_DIR:-}

NODE_BASE_DIGEST=9022946fb57e6fb6a2503931470deba4b49db02bfc8f587f93007fd33ef54415
GO_BASE_DIGEST=4ee9ffa999b4583ce281939cdff828763083610292f252279a0cee77473bd9a7
NGINX_BASE_DIGEST=d5b51cfc7d55fc7a7bcf4d1d577b9c3738331df56d68f0b1d8ac9795b9470a5a

. "${REPOSITORY_ROOT}/tests/docker/lib/cache.sh"

image_error() {
    printf 'image-identity: %s\n' "$*" >&2
    return 1
}

is_lower_hex() {
    lower_hex_value=$1
    lower_hex_length=$2
    [ "${#lower_hex_value}" -eq "${lower_hex_length}" ] || return 1
    case "${lower_hex_value}" in
        *[!0-9a-f]*) return 1 ;;
    esac
}

normalize_image_platform() {
    requested_platform=$1
    if [ -z "${requested_platform}" ]; then
        case "$(uname -m)" in
            amd64|x86_64) requested_platform=linux/amd64 ;;
            arm64|aarch64) requested_platform=linux/arm64 ;;
            *)
                image_error 'host architecture is not supported'
                return 1
                ;;
        esac
    fi
    case "${requested_platform}" in
        linux/amd64|linux/arm64) printf '%s\n' "${requested_platform}" ;;
        *)
            image_error 'platform must be linux/amd64 or linux/arm64'
            return 1
            ;;
    esac
}

buildx_builder_driver() {
    if [ -n "${BUILDX_BUILDER:-}" ]; then
        buildx_inspect=$(docker buildx inspect "${BUILDX_BUILDER}" 2>/dev/null) || {
            image_error 'could not inspect the Buildx builder'
            return 1
        }
    else
        buildx_inspect=$(docker buildx inspect 2>/dev/null) || {
            image_error 'could not inspect the Buildx builder'
            return 1
        }
    fi
    buildx_driver=$(printf '%s\n' "${buildx_inspect}" | awk '$1 == "Driver:" { print $2; exit }')
    case "${buildx_driver}" in
        docker|docker-container) printf '%s\n' "${buildx_driver}" ;;
        *)
            image_error 'unsupported Buildx driver'
            return 1
            ;;
    esac
}

docker_build_metadata() {
    [ -d "${REPOSITORY_ROOT}" ] && [ ! -L "${REPOSITORY_ROOT}" ] || {
        image_error 'repository root must be a regular directory'
        return 1
    }
    [ -f "${REPOSITORY_ROOT}/VERSION" ] || {
        image_error 'VERSION is unavailable at the repository root'
        return 1
    }

    PLATFORM=$(normalize_image_platform "${PLATFORM:-}") || return 1
    BUILD_VERSION=$(tr -d '\r\n' <"${REPOSITORY_ROOT}/VERSION")
    [ -n "${BUILD_VERSION}" ] || {
        image_error 'VERSION must not be empty'
        return 1
    }
    for metadata_base_digest in "${GO_BASE_DIGEST}" "${NODE_BASE_DIGEST}" "${NGINX_BASE_DIGEST}"; do
        is_lower_hex "${metadata_base_digest}" 64 || {
            image_error 'a pinned release base digest is malformed'
            return 1
        }
        grep -Eq "^FROM [^ ]+@sha256:${metadata_base_digest}( AS [^ ]+)?$" \
            "${REPOSITORY_ROOT}/deploy/docker/Dockerfile" || {
            image_error 'a pinned release base digest does not match the Dockerfile'
            return 1
        }
    done

    SOURCE_COMMIT=$(git -C "${REPOSITORY_ROOT}" rev-parse --verify HEAD 2>/dev/null) || {
        image_error 'could not read the source commit'
        return 1
    }
    case "${#SOURCE_COMMIT}" in
        40|64) ;;
        *)
            image_error 'source commit is malformed'
            return 1
            ;;
    esac
    case "${SOURCE_COMMIT}" in
        *[!0-9a-f]*)
            image_error 'source commit is malformed'
            return 1
            ;;
    esac
    SOURCE_DATE_EPOCH=$(git -C "${REPOSITORY_ROOT}" show -s --format=%ct HEAD 2>/dev/null) || {
        image_error 'could not read the source commit epoch'
        return 1
    }
    case "${SOURCE_DATE_EPOCH}" in
        ''|*[!0-9]*)
            image_error 'source commit epoch is malformed'
            return 1
            ;;
    esac
    BUILD_TIME=$(git -C "${REPOSITORY_ROOT}" show -s --format=%cI HEAD 2>/dev/null) || {
        image_error 'could not read the source commit time'
        return 1
    }
    [ -n "${BUILD_TIME}" ] || {
        image_error 'source commit time is empty'
        return 1
    }

    SOURCE_FINGERPRINT=$(
        cd "${REPOSITORY_ROOT}" &&
            go run ./tests/docker/cmd/buildmeta source --root . --ignore .dockerignore
    ) || {
        image_error 'could not compute the source fingerprint'
        return 1
    }
    is_lower_hex "${SOURCE_FINGERPRINT}" 64 || {
        image_error 'source fingerprint is malformed'
        return 1
    }

    BUILD_IDENTITY=$(
        cd "${REPOSITORY_ROOT}" &&
            go run ./tests/docker/cmd/buildmeta identity \
                --source "${SOURCE_FINGERPRINT}" \
                --platform "${PLATFORM}" \
                --base "go=${GO_BASE_DIGEST}" \
                --base "node=${NODE_BASE_DIGEST}" \
                --base "nginx=${NGINX_BASE_DIGEST}" \
                --arg "VERSION=${BUILD_VERSION}" \
                --arg "COMMIT=${SOURCE_COMMIT}" \
                --arg "BUILD_TIME=${BUILD_TIME}" \
                --arg "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"
    ) || {
        image_error 'could not compute the build identity'
        return 1
    }
    is_lower_hex "${BUILD_IDENTITY}" 64 || {
        image_error 'build identity is malformed'
        return 1
    }

    export SOURCE_FINGERPRINT SOURCE_COMMIT SOURCE_DATE_EPOCH BUILD_TIME PLATFORM BUILD_IDENTITY
}

assert_image_identity() {
    [ "$#" -eq 3 ] || {
        image_error 'assert_image_identity requires image, build identity, and source fingerprint'
        return 1
    }
    identity_image=$1
    identity_expected_build=$2
    identity_expected_source=$3
    [ -n "${identity_image}" ] || {
        image_error 'image name must not be empty'
        return 1
    }
    is_lower_hex "${identity_expected_build}" 64 || {
        image_error 'expected build identity is malformed'
        return 1
    }
    is_lower_hex "${identity_expected_source}" 64 || {
        image_error 'expected source fingerprint is malformed'
        return 1
    }
    case "${PLATFORM:-}" in
        linux/amd64|linux/arm64) ;;
        *)
            image_error 'expected image platform is unavailable'
            return 1
            ;;
    esac

    identity_record=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{index .Config.Labels "io.nginx-uix.source-fingerprint"}}|{{index .Config.Labels "io.nginx-uix.build-identity"}}|{{index .Config.Labels "io.nginx-uix.reproducible-epoch"}}|{{.Os}}/{{.Architecture}}|{{.Id}}' "${identity_image}" 2>/dev/null) || {
        image_error 'image is unavailable'
        return 1
    }
    old_identity_ifs=${IFS}
    IFS='|'
    read -r identity_version identity_revision identity_source identity_build identity_platform_epoch identity_platform identity_digest <<EOF
${identity_record}
EOF
    IFS=${old_identity_ifs}

    [ "${identity_version}" = "${BUILD_VERSION:-}" ] || {
        image_error 'image version label is missing, malformed, or mismatched'
        return 1
    }
    [ "${identity_revision}" = "${SOURCE_COMMIT:-}" ] || {
        image_error 'image revision label is missing, malformed, or mismatched'
        return 1
    }
    [ "${identity_source}" = "${identity_expected_source}" ] || {
        image_error 'image source fingerprint label is missing, malformed, or mismatched'
        return 1
    }
    [ "${identity_build}" = "${identity_expected_build}" ] || {
        image_error 'image build identity label is missing, malformed, or mismatched'
        return 1
    }
    [ "${identity_platform_epoch}" = "${SOURCE_DATE_EPOCH:-}" ] || {
        image_error 'image reproducible epoch label is missing, malformed, or mismatched'
        return 1
    }
    [ "${identity_platform}" = "${PLATFORM}" ] || {
        image_error 'image platform is mismatched'
        return 1
    }
    case "${identity_digest}" in
        sha256:*) is_lower_hex "${identity_digest#sha256:}" 64 || {
            image_error 'image digest is malformed'
            return 1
        } ;;
        *)
            image_error 'image digest is malformed'
            return 1
            ;;
    esac
    IMAGE_DIGEST=${identity_digest}
    export IMAGE_DIGEST
}

ensure_test_image() {
    [ "$#" -eq 3 ] || {
        image_error 'ensure_test_image requires image, build mode, and platform'
        return 1
    }
    ensure_image=$1
    ensure_build_mode=$2
    PLATFORM=$3
    case "${ensure_build_mode}" in
        auto|0|1) ;;
        *)
            image_error 'BUILD_IMAGE must be auto, 0, or 1'
            return 1
            ;;
    esac
    ensure_started_at=$(date +%s)
    docker_build_metadata || return 1
    ensure_driver=$(buildx_builder_driver) || return 1

    ensure_build=no
    if [ "${ensure_build_mode}" = 1 ]; then
        ensure_build=yes
    elif [ "${ensure_build_mode}" = auto ]; then
        if ! assert_image_identity "${ensure_image}" "${BUILD_IDENTITY}" "${SOURCE_FINGERPRINT}" >/dev/null 2>&1; then
            ensure_build=yes
        fi
    else
        assert_image_identity "${ensure_image}" "${BUILD_IDENTITY}" "${SOURCE_FINGERPRINT}" || return 1
    fi

    case "${ensure_driver}" in
        docker) ensure_cache_kind=daemon ;;
        docker-container) ensure_cache_kind=none ;;
    esac
    ensure_cached_lines=0
    if [ "${ensure_build}" = yes ]; then
        ensure_cache_parent="${REPOSITORY_ROOT}/.tmp"
        ensure_build_log="${ensure_cache_parent}/buildx-image.$$.log"
        if [ -e "${ensure_build_log}" ] || [ -L "${ensure_build_log}" ]; then
            image_error 'a run-owned Buildx path already exists'
            return 1
        fi

        if [ "${ensure_driver}" = docker-container ]; then
            ensure_current_cache="${ensure_cache_parent}/buildx-cache"
            ensure_staging_cache="${ensure_cache_parent}/buildx-cache.$$.new"
            ensure_backup_cache="${ensure_cache_parent}/buildx-cache.$$.old"
            for ensure_owned_path in "${ensure_staging_cache}" "${ensure_backup_cache}"; do
                if [ -e "${ensure_owned_path}" ] || [ -L "${ensure_owned_path}" ]; then
                    image_error 'a run-owned Buildx path already exists'
                    return 1
                fi
            done

            acquire_buildx_cache_lock "${ensure_cache_parent}" 60 || return 1
            if ! append_cache_from_args "${ensure_current_cache}" "${BUILDX_CACHE_SEED_DIR}"; then
                release_buildx_cache_lock >/dev/null 2>&1 || true
                return 1
            fi
            ensure_cache_kind=${BUILDX_CACHE_KIND}
        fi

        set -- docker buildx build \
            --progress=plain \
            --platform "${PLATFORM}" \
            --file "${REPOSITORY_ROOT}/deploy/docker/Dockerfile" \
            --tag "${ensure_image}" \
            --load \
            --build-arg "VERSION=${BUILD_VERSION}" \
            --build-arg "COMMIT=${SOURCE_COMMIT}" \
            --build-arg "BUILD_TIME=${BUILD_TIME}" \
            --build-arg "SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}" \
            --build-arg "BUILD_IDENTITY=${BUILD_IDENTITY}" \
            --build-arg "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"
        if [ -n "${BUILD_STEP_HTTP_PROXY:-}" ]; then
            set -- "$@" --build-arg "http_proxy=${BUILD_STEP_HTTP_PROXY}"
        fi
        if [ -n "${BUILD_STEP_HTTPS_PROXY:-}" ]; then
            set -- "$@" --build-arg "https_proxy=${BUILD_STEP_HTTPS_PROXY}"
        fi
        if [ -n "${BUILD_STEP_NO_PROXY:-}" ]; then
            set -- "$@" --build-arg "no_proxy=${BUILD_STEP_NO_PROXY}"
        fi
        if [ -n "${BUILD_STEP_GOPROXY:-}" ]; then
            set -- "$@" --build-arg "GOPROXY=${BUILD_STEP_GOPROXY}"
        fi
        if [ "${ensure_driver}" = docker-container ]; then
            set -- "$@" --cache-to "type=local,dest=${ensure_staging_cache},mode=max"
            if [ -n "${BUILDX_CACHE_FROM_CURRENT}" ]; then
                set -- "$@" --cache-from "${BUILDX_CACHE_FROM_CURRENT}"
            fi
            if [ -n "${BUILDX_CACHE_FROM_SEED}" ]; then
                set -- "$@" --cache-from "${BUILDX_CACHE_FROM_SEED}"
            fi
        fi
        set -- "$@" "${REPOSITORY_ROOT}"

        if ! "$@" >"${ensure_build_log}" 2>&1; then
            tail -n 120 "${ensure_build_log}" >&2 || true
            rm -f "${ensure_build_log}" >/dev/null 2>&1 || true
            if [ "${ensure_driver}" = docker-container ]; then
                rm -rf "${ensure_staging_cache}" >/dev/null 2>&1 || true
                release_buildx_cache_lock >/dev/null 2>&1 || true
            fi
            image_error 'Buildx image build failed'
            return 1
        fi
        ensure_cached_lines=$(grep -c 'CACHED' "${ensure_build_log}" || true)
        rm -f "${ensure_build_log}" || {
            if [ "${ensure_driver}" = docker-container ]; then
                release_buildx_cache_lock >/dev/null 2>&1 || true
            fi
            image_error 'could not remove the bounded Buildx log'
            return 1
        }
        if [ "${ensure_driver}" = docker-container ]; then
            if ! publish_buildx_cache "${ensure_staging_cache}" "${ensure_current_cache}" "${ensure_backup_cache}"; then
                release_buildx_cache_lock >/dev/null 2>&1 || true
                return 1
            fi
            release_buildx_cache_lock || return 1
        fi
        assert_image_identity "${ensure_image}" "${BUILD_IDENTITY}" "${SOURCE_FINGERPRINT}" || return 1
    elif [ "${ensure_build_mode}" = auto ]; then
        assert_image_identity "${ensure_image}" "${BUILD_IDENTITY}" "${SOURCE_FINGERPRINT}" || return 1
    fi

    ensure_finished_at=$(date +%s)
    ensure_duration=$((ensure_finished_at - ensure_started_at))
    printf 'image_evidence source_fingerprint=%s build_identity=%s platform=%s driver=%s image_digest=%s build=%s duration_seconds=%s cache=%s cached_lines=%s\n' \
        "${SOURCE_FINGERPRINT}" "${BUILD_IDENTITY}" "${PLATFORM}" "${ensure_driver}" "${IMAGE_DIGEST}" \
        "${ensure_build}" "${ensure_duration}" "${ensure_cache_kind}" "${ensure_cached_lines}"
}
