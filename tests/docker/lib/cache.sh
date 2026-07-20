#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.2.1

BUILDX_CACHE_LOCK_DIR=${BUILDX_CACHE_LOCK_DIR:-}
BUILDX_CACHE_LOCK_PID=${BUILDX_CACHE_LOCK_PID:-}
BUILDX_CACHE_LOCK_OWNED=${BUILDX_CACHE_LOCK_OWNED:-0}
BUILDX_CACHE_FROM_CURRENT=
BUILDX_CACHE_FROM_SEED=
BUILDX_CACHE_KIND=none

buildx_cache_error() {
    printf 'buildx-cache: %s\n' "$*" >&2
    return 1
}

validate_buildx_cache() {
    validate_cache_dir=$1
    [ -n "${validate_cache_dir}" ] || {
        buildx_cache_error 'cache path is empty'
        return 1
    }
    [ ! -L "${validate_cache_dir}" ] || {
        buildx_cache_error 'cache directory must not be a symbolic link'
        return 1
    }
    [ -d "${validate_cache_dir}" ] || {
        buildx_cache_error 'cache directory does not exist'
        return 1
    }
    [ ! -L "${validate_cache_dir}/index.json" ] || {
        buildx_cache_error 'cache index must not be a symbolic link'
        return 1
    }
    [ -f "${validate_cache_dir}/index.json" ] || {
        buildx_cache_error 'cache index is not a regular file'
        return 1
    }
    command -v jq >/dev/null 2>&1 || {
        buildx_cache_error 'jq is required to validate BuildKit cache metadata'
        return 1
    }
    jq -e '.schemaVersion == 2 and (.manifests | type == "array")' \
        "${validate_cache_dir}/index.json" >/dev/null 2>&1 || {
        buildx_cache_error 'cache index is not valid BuildKit OCI metadata'
        return 1
    }
}

buildx_cache_lock_is_owned() {
    [ "${BUILDX_CACHE_LOCK_OWNED}" = 1 ] || return 1
    [ -n "${BUILDX_CACHE_LOCK_DIR}" ] || return 1
    [ ! -L "${BUILDX_CACHE_LOCK_DIR}" ] || return 1
    [ -d "${BUILDX_CACHE_LOCK_DIR}" ] || return 1
    [ ! -L "${BUILDX_CACHE_LOCK_DIR}/pid" ] || return 1
    [ -f "${BUILDX_CACHE_LOCK_DIR}/pid" ] || return 1
    lock_recorded_pid=$(sed -n '1p' "${BUILDX_CACHE_LOCK_DIR}/pid")
    [ "${lock_recorded_pid}" = "${BUILDX_CACHE_LOCK_PID}" ] || return 1
    [ "${lock_recorded_pid}" = "$$" ]
}

acquire_buildx_cache_lock() {
    cache_parent=$1
    cache_timeout=$2

    case "${cache_timeout}" in
        ''|*[!0-9]*)
            buildx_cache_error 'lock timeout must be an integer from 1 to 60 seconds'
            return 1
            ;;
    esac
    if [ "${cache_timeout}" -lt 1 ] || [ "${cache_timeout}" -gt 60 ]; then
        buildx_cache_error 'lock timeout must be an integer from 1 to 60 seconds'
        return 1
    fi
    [ "${BUILDX_CACHE_LOCK_OWNED}" = 0 ] || {
        buildx_cache_error 'this process already owns a Buildx cache lock'
        return 1
    }
    [ -n "${cache_parent}" ] || {
        buildx_cache_error 'cache parent path is empty'
        return 1
    }
    [ ! -L "${cache_parent}" ] || {
        buildx_cache_error 'cache parent must not be a symbolic link'
        return 1
    }
    mkdir -p "${cache_parent}" || {
        buildx_cache_error 'could not create cache parent'
        return 1
    }
    [ -d "${cache_parent}" ] && [ ! -L "${cache_parent}" ] || {
        buildx_cache_error 'cache parent is not a regular directory'
        return 1
    }

    cache_lock_dir="${cache_parent}/buildx-cache.lock"
    cache_deadline=$(($(date +%s) + cache_timeout))
    while ! mkdir "${cache_lock_dir}" 2>/dev/null; do
        if [ "$(date +%s)" -ge "${cache_deadline}" ]; then
            buildx_cache_error 'timed out waiting for the Buildx cache lock'
            return 1
        fi
        sleep 1
    done

    if ! (umask 077 && printf '%s\n' "$$" >"${cache_lock_dir}/pid"); then
        rmdir "${cache_lock_dir}" >/dev/null 2>&1 || true
        buildx_cache_error 'could not record Buildx cache lock ownership'
        return 1
    fi
    BUILDX_CACHE_LOCK_DIR=${cache_lock_dir}
    BUILDX_CACHE_LOCK_PID=$$
    BUILDX_CACHE_LOCK_OWNED=1
}

