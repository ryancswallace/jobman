ARG GO_VERSION=1.26.5
ARG GO_FEATURE_VERSION=1.26

FROM golang:${GO_VERSION}-bookworm@sha256:18aedc16aa19b3fd7ded7245fc14b109e054d65d22ed53c355c899582bbb2113 AS toolchain
FROM mcr.microsoft.com/devcontainers/go:2-${GO_FEATURE_VERSION}-bookworm@sha256:090a56c6c9c4e80a1573c18c1225eb851b8c86c199fc929b07ea2a67f7b4850f

COPY --from=toolchain /usr/local/go /usr/local/go

ARG GOPLS_VERSION=v0.23.0
ARG DELVE_VERSION=v1.27.0
ARG X_TOOLS_VERSION=v0.48.0
ARG GOTESTS_VERSION=v1.9.0
ARG IMPL_VERSION=v1.5.0
ARG STATICCHECK_VERSION=v0.7.0

RUN apt-get update \
    && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install --yes --no-install-recommends \
        bash-completion \
        ca-certificates \
        curl \
        git \
        jq \
        less \
        make \
        man-db \
        openssh-client \
        procps \
        ripgrep \
        shellcheck \
    && rm -rf /var/lib/apt/lists/*

USER vscode
RUN go install golang.org/x/tools/gopls@${GOPLS_VERSION} \
    && go install github.com/go-delve/delve/cmd/dlv@${DELVE_VERSION} \
    && go install golang.org/x/tools/cmd/goimports@${X_TOOLS_VERSION} \
    && go install github.com/cweill/gotests/gotests@${GOTESTS_VERSION} \
    && go install github.com/josharian/impl@${IMPL_VERSION} \
    && go install honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}
