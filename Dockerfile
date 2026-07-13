# syntax=docker/dockerfile:1.7

# These defaults match go.version and the release image. Override them with
# --build-arg when testing a toolchain or base-image upgrade.
ARG GO_VERSION=1.26.5
ARG ALPINE_VERSION=3.24

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine${ALPINE_VERSION}@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build

RUN apk add --no-cache ca-certificates git

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local

WORKDIR /src

# Cache dependencies independently from source changes. The readonly module
# cache prevents a build from silently changing the committed dependency graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download \
    && go mod verify

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build \
      -trimpath \
      -ldflags="-s -w -buildid=" \
      -o /out/jobman \
      .

FROM alpine:${ALPINE_VERSION}@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS runtime

# Bash is required by jobman's command runner. Tini forwards signals and reaps
# orphaned child processes when jobman is PID 1. CA roots and timezone data make
# networked and scheduled jobs useful without inflating the image excessively.
RUN apk add --no-cache bash ca-certificates tini tzdata \
    && addgroup -S -g 10001 jobman \
    && adduser -S -D -u 10001 -G jobman -h /home/jobman jobman \
    && mkdir -p /home/jobman/.config/jobman /work \
    && chown -R jobman:jobman /home/jobman /work

ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="jobman" \
      org.opencontainers.image.description="A daemon-less command line job manager" \
      org.opencontainers.image.url="https://github.com/ryancswallace/jobman" \
      org.opencontainers.image.source="https://github.com/ryancswallace/jobman" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.created="$BUILD_DATE" \
      org.opencontainers.image.revision="$VCS_REF" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build --chown=root:root /out/jobman /usr/local/bin/jobman

ENV HOME=/home/jobman \
    XDG_CONFIG_HOME=/home/jobman/.config

USER 10001:10001
WORKDIR /work

STOPSIGNAL SIGTERM
ENTRYPOINT ["/sbin/tini", "--", "jobman"]
CMD ["--help"]