release_buildx_cache_lock() {
    buildx_cache_lock_is_owned || {
        buildx_cache_error 'refusing to release a Buildx cache lock not owned by this process'
        return 1
    }
    rm -f "${BUILDX_CACHE_LOCK_DIR}/pid" || {
        buildx_cache_error 'could not remove owned Buildx cache lock marker'
        return 1
    }
    rmdir "${BUILDX_CACHE_LOCK_DIR}" || {
        buildx_cache_error 'could not remove owned Buildx cache lock directory'
        return 1
    }
    BUILDX_CACHE_LOCK_DIR=
    BUILDX_CACHE_LOCK_PID=
    BUILDX_CACHE_LOCK_OWNED=0
}

append_cache_from_args() {
    current_cache=$1
    seed_cache=$2
    BUILDX_CACHE_FROM_CURRENT=
    BUILDX_CACHE_FROM_SEED=
    BUILDX_CACHE_KIND=none

    if [ -e "${current_cache}" ] || [ -L "${current_cache}" ]; then
        validate_buildx_cache "${current_cache}" || return 1
        BUILDX_CACHE_FROM_CURRENT="type=local,src=${current_cache}"
        BUILDX_CACHE_KIND=current
    fi
    if [ -n "${seed_cache}" ]; then
        validate_buildx_cache "${seed_cache}" || return 1
        BUILDX_CACHE_FROM_SEED="type=local,src=${seed_cache}"
        if [ "${BUILDX_CACHE_KIND}" = none ]; then
            BUILDX_CACHE_KIND=seed
        fi
    fi
}

validate_buildx_cache_publication_paths() {
    publication_staging=$1
    publication_current=$2
    publication_backup=$3
    case "${publication_staging}:${publication_current}:${publication_backup}" in
        /*:/*:/*) ;;
        *)
            buildx_cache_error 'cache publication paths must be absolute'
            return 1
            ;;
    esac
    publication_parent=$(dirname "${publication_current}")
    [ "$(dirname "${publication_staging}")" = "${publication_parent}" ] &&
        [ "$(dirname "${publication_backup}")" = "${publication_parent}" ] || {
        buildx_cache_error 'cache publication paths must share one parent'
        return 1
    }
    [ "$(basename "${publication_current}")" = buildx-cache ] || {
        buildx_cache_error 'current cache path has an unexpected name'
        return 1
    }
    case "$(basename "${publication_staging}")" in
        buildx-cache.*.new) ;;
        *)
            buildx_cache_error 'staging cache path has an unexpected name'
            return 1
            ;;
    esac
    case "$(basename "${publication_backup}")" in
        buildx-cache.*.old) ;;
        *)
            buildx_cache_error 'backup cache path has an unexpected name'
            return 1
            ;;
    esac
    [ -d "${publication_parent}" ] && [ ! -L "${publication_parent}" ] || {
        buildx_cache_error 'cache publication parent is not a regular directory'
        return 1
    }
}

publish_buildx_cache() {
    staging_cache=$1
    current_cache=$2
    backup_cache=$3

    buildx_cache_lock_is_owned || {
        buildx_cache_error 'cache publication requires the owned Buildx cache lock'
        return 1
    }
    validate_buildx_cache_publication_paths "${staging_cache}" "${current_cache}" "${backup_cache}" || return 1
    validate_buildx_cache "${staging_cache}" || return 1
    if [ -e "${backup_cache}" ] || [ -L "${backup_cache}" ]; then
        buildx_cache_error 'cache backup path already exists'
        return 1
    fi

    cache_had_previous=0
    if [ -e "${current_cache}" ] || [ -L "${current_cache}" ]; then
        validate_buildx_cache "${current_cache}" || return 1
        mv "${current_cache}" "${backup_cache}" || {
            buildx_cache_error 'could not move current cache to backup'
            return 1
        }
        cache_had_previous=1
    fi

    if ! mv "${staging_cache}" "${current_cache}"; then
        if [ "${cache_had_previous}" = 1 ]; then
            if ! mv "${backup_cache}" "${current_cache}"; then
                buildx_cache_error 'could not restore the previous cache after publication failure'
                return 1
            fi
        fi
        buildx_cache_error 'could not publish the new Buildx cache'
        return 1
    fi

    if [ "${cache_had_previous}" = 1 ]; then
        validate_buildx_cache "${backup_cache}" || return 1
        rm -rf "${backup_cache}" || {
            buildx_cache_error 'could not remove the previous cache backup'
            return 1
        }
    fi
}
