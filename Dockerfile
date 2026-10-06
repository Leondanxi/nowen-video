# syntax=docker/dockerfile:1.6
# Official Nowen Video image, optimized for NAS and self-hosted home media use.
# cmd/server-lite remains an internal migration-stable implementation path only;
# it is no longer a separate Lite product edition. Dockerfile.full is retained
# solely for legacy compatibility and rollback validation.

FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend
ARG NOWEN_VERSION=0.1.0
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
ENV VITE_APP_VERSION=${NOWEN_VERSION}
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
ARG NOWEN_VERSION=0.1.0
WORKDIR /app
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X github.com/nowen-video/nowen-video/internal/version.Version=${NOWEN_VERSION}" \
      -o nowen-video ./cmd/server-lite

FROM alpine:3.24
ARG TARGETARCH
ARG NOWEN_VERSION=0.1.0
ARG FFMPEG_VERSION=8.1.2-r0
# ---------------------------------------------------------------------------
# OPTIONAL ffmpeg-over-ip client bundle (DISABLED BY DEFAULT).
#   FFOIP_VERSION pins a GitHub release tag, e.g. v5.2.1. Leave EMPTY ("") to
#   skip the download entirely (this is the default and the recommended route;
#   most users should read-only bind-mount the client instead — see
#   docs/ffmpeg-over-ip-deploy.md and docker-compose.yml).
#
#   When set, the static Linux client is fetched from the project's GitHub
#   Releases:
#     https://github.com/steelbrain/ffmpeg-over-ip/releases/download/<TAG>/linux-<arch>-ffmpeg-over-ip-client.zip
#   where <arch> is $TARGETARCH (amd64 | arm64 — these match the release asset
#   names exactly). It is installed at /opt/ffoip/ffmpeg, and /opt/ffoip/ffprobe
#   is created as an argv[0] symlink to the same binary (the client switches to
#   ffprobe mode when its basename contains "ffprobe").
#
#   The system ffmpeg/ffprobe installed below is left untouched so it remains a
#   local software-encode fallback. No address/secret is baked into the image;
#   those are injected at runtime via compose environment / .env or nowen's hot
#   settings.
#
#   Build with e.g.:
#     docker build --build-arg FFOIP_VERSION=v5.2.1 -t nowen-video:ffoip .
# ---------------------------------------------------------------------------
ARG FFOIP_VERSION=""

# Keep the runtime dependency surface minimal. Alpine's BusyBox already
# provides the health-check client and standard process utilities we need.
RUN apk add --no-cache \
      "ffmpeg=${FFMPEG_VERSION}" \
      tzdata \
      ca-certificates \
      su-exec \
    && ffmpeg -version | head -n 1 | grep -F "ffmpeg version 8.1.2" \
    && ffprobe -version | head -n 1 | grep -F "ffprobe version 8.1.2"

# Hardware acceleration drivers are part of the official image because direct
# play, remux and on-demand fallback transcoding are core playback capabilities.
# libva-utils is intentionally omitted: it only provides diagnostics such as
# vainfo and is not required by FFmpeg for VA-API/QSV runtime acceleration.
RUN set -eux; \
    if [ "${TARGETARCH}" = "amd64" ]; then \
      apk add --no-cache intel-media-driver libva-intel-driver mesa-va-gallium; \
    else \
      apk add --no-cache mesa-va-gallium; \
    fi

# Optional ffmpeg-over-ip client bundle. This whole stage is a no-op unless you
# pass --build-arg FFOIP_VERSION=<tag>; it adds ~1.2 MB and does NOT remove the
# system ffmpeg installed above. Asset layout is tolerant of a flat vs. wrapper
# zip. See the ARG declaration above and docs/ffmpeg-over-ip-deploy.md.
RUN set -eux; \
    if [ -n "${FFOIP_VERSION}" ]; then \
      apk add --no-cache curl unzip; \
      mkdir -p /opt/ffoip; \
      echo "Bundling ffmpeg-over-ip client ${FFOIP_VERSION} for linux-${TARGETARCH}"; \
      curl -fsSL -o /tmp/ffoip.zip \
        "https://github.com/steelbrain/ffmpeg-over-ip/releases/download/${FFOIP_VERSION}/linux-${TARGETARCH}-ffmpeg-over-ip-client.zip"; \
      tmp="$(mktemp -d)"; \
      unzip -o -q /tmp/ffoip.zip -d "${tmp}"; \
      bin="$(find "${tmp}" -type f \( -name 'ffmpeg-over-ip-client*' -o -name 'ffmpeg-over-ip' \) | head -n1)"; \
      [ -n "${bin}" ] || { echo "ffmpeg-over-ip client binary not found in release zip" >&2; exit 1; }; \
      install -m 0755 "${bin}" /opt/ffoip/ffmpeg; \
      ln -sf ffmpeg /opt/ffoip/ffprobe; \
      chmod -R a+rX /opt/ffoip; \
      rm -rf /tmp/ffoip.zip "${tmp}"; \
      ls -l /opt/ffoip; \
    else \
      echo "FFOIP_VERSION not set -> skipping ffmpeg-over-ip client bundle (system ffmpeg remains the only ffmpeg)."; \
    fi

RUN addgroup -S nowen && adduser -S nowen -G nowen
WORKDIR /app
COPY --from=backend /app/nowen-video /usr/local/bin/nowen-video
COPY --from=frontend /app/web/dist /app/web/dist
COPY scripts/docker-entrypoint.sh /entrypoint.sh

RUN mkdir -p /data /cache /media \
    && chown -R nowen:nowen /data /cache /media \
    && chmod +x /entrypoint.sh

ENV NOWEN_APP_PORT=8080
ENV NOWEN_APP_DATA_DIR=/data
ENV NOWEN_APP_WEB_DIR=/app/web/dist
ENV NOWEN_DATABASE_DB_PATH=/data/nowen.db
ENV NOWEN_CACHE_CACHE_DIR=/cache
ENV NOWEN_LOGGING_LEVEL=info
ENV NOWEN_VERSION=${NOWEN_VERSION}
ENV TZ=Asia/Shanghai

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD /bin/busybox wget -q -O /dev/null http://localhost:8080/api/health || exit 1

CMD ["/entrypoint.sh"]
