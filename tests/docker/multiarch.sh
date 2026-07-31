#!/bin/sh
# @author hanchao <hanchao@66yunlian.com>
# @since 0.1.0

set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd "${SCRIPT_DIR}/../.." && pwd)
cd "${REPOSITORY_ROOT}"

# shellcheck source=lib/image.sh
. "${SCRIPT_DIR}/lib/image.sh"
# shellcheck source=lib/playwright_summary.sh
. "${SCRIPT_DIR}/lib/playwright_summary.sh"

VERSION=$(tr -d '\n' < VERSION)
RUN_RANDOM=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
RUN_ID="t18-$$-${RUN_RANDOM}"

TEMP_DIR=""
OWN_CONTAINERS=""
OWN_BUILDERS=""
OWN_IMAGES=""
OWN_VOLUMES=""
OWN_CACHE_PATHS=""
AMD64_CACHE_KIND=none
ARM64_CACHE_KIND=none

AMD64_IMAGE="nginx-uix:${VERSION}-multiarch-${RUN_ID}-amd64"
ARM64_IMAGE="nginx-uix:${VERSION}-multiarch-${RUN_ID}-arm64"
PLAYWRIGHT_IMAGE="nginx-uix-playwright:${VERSION}-${RUN_ID}"
RESOURCE_PREFIX="nginx-uix-${RUN_ID}"
BUILDER_NAME="nginx-uix-${RUN_ID}"
BUILDX_CACHE_PARENT="${REPOSITORY_ROOT}/.tmp"
BUILDX_CACHE_DIR="${BUILDX_CACHE_PARENT}/buildx-cache"
BUILDX_CACHE_SEED_DIR=${BUILDX_CACHE_SEED_DIR:-}
NATIVE_IMAGE=${NATIVE_IMAGE:-}

NGINX_BASE='nginx:1.30.3-alpine-slim@sha256:d5b51cfc7d55fc7a7bcf4d1d577b9c3738331df56d68f0b1d8ac9795b9470a5a'
PLAYWRIGHT_BASE='mcr.microsoft.com/playwright:v1.61.0-noble@sha256:57b65fdc9ceabe0ef613124c7bbe2babcf9362c4d85e382fe3b03604e84b428a'
SBOM_GENERATOR='docker/buildkit-syft-scanner@sha256:79e7b013cbec16bbb436f312819a49a4a57752b2270c1a9332ae1a10fcc82a68'

log() {
  printf '[multiarch] %s\n' "$*"
}

fail() {
  printf '[multiarch] ERROR: %s\n' "$*" >&2
  exit 1
}

register_cache_path() {
  register_cache_path_value=$1
  [ "$(dirname "${register_cache_path_value}")" = "${BUILDX_CACHE_PARENT}" ] ||
    fail 'run-owned cache path has an unexpected parent'
  case "$(basename "${register_cache_path_value}")" in
    "buildx-cache.${RUN_ID}-"*.new | "buildx-cache.${RUN_ID}-"*.old) ;;
    *) fail 'run-owned cache path has an unexpected name' ;;
  esac
  if [ -e "${register_cache_path_value}" ] || [ -L "${register_cache_path_value}" ]; then
    fail 'refusing an existing run-owned cache path'
  fi
  OWN_CACHE_PATHS="${OWN_CACHE_PATHS} ${register_cache_path_value}"
}

remove_owned_cache_paths() {
  for remove_cache_path in ${OWN_CACHE_PATHS}; do
    case "${remove_cache_path}" in
      *.old)
        if [ -d "${remove_cache_path}" ] && [ ! -L "${remove_cache_path}" ]; then
          if [ ! -e "${BUILDX_CACHE_DIR}" ] && [ ! -L "${BUILDX_CACHE_DIR}" ] &&
            validate_buildx_cache "${remove_cache_path}" >/dev/null 2>&1; then
            mv "${remove_cache_path}" "${BUILDX_CACHE_DIR}" >/dev/null 2>&1 ||
              printf '[multiarch] WARNING: previous Buildx cache remains in its run-owned backup\n' >&2
            continue
          fi
          if ! validate_buildx_cache "${BUILDX_CACHE_DIR}" >/dev/null 2>&1; then
            printf '[multiarch] WARNING: refusing to remove previous cache backup while current is invalid\n' >&2
            continue
          fi
        fi
        ;;
    esac
    if [ -L "${remove_cache_path}" ] || [ -f "${remove_cache_path}" ]; then
      rm -f "${remove_cache_path}" >/dev/null 2>&1 || true
    elif [ -d "${remove_cache_path}" ]; then
      rm -rf "${remove_cache_path}" >/dev/null 2>&1 || true
    fi
  done
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
  if [ "${BUILDX_CACHE_LOCK_OWNED:-0}" = 1 ]; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
  fi
  remove_owned_cache_paths

  exit "${cleanup_rc}"
}

on_signal() {
  exit 130
}

# Install cleanup before creating temporary files or Docker resources.
trap cleanup EXIT
trap on_signal HUP INT TERM

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

retry() {
  retry_limit=$1
  shift
  retry_attempt=1
  while :; do
    if "$@"; then
      return 0
    fi
    if [ "${retry_attempt}" -ge "${retry_limit}" ]; then
      return 1
    fi
    log "attempt ${retry_attempt}/${retry_limit} failed; retrying the unchanged command"
    sleep $((retry_attempt * 2))
    retry_attempt=$((retry_attempt + 1))
  done
}

