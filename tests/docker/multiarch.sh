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

VERSION=$(tr -d '\r\n' < VERSION)
OUTPUT_DIR=${MULTIARCH_OUTPUT_DIR:-"${REPOSITORY_ROOT}/.tmp/multiarch"}
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/nginx-uix-multiarch.XXXXXX")

cleanup() {
  cleanup_status=$?
  trap - EXIT HUP INT TERM
  rm -rf "${TEMP_DIR}"
  exit "${cleanup_status}"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
  printf '[multiarch] ERROR: %s\n' "$*" >&2
  exit 1
}

digest_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

build_binary_pair() {
  binary_arch=$1
  binary_output="${TEMP_DIR}/binary-${binary_arch}"

  set -- docker buildx build \
    --progress=plain \
    --platform "linux/${binary_arch}" \
    --provenance=false \
    --file deploy/docker/Dockerfile \
    --target binary-export \
    --output "type=local,dest=${binary_output}" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "COMMIT=${SOURCE_COMMIT}" \
    --build-arg "BUILD_TIME=${BUILD_TIME}" \
    --build-arg "SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}" \
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
  set -- "$@" .
  "$@"

  [ -s "${binary_output}/nginx-uix" ] ||
    fail "linux/${binary_arch} nginx-uix binary is empty"
  [ -s "${binary_output}/nginx-uix-agent" ] ||
    fail "linux/${binary_arch} nginx-uix-agent binary is empty"
  mv "${binary_output}/nginx-uix" "${TEMP_DIR}/nginx-uix-linux-${binary_arch}"
  mv "${binary_output}/nginx-uix-agent" \
    "${TEMP_DIR}/nginx-uix-agent-linux-${binary_arch}"
}

build_image_archive() {
  image_arch=$1
  image_identity=$2
  image_archive="${TEMP_DIR}/nginx-uix-${VERSION}-linux-${image_arch}.oci.tar"

  set -- docker buildx build \
    --progress=plain \
    --platform "linux/${image_arch}" \
    --provenance=false \
    --file deploy/docker/Dockerfile \
    --tag "nginx-uix:${VERSION}-${image_arch}" \
    --output "type=oci,dest=${image_archive}" \
    --build-arg "VERSION=${VERSION}" \
    --build-arg "COMMIT=${SOURCE_COMMIT}" \
    --build-arg "BUILD_TIME=${BUILD_TIME}" \
    --build-arg "SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}" \
    --build-arg "BUILD_IDENTITY=${image_identity}" \
    --build-arg "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"
  if [ -n "${MULTIARCH_IMAGE_REPOSITORY:-}" ]; then
    set -- "$@" \
      --output "type=registry,name=${MULTIARCH_IMAGE_REPOSITORY}:${VERSION}-${image_arch}"
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
  set -- "$@" .
  "$@"

  [ -s "${image_archive}" ] || fail "linux/${image_arch} image archive is empty"
  tar -tf "${image_archive}" | grep -Fx 'oci-layout' >/dev/null ||
    fail "linux/${image_arch} image archive is not an OCI layout"
}

for required_command in docker git go tar awk grep mktemp mv; do
  command -v "${required_command}" >/dev/null 2>&1 ||
    fail "required command is unavailable: ${required_command}"
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  fail 'sha256sum or shasum is required'
fi
[ "${VERSION}" = 1.0.0 ] || fail "unexpected release version: ${VERSION}"
docker info >/dev/null
docker buildx version >/dev/null
mkdir -p "${OUTPUT_DIR}"

PLATFORM=linux/amd64
docker_build_metadata || fail 'could not compute linux/amd64 build metadata'
AMD64_BUILD_IDENTITY=${BUILD_IDENTITY}
EXPECTED_SOURCE_FINGERPRINT=${SOURCE_FINGERPRINT}
EXPECTED_SOURCE_COMMIT=${SOURCE_COMMIT}
EXPECTED_SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}
EXPECTED_BUILD_TIME=${BUILD_TIME}

PLATFORM=linux/arm64
docker_build_metadata || fail 'could not compute linux/arm64 build metadata'
ARM64_BUILD_IDENTITY=${BUILD_IDENTITY}
[ "${SOURCE_FINGERPRINT}" = "${EXPECTED_SOURCE_FINGERPRINT}" ] ||
  fail 'source fingerprint changed between platforms'
[ "${SOURCE_COMMIT}" = "${EXPECTED_SOURCE_COMMIT}" ] ||
  fail 'source commit changed between platforms'
[ "${SOURCE_DATE_EPOCH}" = "${EXPECTED_SOURCE_DATE_EPOCH}" ] ||
  fail 'source epoch changed between platforms'
[ "${BUILD_TIME}" = "${EXPECTED_BUILD_TIME}" ] ||
  fail 'build time changed between platforms'

build_binary_pair amd64
build_binary_pair arm64
build_image_archive amd64 "${AMD64_BUILD_IDENTITY}"
build_image_archive arm64 "${ARM64_BUILD_IDENTITY}"

for artifact in \
  "nginx-uix-linux-amd64" \
  "nginx-uix-agent-linux-amd64" \
  "nginx-uix-linux-arm64" \
  "nginx-uix-agent-linux-arm64" \
  "nginx-uix-${VERSION}-linux-amd64.oci.tar" \
  "nginx-uix-${VERSION}-linux-arm64.oci.tar"; do
  mv "${TEMP_DIR}/${artifact}" "${OUTPUT_DIR}/${artifact}"
done

printf 'multi-platform build: PASS\n'
printf 'source_commit=%s source_fingerprint=%s\n' \
  "${SOURCE_COMMIT}" "${SOURCE_FINGERPRINT}"
printf 'linux_amd64_binary=%s agent=%s build_identity=%s image_archive_sha256=%s\n' \
  "$(digest_file "${OUTPUT_DIR}/nginx-uix-linux-amd64")" \
  "$(digest_file "${OUTPUT_DIR}/nginx-uix-agent-linux-amd64")" \
  "${AMD64_BUILD_IDENTITY}" \
  "$(digest_file "${OUTPUT_DIR}/nginx-uix-${VERSION}-linux-amd64.oci.tar")"
printf 'linux_arm64_binary=%s agent=%s build_identity=%s image_archive_sha256=%s\n' \
  "$(digest_file "${OUTPUT_DIR}/nginx-uix-linux-arm64")" \
  "$(digest_file "${OUTPUT_DIR}/nginx-uix-agent-linux-arm64")" \
  "${ARM64_BUILD_IDENTITY}" \
  "$(digest_file "${OUTPUT_DIR}/nginx-uix-${VERSION}-linux-arm64.oci.tar")"
