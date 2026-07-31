# @author hanchao <hanchao@66yunlian.com>
# @since 0.1.0

ARG VERSION=1.0.0
ARG COMMIT
ARG BUILD_TIME
ARG SOURCE_FINGERPRINT
ARG BUILD_IDENTITY
ARG SOURCE_DATE_EPOCH
ARG GOPROXY=https://proxy.golang.org,direct

FROM --platform=$BUILDPLATFORM node:24.17.0-trixie-slim@sha256:9022946fb57e6fb6a2503931470deba4b49db02bfc8f587f93007fd33ef54415 AS frontend

WORKDIR /workspace/web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,id=nginx-uix-npm,target=/root/.npm,sharing=locked \
    test "$(npm --version)" = "11.13.0" && npm ci

COPY web/index.html \
    web/eslint.config.js \
    web/tsconfig.json \
    web/tsconfig.app.json \
    web/tsconfig.node.json \
    web/vite.config.ts \
    ./
COPY web/src ./src
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.5-trixie@sha256:4ee9ffa999b4583ce281939cdff828763083610292f252279a0cee77473bd9a7 AS go-builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG COMMIT
ARG BUILD_TIME
ARG GOPROXY

WORKDIR /workspace

RUN test "$(go env GOVERSION)" = "go1.26.5"

COPY go.mod go.sum VERSION ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    test -n "${VERSION}" \
    && test -n "${COMMIT}" \
    && test -n "${BUILD_TIME}" \
    && test "$(tr -d '\n' < VERSION)" = "${VERSION}" \
    && GOPROXY="${GOPROXY}" go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY --from=frontend /workspace/web/dist ./internal/httpapi/uiassets/dist

RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    set -eu; \
    ldflags="-s -w \
      -X github.com/kuroky/nginx-uix/internal/app.Version=${VERSION} \
      -X github.com/kuroky/nginx-uix/internal/app.Commit=${COMMIT} \
      -X github.com/kuroky/nginx-uix/internal/app.BuildTime=${BUILD_TIME}"; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
      go build -buildvcs=false -trimpath -ldflags "${ldflags}" -o /out/nginx-uix ./cmd/nginx-uix; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
      go build -buildvcs=false -trimpath -ldflags "${ldflags}" -o /out/nginx-uix-agent ./cmd/nginx-uix-agent

FROM scratch AS binary-export

COPY --from=go-builder /out/nginx-uix /nginx-uix
COPY --from=go-builder /out/nginx-uix-agent /nginx-uix-agent

FROM --platform=$BUILDPLATFORM nginx:1.30.3-alpine-slim@sha256:d5b51cfc7d55fc7a7bcf4d1d577b9c3738331df56d68f0b1d8ac9795b9470a5a AS s6-overlay

ARG TARGETARCH

RUN apk add --no-cache xz=5.8.3-r0

ADD --checksum=sha256:b720f9d9340efc8bb07528b9743813c836e4b02f8693d90241f047998b4c53cf \
    https://github.com/just-containers/s6-overlay/releases/download/v3.2.3.0/s6-overlay-noarch.tar.xz \
    /tmp/s6-overlay-noarch.tar.xz
ADD --checksum=sha256:a93f02882c6ed46b21e7adb5c0add86154f01236c93cd82c7d682722e8840563 \
    https://github.com/just-containers/s6-overlay/releases/download/v3.2.3.0/s6-overlay-x86_64.tar.xz \
    /tmp/s6-overlay-x86_64.tar.xz
ADD --checksum=sha256:0952056ff913482163cc30e35b2e944b507ba1025d78f5becbb89367bf344581 \
    https://github.com/just-containers/s6-overlay/releases/download/v3.2.3.0/s6-overlay-aarch64.tar.xz \
    /tmp/s6-overlay-aarch64.tar.xz

RUN set -eu; \
    printf '%s  %s\n' \
      'b720f9d9340efc8bb07528b9743813c836e4b02f8693d90241f047998b4c53cf' '/tmp/s6-overlay-noarch.tar.xz' \
      'a93f02882c6ed46b21e7adb5c0add86154f01236c93cd82c7d682722e8840563' '/tmp/s6-overlay-x86_64.tar.xz' \
      '0952056ff913482163cc30e35b2e944b507ba1025d78f5becbb89367bf344581' '/tmp/s6-overlay-aarch64.tar.xz' \
      | sha256sum -c; \
    case "${TARGETARCH}" in \
      amd64) s6_arch='x86_64' ;; \
      arm64) s6_arch='aarch64' ;; \
      *) echo "unsupported target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /s6-rootfs; \
    tar -C /s6-rootfs -Jxpf /tmp/s6-overlay-noarch.tar.xz; \
    tar -C /s6-rootfs -Jxpf "/tmp/s6-overlay-${s6_arch}.tar.xz"