digest_file() (
  digest_path=$1
  if command -v sha256sum >/dev/null 2>&1; then
    LC_ALL=C sha256sum "${digest_path}" | awk '{print $1}'
  else
    LC_ALL=C shasum -a 256 "${digest_path}" | awk '{print $1}'
  fi
)

build_platform_oci_once() {
  if [ -e "${PLATFORM_STAGING_CACHE}" ] || [ -L "${PLATFORM_STAGING_CACHE}" ]; then
    rm -rf "${PLATFORM_STAGING_CACHE}" || return 1
  fi
  rm -f "${PLATFORM_OCI_ARCHIVE}"

  acquire_buildx_cache_lock "${BUILDX_CACHE_PARENT}" 60 || return 1
  if ! append_cache_from_args "${BUILDX_CACHE_DIR}" "${BUILDX_CACHE_SEED_DIR}"; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
    return 1
  fi
  printf '%s\n' "${BUILDX_CACHE_KIND}" >"${TEMP_DIR}/cache-${PLATFORM_ARCH}.kind"
  log "linux/${PLATFORM_ARCH} cache imports: ${BUILDX_CACHE_KIND}"

  set -- docker buildx build \
    --builder "${BUILDER_NAME}" \
    --progress=plain \
    --platform "linux/${PLATFORM_ARCH}" \
    --provenance=false \
    --attest "type=sbom,generator=${SBOM_GENERATOR}" \
    --tag "${PLATFORM_IMAGE}" \
    --output "type=oci,dest=${PLATFORM_OCI_ARCHIVE}" \
    --cache-to "type=local,dest=${PLATFORM_STAGING_CACHE},mode=max" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "COMMIT=${SOURCE_COMMIT}" \
    --build-arg "BUILD_TIME=${BUILD_TIME}" \
    --build-arg "SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}" \
    --build-arg "BUILD_IDENTITY=${PLATFORM_BUILD_IDENTITY}" \
    --build-arg "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"
  if [ -n "${BUILDX_CACHE_FROM_CURRENT}" ]; then
    set -- "$@" --cache-from "${BUILDX_CACHE_FROM_CURRENT}"
  fi
  if [ -n "${BUILDX_CACHE_FROM_SEED}" ]; then
    set -- "$@" --cache-from "${BUILDX_CACHE_FROM_SEED}"
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
  if [ -n "${BUILD_STEP_GOPROXY:-}" ]; then
    set -- "$@" --build-arg "GOPROXY=${BUILD_STEP_GOPROXY}"
  fi
  set -- "$@" --file deploy/docker/Dockerfile .

  if ! "$@"; then
    rm -rf "${PLATFORM_STAGING_CACHE}" >/dev/null 2>&1 || true
    release_buildx_cache_lock >/dev/null 2>&1 || true
    return 1
  fi
  if ! publish_buildx_cache \
    "${PLATFORM_STAGING_CACHE}" "${BUILDX_CACHE_DIR}" "${PLATFORM_BACKUP_CACHE}"; then
    release_buildx_cache_lock >/dev/null 2>&1 || true
    return 1
  fi
  release_buildx_cache_lock || return 1
}

build_platform_oci() {
  PLATFORM_ARCH=$1
  PLATFORM_IMAGE=$2
  PLATFORM_BUILD_IDENTITY=$3
  PLATFORM_OCI_ARCHIVE=$4
  PLATFORM_STAGING_CACHE="${BUILDX_CACHE_PARENT}/buildx-cache.${RUN_ID}-${PLATFORM_ARCH}.new"
  PLATFORM_BACKUP_CACHE="${BUILDX_CACHE_PARENT}/buildx-cache.${RUN_ID}-${PLATFORM_ARCH}.old"
  register_cache_path "${PLATFORM_STAGING_CACHE}"
  register_cache_path "${PLATFORM_BACKUP_CACHE}"
  retry 3 build_platform_oci_once || fail "linux/${PLATFORM_ARCH} OCI build failed"
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
  oci_root=$1
  oci_digest=$2
  case "${oci_digest}" in
    sha256:[0-9a-f][0-9a-f]*) ;;
    *) fail "unexpected OCI digest: ${oci_digest}" ;;
  esac
  oci_hex=${oci_digest#sha256:}
  [ "${#oci_hex}" -eq 64 ] || fail "unexpected OCI digest length: ${oci_digest}"
  case "${oci_hex}" in
    *[!0-9a-f]*) fail "unexpected OCI digest: ${oci_digest}" ;;
  esac
  printf '%s/blobs/sha256/%s\n' "${oci_root}" "${oci_hex}"
)

verify_oci_blob() (
  verify_root=$1
  verify_digest=$2
  verify_size=$3
  verify_path=$(oci_blob_path "${verify_root}" "${verify_digest}")
  [ -f "${verify_path}" ] && [ ! -L "${verify_path}" ] ||
    fail "OCI descriptor blob is missing: ${verify_digest}"
  verify_actual=$(digest_file "${verify_path}")
  [ "sha256:${verify_actual}" = "${verify_digest}" ] || fail "OCI blob digest mismatch: ${verify_digest}"
  verify_actual_size=$(wc -c <"${verify_path}" | tr -d ' ')
  [ "${verify_actual_size}" = "${verify_size}" ] || fail "OCI blob size mismatch: ${verify_digest}"
)

