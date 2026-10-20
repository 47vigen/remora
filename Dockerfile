# syntax=docker/dockerfile:1
#
# remora image: remora (pure Go, cross-compiled) plus the two engines, each built
# with cgo from its git submodule on the target platform. Runtime base is glibc.
#
# Build: make image   (passes BIFROST_VERSION and CLIPROXYAPI_VERSION from engines/versions.env)

# ---------------------------------------------------------------------------
# web: the console, built by Vite into web/dist with a .br and a .gz beside each asset; nginx serves it.
FROM --platform=$BUILDPLATFORM oven/bun:1.4.3 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# ---------------------------------------------------------------------------
# remora: cross-compiled on the build platform.
FROM --platform=$BUILDPLATFORM golang:1.27-trixie AS remora
ARG TARGETOS
ARG TARGETARCH
ARG REMORA_VERSION=dev
ARG REMORA_COMMIT=none
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/47vigen/remora/internal/version.Version=${REMORA_VERSION} -X github.com/47vigen/remora/internal/version.Commit=${REMORA_COMMIT}" \
      -o /out/remora ./cmd/remora

# ---------------------------------------------------------------------------
# bifrost (front door): cgo, built on the target platform.
FROM golang:1.27-trixie AS bifrost
ARG BIFROST_VERSION
RUN test -n "$BIFROST_VERSION"
WORKDIR /src/transports
COPY engines/bifrost/transports/go.mod engines/bifrost/transports/go.sum ./
RUN GOWORK=off go mod download
COPY engines/bifrost/transports/ ./
# The dashboard UI is not shipped. main.go embeds bifrost-http/ui with `all:`,
# which needs at least one file; a lone dotfile is never served.
RUN mkdir -p bifrost-http/ui && touch bifrost-http/ui/.keep
# Build against the submodule's own core module instead of the published one, so a patch carried on the engine's
# fork (docs/patches.md) is in the binary. Without a patch this is the same source: the pinned tag's core is the
# core release that transports requires.
COPY engines/bifrost/core/ /src/core/
RUN go mod edit -replace github.com/maximhq/bifrost/core=../core
RUN GOWORK=off CGO_ENABLED=1 go build -trimpath -tags sqlite_static \
      -ldflags "-s -w -X main.Version=v${BIFROST_VERSION#transports/v}" \
      -o /out/bifrost ./bifrost-http

# ---------------------------------------------------------------------------
# cliproxyapi (OAuth engine): cgo, built on the target platform.
FROM golang:1.27-trixie AS cliproxyapi
ARG CLIPROXYAPI_VERSION
RUN test -n "$CLIPROXYAPI_VERSION"
WORKDIR /src
COPY engines/cliproxyapi/go.mod engines/cliproxyapi/go.sum ./
RUN go mod download
COPY engines/cliproxyapi/ ./
RUN CGO_ENABLED=1 go build -buildvcs=false -trimpath \
      -ldflags "-s -w -X main.Version=${CLIPROXYAPI_VERSION} -X main.Commit=${CLIPROXYAPI_VERSION}" \
      -o /out/cliproxyapi ./cmd/server

# ---------------------------------------------------------------------------
# final: glibc runtime with tini as PID 1; nginx in front on 8080, remora behind it on loopback.
FROM debian:13-slim
ARG BIFROST_VERSION
ARG CLIPROXYAPI_VERSION
RUN test -n "$BIFROST_VERSION" && test -n "$CLIPROXYAPI_VERSION"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tini nginx libnginx-mod-http-brotli-static \
 && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled /var/www \
 && useradd --system --uid 10001 --home-dir /data --no-create-home --shell /usr/sbin/nologin remora
COPY --from=remora /out/remora /usr/local/bin/remora
COPY --from=bifrost /out/bifrost /usr/local/lib/remora/engines/bifrost
COPY --from=cliproxyapi /out/cliproxyapi /usr/local/lib/remora/engines/cliproxyapi
COPY --from=web /src/web/dist /usr/share/remora/web
COPY deploy/image/nginx.conf deploy/image/proxy.conf /etc/remora/
COPY deploy/image/start /usr/local/lib/remora/start
RUN printf '{"bifrost":{"version":"%s","sha256":"%s"},"cliproxyapi":{"version":"%s","sha256":"%s"}}\n' \
      "$BIFROST_VERSION" "$(sha256sum /usr/local/lib/remora/engines/bifrost | cut -d' ' -f1)" \
      "$CLIPROXYAPI_VERSION" "$(sha256sum /usr/local/lib/remora/engines/cliproxyapi | cut -d' ' -f1)" \
      > /usr/local/lib/remora/engines/versions.json \
 && mkdir /data \
 && chown 10001 /data
VOLUME /data
EXPOSE 8080
ENV REMORA_DATA=/data
# nginx owns the public port; remora listens behind it.
ENV REMORA_LISTEN=127.0.0.1:8081
USER 10001
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/lib/remora/start"]
CMD ["serve"]
HEALTHCHECK --interval=10s --timeout=5s --start-period=120s CMD ["/usr/local/bin/remora", "healthcheck", "http://127.0.0.1:8080/healthz"]