FROM nginx:1.30.3-alpine-slim@sha256:d5b51cfc7d55fc7a7bcf4d1d577b9c3738331df56d68f0b1d8ac9795b9470a5a AS runtime

ARG VERSION
ARG COMMIT
ARG SOURCE_FINGERPRINT
ARG BUILD_IDENTITY
ARG SOURCE_DATE_EPOCH

RUN test -n "${VERSION}" \
    && test -n "${COMMIT}" \
    && test -n "${SOURCE_FINGERPRINT}" \
    && test -n "${BUILD_IDENTITY}" \
    && test -n "${SOURCE_DATE_EPOCH}"

LABEL org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="https://github.com/Kurok1/nginx-uix" \
      org.opencontainers.image.licenses="Apache-2.0" \
      io.nginx-uix.source-fingerprint="${SOURCE_FINGERPRINT}" \
      io.nginx-uix.build-identity="${BUILD_IDENTITY}" \
      io.nginx-uix.reproducible-epoch="${SOURCE_DATE_EPOCH}"

RUN set -eu; \
    addgroup -S -g 10001 nginx-uix; \
    addgroup -S -g 10002 nginx-uix-agent; \
    adduser -S -D -H -u 10001 -G nginx-uix \
      -h /var/lib/nginx-uix -s /sbin/nologin nginx-uix; \
    addgroup nginx-uix nginx-uix-agent; \
    rm -rf /etc/nginx; \
    mkdir -p /etc/nginx /run/nginx-uix /usr/share/nginx-uix/default-nginx /var/lib/nginx-uix; \
    chown nginx-uix:nginx-uix /var/lib/nginx-uix; \
    chmod 0700 /var/lib/nginx-uix; \
    chmod 0755 /etc/nginx /run/nginx-uix /usr/share/nginx-uix/default-nginx

COPY --from=s6-overlay /s6-rootfs /
COPY --from=go-builder --chown=0:0 --chmod=0755 /out/nginx-uix /out/nginx-uix-agent /usr/local/bin/
COPY --chown=0:0 --chmod=0644 LICENSE /usr/share/licenses/nginx-uix/LICENSE
COPY --chown=0:0 deploy/docker/defaults/ /usr/share/nginx-uix/default-nginx/
COPY --chown=0:0 deploy/docker/rootfs/ /

RUN set -eu; \
    chown -R root:root /etc/s6-overlay /usr/share/nginx-uix/default-nginx; \
    find /usr/share/nginx-uix/default-nginx -type d -exec chmod 0755 '{}' +; \
    find /usr/share/nginx-uix/default-nginx -type f -exec chmod 0644 '{}' +; \
    chmod 0755 \
      /etc/s6-overlay/s6-rc.d/nginx-uix/run \
      /etc/s6-overlay/s6-rc.d/nginx-uix/finish \
      /etc/s6-overlay/s6-rc.d/nginx-uix-agent/run \
      /etc/s6-overlay/s6-rc.d/nginx/run \
      /etc/s6-overlay/s6-rc.d/nginx/finish; \
    chmod 0644 \
      /etc/s6-overlay/s6-rc.d/nginx-uix-init/type \
      /etc/s6-overlay/s6-rc.d/nginx-uix-init/up \
      /etc/s6-overlay/s6-rc.d/nginx-uix/type \
      /etc/s6-overlay/s6-rc.d/nginx-uix/dependencies.d/nginx-uix-init \
      /etc/s6-overlay/s6-rc.d/nginx-uix-agent/type \
      /etc/s6-overlay/s6-rc.d/nginx-uix-agent/dependencies.d/nginx-uix-init \
      /etc/s6-overlay/s6-rc.d/nginx/type \
      /etc/s6-overlay/s6-rc.d/nginx/dependencies.d/nginx-uix-init \
      /etc/s6-overlay/s6-rc.d/user/contents.d/nginx-uix-init \
      /etc/s6-overlay/s6-rc.d/user/contents.d/nginx-uix \
      /etc/s6-overlay/s6-rc.d/user/contents.d/nginx-uix-agent \
      /etc/s6-overlay/s6-rc.d/user/contents.d/nginx

ENV NGINX_UIX_LISTEN_ADDR=0.0.0.0:9000 \
    S6_BEHAVIOUR_IF_STAGE2_FAILS=2 \
    S6_SERVICES_GRACETIME=10000 \
    S6_KILL_GRACETIME=10000

EXPOSE 80 443 9000
VOLUME ["/etc/nginx", "/var/lib/nginx-uix"]
STOPSIGNAL SIGTERM

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
    CMD ["/usr/local/bin/nginx-uix", "healthcheck"]

ENTRYPOINT ["/init"]
CMD []