platform_index_path() (
  platform_index_root=$1
  platform_index_top="${platform_index_root}/index.json"
  jq -e '
    .schemaVersion == 2 and
    .mediaType == "application/vnd.oci.image.index.v1+json" and
    (.manifests | type == "array" and length > 0)
  ' "${platform_index_top}" >/dev/null || fail 'OCI layout top-level index is malformed'
  platform_index_wrappers=$(jq -er '
    [.manifests[] | select(.mediaType == "application/vnd.oci.image.index.v1+json")] | length
  ' "${platform_index_top}")
  case "${platform_index_wrappers}" in
    0)
      printf '%s\n' "${platform_index_top}"
      ;;
    1)
      [ "$(jq -er '.manifests | length' "${platform_index_top}")" = 1 ] ||
        fail 'OCI layout mixes an index wrapper with sibling descriptors'
      platform_index_digest=$(jq -er '.manifests[0].digest' "${platform_index_top}")
      platform_index_size=$(jq -er '.manifests[0].size' "${platform_index_top}")
      verify_oci_blob "${platform_index_root}" "${platform_index_digest}" "${platform_index_size}"
      platform_index_inner=$(oci_blob_path "${platform_index_root}" "${platform_index_digest}")
      jq -e '
        .schemaVersion == 2 and
        .mediaType == "application/vnd.oci.image.index.v1+json" and
        (.manifests | type == "array" and length > 0)
      ' "${platform_index_inner}" >/dev/null || fail 'OCI layout nested platform index is malformed'
      printf '%s\n' "${platform_index_inner}"
      ;;
    *)
      fail 'OCI layout has multiple top-level index wrappers'
      ;;
  esac
)

