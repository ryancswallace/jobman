ARG GO_VERSION=1.26
FROM mcr.microsoft.com/devcontainers/go:2-${GO_VERSION}-bookworm

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

# Install the tools used by the Go extension and the repository's development
# workflow in the image so a freshly created container is ready to use.
USER vscode
RUN go install golang.org/x/tools/gopls@latest \
    && go install github.com/go-delve/delve/cmd/dlv@latest \
    && go install golang.org/x/tools/cmd/goimports@latest \
    && go install github.com/cweill/gotests/gotests@v1.6.0 \
    && go install github.com/josharian/impl@v1.4.0 \
    && go install honnef.co/go/tools/cmd/staticcheck@latest