verify_sbom_attestation() (
  verify_sbom_root=$1
  verify_sbom_arch=$2
  verify_sbom_subject=$3
  verify_sbom_index=$4
  verify_sbom_count=$(jq -er --arg subject "${verify_sbom_subject}" '
    [.manifests[] | select(
      .platform.os == "unknown" and .platform.architecture == "unknown" and
      .annotations["vnd.docker.reference.type"] == "attestation-manifest" and
      .annotations["vnd.docker.reference.digest"] == $subject
    )] | length
  ' "${verify_sbom_index}")
  [ "${verify_sbom_count}" = 1 ] ||
    fail "linux/${verify_sbom_arch} does not have exactly one attached SBOM manifest"

  verify_sbom_manifest_digest=$(jq -er --arg subject "${verify_sbom_subject}" '
    .manifests[] | select(
      .annotations["vnd.docker.reference.type"] == "attestation-manifest" and
      .annotations["vnd.docker.reference.digest"] == $subject
    ) | .digest
  ' "${verify_sbom_index}")
  verify_sbom_manifest_size=$(jq -er --arg subject "${verify_sbom_subject}" '
    .manifests[] | select(
      .annotations["vnd.docker.reference.type"] == "attestation-manifest" and
      .annotations["vnd.docker.reference.digest"] == $subject
    ) | .size
  ' "${verify_sbom_index}")
  verify_oci_blob "${verify_sbom_root}" "${verify_sbom_manifest_digest}" "${verify_sbom_manifest_size}"
  verify_sbom_manifest_path=$(oci_blob_path "${verify_sbom_root}" "${verify_sbom_manifest_digest}")
  jq -e '
    .schemaVersion == 2 and
    .mediaType == "application/vnd.oci.image.manifest.v1+json" and
    .config.mediaType == "application/vnd.oci.image.config.v1+json" and
    ([.layers[] | select(
      .mediaType == "application/vnd.in-toto+json" and
      .annotations["in-toto.io/predicate-type"] == "https://spdx.dev/Document"
    )] | length) == 1
  ' "${verify_sbom_manifest_path}" >/dev/null ||
    fail "linux/${verify_sbom_arch} SBOM attestation manifest is malformed"

  verify_sbom_config_digest=$(jq -er '.config.digest' "${verify_sbom_manifest_path}")
  verify_sbom_config_size=$(jq -er '.config.size' "${verify_sbom_manifest_path}")
  verify_oci_blob "${verify_sbom_root}" "${verify_sbom_config_digest}" "${verify_sbom_config_size}"
  verify_sbom_layer_digest=$(jq -er '
    .layers[] | select(
      .mediaType == "application/vnd.in-toto+json" and
      .annotations["in-toto.io/predicate-type"] == "https://spdx.dev/Document"
    ) | .digest
  ' "${verify_sbom_manifest_path}")
  verify_sbom_layer_size=$(jq -er '
    .layers[] | select(
      .mediaType == "application/vnd.in-toto+json" and
      .annotations["in-toto.io/predicate-type"] == "https://spdx.dev/Document"
    ) | .size
  ' "${verify_sbom_manifest_path}")
  verify_oci_blob "${verify_sbom_root}" "${verify_sbom_layer_digest}" "${verify_sbom_layer_size}"
  verify_sbom_layer_path=$(oci_blob_path "${verify_sbom_root}" "${verify_sbom_layer_digest}")
  verify_sbom_subject_hex=${verify_sbom_subject#sha256:}
  jq -e --arg subject "${verify_sbom_subject_hex}" '
    ._type == "https://in-toto.io/Statement/v1" and
    .predicateType == "https://spdx.dev/Document" and
    any(.subject[]; .digest.sha256 == $subject) and
    (.predicate.spdxVersion | startswith("SPDX-")) and
    .predicate.SPDXID == "SPDXRef-DOCUMENT" and
    (.predicate.packages | type == "array" and length > 0)
  ' "${verify_sbom_layer_path}" >/dev/null ||
    fail "linux/${verify_sbom_arch} SPDX SBOM statement is malformed or detached"
  verify_sbom_packages=$(jq -er '.predicate.packages | length' "${verify_sbom_layer_path}")
  printf '%s\n' "${verify_sbom_layer_digest}" >"${TEMP_DIR}/sbom-${verify_sbom_arch}.digest"
  printf '%s\n' "${verify_sbom_packages}" >"${TEMP_DIR}/sbom-${verify_sbom_arch}.packages"
)

verify_platform_layout() (
  verify_layout_root=$1
  verify_arch=$2
  verify_build_identity=$3
  verify_index=$(platform_index_path "${verify_layout_root}")
  [ -f "${verify_layout_root}/oci-layout" ] && [ ! -L "${verify_layout_root}/oci-layout" ] ||
    fail "linux/${verify_arch} OCI layout marker is missing"
  [ -f "${verify_index}" ] && [ ! -L "${verify_index}" ] ||
    fail "linux/${verify_arch} OCI index is missing"
  if ! jq -e --arg arch "${verify_arch}" '
    .schemaVersion == 2 and
    .mediaType == "application/vnd.oci.image.index.v1+json" and
    ([.manifests[] | select(
      .mediaType == "application/vnd.oci.image.manifest.v1+json" and
      .platform.os == "linux" and .platform.architecture == $arch
    )] | length) == 1 and
    ([.manifests[] | select(
      .mediaType == "application/vnd.oci.image.manifest.v1+json" and
      .platform.os == "unknown" and .platform.architecture == "unknown" and
      .annotations["vnd.docker.reference.type"] == "attestation-manifest"
    )] | length) == 1
  ' "${verify_index}" >/dev/null; then
    jq . "${verify_index}" >&2 || true
    fail "linux/${verify_arch} OCI index is malformed"
  fi

  verify_manifest_digest=$(jq -er --arg arch "${verify_arch}" '
    .manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest
  ' "${verify_index}")
  verify_manifest_size=$(jq -er --arg arch "${verify_arch}" '
    .manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .size
  ' "${verify_index}")
  verify_oci_blob "${verify_layout_root}" "${verify_manifest_digest}" "${verify_manifest_size}"
  verify_manifest_path=$(oci_blob_path "${verify_layout_root}" "${verify_manifest_digest}")
  jq -e \
    '.schemaVersion == 2 and
     .mediaType == "application/vnd.oci.image.manifest.v1+json" and
     .config.mediaType == "application/vnd.oci.image.config.v1+json" and
     (.layers | type == "array" and length > 0)' \
    "${verify_manifest_path}" >/dev/null || fail "linux/${verify_arch} OCI manifest is malformed"

  verify_config_digest=$(jq -er '.config.digest' "${verify_manifest_path}")
  verify_config_size=$(jq -er '.config.size' "${verify_manifest_path}")
  verify_oci_blob "${verify_layout_root}" "${verify_config_digest}" "${verify_config_size}"
  jq -er '.layers[] | [.digest, (.size | tostring)] | @tsv' \
    "${verify_manifest_path}" >"${TEMP_DIR}/layers-${verify_arch}.tsv"
  while IFS="$(printf '\t')" read -r verify_layer_digest verify_layer_size; do
    verify_oci_blob "${verify_layout_root}" "${verify_layer_digest}" "${verify_layer_size}"
  done <"${TEMP_DIR}/layers-${verify_arch}.tsv"

  verify_config_path=$(oci_blob_path "${verify_layout_root}" "${verify_config_digest}")
  if ! jq -e \
    --arg arch "${verify_arch}" \
    --arg version "${VERSION}" \
    --arg revision "${SOURCE_COMMIT}" \
    --arg source "${SOURCE_FINGERPRINT}" \
    --arg identity "${verify_build_identity}" \
    --arg epoch "${SOURCE_DATE_EPOCH}" \
    '.os == "linux" and .architecture == $arch and
     .config.Labels["org.opencontainers.image.version"] == $version and
     .config.Labels["org.opencontainers.image.revision"] == $revision and
     .config.Labels["org.opencontainers.image.licenses"] == "Apache-2.0" and
     .config.Labels["io.nginx-uix.source-fingerprint"] == $source and
     .config.Labels["io.nginx-uix.build-identity"] == $identity and
     .config.Labels["io.nginx-uix.reproducible-epoch"] == $epoch and
     .config.Healthcheck.Test == ["CMD", "/usr/local/bin/nginx-uix", "healthcheck"] and
     .config.Entrypoint == ["/init"] and
     ((.config.Cmd // []) == []) and
     .config.StopSignal == "SIGTERM" and
     (.config.ExposedPorts | keys | sort) == ["443/tcp", "80/tcp", "9000/tcp"] and
     (.config.Volumes | keys | sort) == ["/etc/nginx", "/var/lib/nginx-uix"]' \
    "${verify_config_path}" >/dev/null; then
    jq . "${verify_config_path}" >&2 || true
    fail "linux/${verify_arch} OCI config identity is mismatched"
  fi

  verify_sbom_attestation \
    "${verify_layout_root}" "${verify_arch}" "${verify_manifest_digest}" "${verify_index}"
  printf '%s\n' "${verify_manifest_digest}"
)

copy_verified_blob() {
  copy_blob_root=$1
  copy_blob_digest=$2
  copy_blob_size=$3
  verify_oci_blob "${copy_blob_root}" "${copy_blob_digest}" "${copy_blob_size}"
  copy_blob_source=$(oci_blob_path "${copy_blob_root}" "${copy_blob_digest}")
  copy_blob_destination=$(oci_blob_path "${OCI_ROOT}" "${copy_blob_digest}")
  if [ -e "${copy_blob_destination}" ] || [ -L "${copy_blob_destination}" ]; then
    verify_oci_blob "${OCI_ROOT}" "${copy_blob_digest}" "${copy_blob_size}"
    return 0
  fi
  cp "${copy_blob_source}" "${copy_blob_destination}.new"
  copy_blob_new_digest=$(digest_file "${copy_blob_destination}.new")
  copy_blob_new_size=$(wc -c <"${copy_blob_destination}.new" | tr -d ' ')
  [ "sha256:${copy_blob_new_digest}" = "${copy_blob_digest}" ] &&
    [ "${copy_blob_new_size}" = "${copy_blob_size}" ] ||
    fail "copied OCI blob changed: ${copy_blob_digest}"
  mv "${copy_blob_destination}.new" "${copy_blob_destination}"
}

copy_verified_platform_blobs() {
  copy_root=$1
  copy_arch=$2
  copy_index=$(platform_index_path "${copy_root}")
  jq -er '.manifests[] | [.digest, (.size | tostring)] | @tsv' \
    "${copy_index}" >"${TEMP_DIR}/manifests-${copy_arch}.tsv"
  copy_manifest_ordinal=0
  while IFS="$(printf '\t')" read -r copy_manifest_digest copy_manifest_size; do
    copy_manifest_ordinal=$((copy_manifest_ordinal + 1))
    copy_verified_blob "${copy_root}" "${copy_manifest_digest}" "${copy_manifest_size}"
    copy_manifest_path=$(oci_blob_path "${copy_root}" "${copy_manifest_digest}")
    jq -er '.config, .layers[] | [.digest, (.size | tostring)] | @tsv' \
      "${copy_manifest_path}" >"${TEMP_DIR}/children-${copy_arch}-${copy_manifest_ordinal}.tsv"
    while IFS="$(printf '\t')" read -r copy_child_digest copy_child_size; do
      copy_verified_blob "${copy_root}" "${copy_child_digest}" "${copy_child_size}"
    done <"${TEMP_DIR}/children-${copy_arch}-${copy_manifest_ordinal}.tsv"
  done <"${TEMP_DIR}/manifests-${copy_arch}.tsv"
}

write_platform_descriptors() {
  descriptor_root=$1
  descriptor_arch=$2
  descriptor_output=$3
  descriptor_index=$(platform_index_path "${descriptor_root}")
  jq -cS --arg arch "${descriptor_arch}" '
    .manifests[] | select(
      (.platform.os == "linux" and .platform.architecture == $arch) or
      (.platform.os == "unknown" and .platform.architecture == "unknown" and
       .annotations["vnd.docker.reference.type"] == "attestation-manifest")
    )
  ' \
    "${descriptor_index}" >"${descriptor_output}"
}

merge_platform_layouts() {
  rm -rf "${OCI_ROOT}"
  mkdir -p "${OCI_ROOT}/blobs/sha256"
  printf '%s\n' '{"imageLayoutVersion":"1.0.0"}' >"${OCI_ROOT}/oci-layout"
  copy_verified_platform_blobs "${AMD64_OCI_ROOT}" amd64
  copy_verified_platform_blobs "${ARM64_OCI_ROOT}" arm64
  write_platform_descriptors "${AMD64_OCI_ROOT}" amd64 "${TEMP_DIR}/descriptor-amd64.json"
  write_platform_descriptors "${ARM64_OCI_ROOT}" arm64 "${TEMP_DIR}/descriptor-arm64.json"
  jq -cS -s \
    '{schemaVersion: 2,
      mediaType: "application/vnd.oci.image.index.v1+json",
      manifests: sort_by(
        if .platform.os == "linux" then
          "0/" + .platform.architecture
        else
          "1/" + .annotations["vnd.docker.reference.digest"]
        end
      )}' \
    "${TEMP_DIR}/descriptor-amd64.json" "${TEMP_DIR}/descriptor-arm64.json" \
    >"${OCI_ROOT}/index.json.new"
  mv "${OCI_ROOT}/index.json.new" "${OCI_ROOT}/index.json"
  jq -e \
    '.schemaVersion == 2 and (.manifests | length) == 4 and
     ([.manifests[] | select(.platform.os == "linux") |
       (.platform.os + "/" + .platform.architecture)] ==
       ["linux/amd64", "linux/arm64"]) and
     (([.manifests[] | select(.platform.os == "unknown") |
       .annotations["vnd.docker.reference.digest"]] | sort) ==
      ([.manifests[] | select(.platform.os == "linux") | .digest] | sort)) and
     all(.manifests[] | select(.platform.os == "unknown");
       .platform.architecture == "unknown" and
       .annotations["vnd.docker.reference.type"] == "attestation-manifest")' \
    "${OCI_ROOT}/index.json" >/dev/null ||
    fail 'merged OCI index does not contain two sorted platforms and their attached SBOMs'
  OCI_LAYOUT_INDEX_DIGEST="sha256:$(digest_file "${OCI_ROOT}/index.json")"
}

extract_platform_layout() {
  extract_archive=$1
  extract_root=$2
  rm -rf "${extract_root}"
  mkdir -p "${extract_root}"
  LC_ALL=C tar -xf "${extract_archive}" -C "${extract_root}"
}

load_platform_image() {
  load_arch=$1
  load_image=$2
  load_archive=$3
  load_expected_identity=$4
  register_image "${load_image}"
  docker load --input "${load_archive}" >/dev/null
  PLATFORM="linux/${load_arch}"
  docker_build_metadata || fail "could not recompute linux/${load_arch} image metadata"
  [ "${BUILD_IDENTITY}" = "${load_expected_identity}" ] ||
    fail "linux/${load_arch} metadata changed after OCI build"
  assert_image_identity "${load_image}" "${load_expected_identity}" "${SOURCE_FINGERPRINT}" ||
    fail "loaded linux/${load_arch} image identity is invalid"
}

normalize_architecture() (
  case "$1" in
    amd64 | x86_64) printf 'amd64\n' ;;
    arm64 | aarch64) printf 'arm64\n' ;;
    *) printf '%s\n' "$1" ;;
  esac
)

platform_runtime_supported() {
  runnable_arch=$1
  runnable_container="${RESOURCE_PREFIX}-probe-${runnable_arch}"
  runnable_log="${TEMP_DIR}/probe-${runnable_arch}.log"
  if ! retry 3 docker pull --platform "linux/${runnable_arch}" "${NGINX_BASE}" >/dev/null; then
    log "linux/${runnable_arch} base cannot be pulled; runtime checks are unavailable on this runner"
    return 1
  fi
  register_container "${runnable_container}"
  if ! docker create \
    --name "${runnable_container}" \
    --platform "linux/${runnable_arch}" \
    --entrypoint /bin/true \
    "${NGINX_BASE}" >"${runnable_log}" 2>&1; then
    log "linux/${runnable_arch} cannot be created on this runner; runtime checks are skipped for that architecture"
    return 1
  fi
  if docker start --attach "${runnable_container}" >"${runnable_log}" 2>&1; then
    return 0
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

run_workspace_suite() {
  suite_image=$1
  suite_arch=$2
  log "running workspace suite for native linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/workspace.sh"
}

run_upgrade_suite() {
  suite_image=$1
  suite_arch=$2
  log "running v0.6 long-chain and v0.7 direct upgrade/recovery suites for native linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/upgrade_compatibility.sh"
}

run_repeated_recovery_suite() {
  suite_image=$1
  suite_arch=$2
  log "running fixed ten-round publication/recovery suite for native linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/repeated_recovery.sh"
}

run_stability_suite() {
  suite_image=$1
  suite_arch=$2
  log "running fixed 600-second stability suite for native linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/stability.sh"
}

run_security_suite() {
  suite_image=$1
  suite_arch=$2
  log "running minimum-capability security suite for native linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/security.sh"
}

run_acme_suite() {
  suite_image=$1
  suite_arch=$2
  log "running isolated real-ACME lifecycle suite for native linux/${suite_arch}"
  IMAGE="${suite_image}" PLATFORM="linux/${suite_arch}" BUILD_IMAGE=0 \
    "${SCRIPT_DIR}/acme.sh"
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
  verify_playwright_summary "${playwright_log}" ||
    fail 'Playwright output does not prove exactly 82 passed and 1 Docker workspace skip'
  log 'Playwright acceptance passed: 82/82 tests; 1 Docker workspace test conditionally skipped'
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

for required_command in docker git go jq curl tar awk sed grep od tr wc chmod date mktemp cp mv; do
  require_command "${required_command}"
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  fail 'sha256sum or shasum is required'
fi
require_executable "${SCRIPT_DIR}/smoke.sh"
require_executable "${SCRIPT_DIR}/faults.sh"
require_executable "${SCRIPT_DIR}/workspace.sh"
require_executable "${SCRIPT_DIR}/upgrade.sh"
require_executable "${SCRIPT_DIR}/upgrade_compatibility.sh"
require_executable "${SCRIPT_DIR}/repeated_recovery.sh"
require_executable "${SCRIPT_DIR}/stability.sh"
require_executable "${SCRIPT_DIR}/security.sh"
require_executable "${SCRIPT_DIR}/acme.sh"
[ "${VERSION}" = '1.0.0' ] || fail "unexpected release version: ${VERSION}"
[ -n "${NATIVE_IMAGE}" ] || fail 'NATIVE_IMAGE is required and is never rebuilt by multiarch.sh'
case "${SBOM_GENERATOR}" in
  *@sha256:*) SBOM_GENERATOR_DIGEST=${SBOM_GENERATOR##*@sha256:} ;;
  *) fail 'SBOM generator must use an immutable sha256 digest' ;;
esac
is_lower_hex "${SBOM_GENERATOR_DIGEST}" 64 || fail 'SBOM generator digest is malformed'
docker info >/dev/null
docker buildx version >/dev/null

HOST_ARCH=$(normalize_architecture "$(docker info --format '{{.Architecture}}')")
case "${HOST_ARCH}" in
  amd64 | arm64) ;;
  *) fail "unsupported Docker host architecture: ${HOST_ARCH}" ;;
esac

PLATFORM="linux/${HOST_ARCH}"
docker_build_metadata || fail 'could not compute native image metadata'
INITIAL_SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}
EXPECTED_SOURCE_COMMIT=${SOURCE_COMMIT}
EXPECTED_SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
EXPECTED_BUILD_TIME=${BUILD_TIME}
NATIVE_BUILD_IDENTITY=${BUILD_IDENTITY}
assert_image_identity "${NATIVE_IMAGE}" "${NATIVE_BUILD_IDENTITY}" "${INITIAL_SOURCE_FINGERPRINT}" ||
  fail 'NATIVE_IMAGE does not match the native source/build identity'
NATIVE_IMAGE_DIGEST=${IMAGE_DIGEST}

PLATFORM=linux/amd64
docker_build_metadata || fail 'could not compute linux/amd64 build metadata'
[ "${SOURCE_FINGERPRINT}" = "${INITIAL_SOURCE_FINGERPRINT}" ] &&
  [ "${SOURCE_COMMIT}" = "${EXPECTED_SOURCE_COMMIT}" ] &&
  [ "${SOURCE_DATE_EPOCH}" = "${EXPECTED_SOURCE_DATE_EPOCH}" ] &&
  [ "${BUILD_TIME}" = "${EXPECTED_BUILD_TIME}" ] || fail 'linux/amd64 metadata changed shared source identity'
AMD64_BUILD_IDENTITY=${BUILD_IDENTITY}

PLATFORM=linux/arm64
docker_build_metadata || fail 'could not compute linux/arm64 build metadata'
[ "${SOURCE_FINGERPRINT}" = "${INITIAL_SOURCE_FINGERPRINT}" ] &&
  [ "${SOURCE_COMMIT}" = "${EXPECTED_SOURCE_COMMIT}" ] &&
  [ "${SOURCE_DATE_EPOCH}" = "${EXPECTED_SOURCE_DATE_EPOCH}" ] &&
  [ "${BUILD_TIME}" = "${EXPECTED_BUILD_TIME}" ] || fail 'linux/arm64 metadata changed shared source identity'
ARM64_BUILD_IDENTITY=${BUILD_IDENTITY}
[ "${AMD64_BUILD_IDENTITY}" != "${ARM64_BUILD_IDENTITY}" ] ||
  fail 'platform-specific build identities must differ'

TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-multiarch.${RUN_ID}.XXXXXX")
chmod 0700 "${TEMP_DIR}"
AMD64_OCI_ARCHIVE="${TEMP_DIR}/nginx-uix-${VERSION}-amd64.oci.tar"
ARM64_OCI_ARCHIVE="${TEMP_DIR}/nginx-uix-${VERSION}-arm64.oci.tar"
AMD64_OCI_ROOT="${TEMP_DIR}/oci-amd64"
ARM64_OCI_ROOT="${TEMP_DIR}/oci-arm64"
OCI_ROOT="${TEMP_DIR}/oci"

register_builder "${BUILDER_NAME}"
create_buildx_builder
retry 3 docker buildx inspect "${BUILDER_NAME}" --bootstrap >/dev/null

log "building separate OCI layouts from commit ${SOURCE_COMMIT}"
build_platform_oci amd64 "${AMD64_IMAGE}" "${AMD64_BUILD_IDENTITY}" "${AMD64_OCI_ARCHIVE}"
AMD64_CACHE_KIND=$(cat "${TEMP_DIR}/cache-amd64.kind")
extract_platform_layout "${AMD64_OCI_ARCHIVE}" "${AMD64_OCI_ROOT}"
AMD64_MANIFEST_DIGEST=$(verify_platform_layout \
  "${AMD64_OCI_ROOT}" amd64 "${AMD64_BUILD_IDENTITY}")
AMD64_SBOM_DIGEST=$(cat "${TEMP_DIR}/sbom-amd64.digest")
AMD64_SBOM_PACKAGES=$(cat "${TEMP_DIR}/sbom-amd64.packages")

build_platform_oci arm64 "${ARM64_IMAGE}" "${ARM64_BUILD_IDENTITY}" "${ARM64_OCI_ARCHIVE}"
ARM64_CACHE_KIND=$(cat "${TEMP_DIR}/cache-arm64.kind")
extract_platform_layout "${ARM64_OCI_ARCHIVE}" "${ARM64_OCI_ROOT}"
ARM64_MANIFEST_DIGEST=$(verify_platform_layout \
  "${ARM64_OCI_ROOT}" arm64 "${ARM64_BUILD_IDENTITY}")
ARM64_SBOM_DIGEST=$(cat "${TEMP_DIR}/sbom-arm64.digest")
ARM64_SBOM_PACKAGES=$(cat "${TEMP_DIR}/sbom-arm64.packages")
[ "${AMD64_MANIFEST_DIGEST}" != "${ARM64_MANIFEST_DIGEST}" ] || fail 'architecture manifests have the same digest'
[ "${AMD64_SBOM_DIGEST}" != "${ARM64_SBOM_DIGEST}" ] || fail 'architecture SBOM attestations have the same digest'
merge_platform_layouts
log "OCI linux/amd64 manifest: ${AMD64_MANIFEST_DIGEST}"
log "OCI linux/arm64 manifest: ${ARM64_MANIFEST_DIGEST}"
log "SPDX linux/amd64 attestation: ${AMD64_SBOM_DIGEST} (${AMD64_SBOM_PACKAGES} packages)"
log "SPDX linux/arm64 attestation: ${ARM64_SBOM_DIGEST} (${ARM64_SBOM_PACKAGES} packages)"

AMD64_RUNNABLE=0
ARM64_RUNNABLE=0
AMD64_RUNTIME_RESULT='not_run_non_native'
ARM64_RUNTIME_RESULT='not_run_non_native'
AMD64_WORKSPACE_RESULT='not_run_non_native'
ARM64_WORKSPACE_RESULT='not_run_non_native'
AMD64_LIMITATION='native_linux_amd64_runner_required'
ARM64_LIMITATION='native_linux_arm64_runner_required'

if [ "${HOST_ARCH}" = amd64 ]; then
  AMD64_IMAGE=${NATIVE_IMAGE}
  AMD64_RUNNABLE=1
  run_minimal_boot amd64 "${NATIVE_IMAGE}"
  AMD64_RUNTIME_RESULT='passed'
  AMD64_LIMITATION='none'
  log 'linux/arm64 OCI config, labels, rootfs descriptors, and healthcheck passed static verification; native linux/arm64 runtime and workspace acceptance remain required'
else
  ARM64_IMAGE=${NATIVE_IMAGE}
  ARM64_RUNNABLE=1
  run_minimal_boot arm64 "${NATIVE_IMAGE}"
  ARM64_RUNTIME_RESULT='passed'
  ARM64_LIMITATION='none'
  log 'linux/amd64 OCI config, labels, rootfs descriptors, and healthcheck passed static verification; native linux/amd64 runtime and workspace acceptance remain required'
fi

if [ "${HOST_ARCH}" = amd64 ]; then
  run_smoke_suite "${NATIVE_IMAGE}" amd64 full
  run_fault_suite "${NATIVE_IMAGE}" amd64
  run_workspace_suite "${NATIVE_IMAGE}" amd64
  run_upgrade_suite "${NATIVE_IMAGE}" amd64
  run_repeated_recovery_suite "${NATIVE_IMAGE}" amd64
  run_stability_suite "${NATIVE_IMAGE}" amd64
  run_security_suite "${NATIVE_IMAGE}" amd64
  run_acme_suite "${NATIVE_IMAGE}" amd64
  AMD64_WORKSPACE_RESULT='passed'
else
  run_smoke_suite "${NATIVE_IMAGE}" arm64 full
  run_fault_suite "${NATIVE_IMAGE}" arm64
  run_workspace_suite "${NATIVE_IMAGE}" arm64
  run_upgrade_suite "${NATIVE_IMAGE}" arm64
  run_repeated_recovery_suite "${NATIVE_IMAGE}" arm64
  run_stability_suite "${NATIVE_IMAGE}" arm64
  run_security_suite "${NATIVE_IMAGE}" arm64
  run_acme_suite "${NATIVE_IMAGE}" arm64
  ARM64_WORKSPACE_RESULT='passed'
fi

run_playwright_acceptance
assert_browser_isolation "${NATIVE_IMAGE}" "${HOST_ARCH}"

PLATFORM="linux/${HOST_ARCH}"
docker_build_metadata || fail 'could not recompute final source identity'
FINAL_SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}
[ "${INITIAL_SOURCE_FINGERPRINT}" = "${FINAL_SOURCE_FINGERPRINT}" ] ||
  fail 'release inputs changed during the architecture builds'
[ "${BUILD_IDENTITY}" = "${NATIVE_BUILD_IDENTITY}" ] ||
  fail 'native build identity changed during the architecture builds'
assert_image_identity "${NATIVE_IMAGE}" "${NATIVE_BUILD_IDENTITY}" "${FINAL_SOURCE_FINGERPRINT}" ||
  fail 'NATIVE_IMAGE identity changed during multiarch acceptance'
[ "${IMAGE_DIGEST}" = "${NATIVE_IMAGE_DIGEST}" ] ||
  fail 'NATIVE_IMAGE digest changed during multiarch acceptance'

printf '\nTask 18 multi-architecture/browser acceptance: PASS\n'
printf 'run_id=%s\n' "${RUN_ID}"
printf 'source_commit=%s\n' "${SOURCE_COMMIT}"
printf 'source_fingerprint=%s\n' "${FINAL_SOURCE_FINGERPRINT}"
printf 'native_image_digest=%s native_platform=linux/%s\n' "${NATIVE_IMAGE_DIGEST}" "${HOST_ARCH}"
printf 'oci_layout_index_digest=%s\n' "${OCI_LAYOUT_INDEX_DIGEST}"
printf 'sbom_generator=%s\n' "${SBOM_GENERATOR}"
printf 'linux_amd64_manifest=%s build_identity=%s runnable=%s runtime=%s workspace_runtime=%s static_verification=passed limitation=%s cache=%s\n' \
  "${AMD64_MANIFEST_DIGEST}" "${AMD64_BUILD_IDENTITY}" \
  "${AMD64_RUNNABLE}" "${AMD64_RUNTIME_RESULT}" "${AMD64_WORKSPACE_RESULT}" \
  "${AMD64_LIMITATION}" "${AMD64_CACHE_KIND}"
printf 'linux_arm64_manifest=%s build_identity=%s runnable=%s runtime=%s workspace_runtime=%s static_verification=passed limitation=%s cache=%s\n' \
  "${ARM64_MANIFEST_DIGEST}" "${ARM64_BUILD_IDENTITY}" \
  "${ARM64_RUNNABLE}" "${ARM64_RUNTIME_RESULT}" "${ARM64_WORKSPACE_RESULT}" \
  "${ARM64_LIMITATION}" "${ARM64_CACHE_KIND}"
printf 'linux_amd64_sbom=%s packages=%s predicate=https://spdx.dev/Document\n' \
  "${AMD64_SBOM_DIGEST}" "${AMD64_SBOM_PACKAGES}"
printf 'linux_arm64_sbom=%s packages=%s predicate=https://spdx.dev/Document\n' \
  "${ARM64_SBOM_DIGEST}" "${ARM64_SBOM_PACKAGES}"
printf 'playwright=82/82 conditional_skip=1_docker_workspace\n'
printf 'native_upgrade=passed native_security=passed native_acme=passed\n'
printf 'native_repeated_recovery=passed native_stability=passed\n'
printf 'browser_isolation=release-added-layers+final-filesystem\n'
