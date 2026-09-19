FROM mcr.microsoft.com/devcontainers/javascript-node:1-20-bookworm

ARG CODEX_VERSION=0.155.1
ARG CODEBASE_MEMORY_MCP_VERSION=0.11.0
ARG RTK_VERSION=v0.49.0

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        fd-find \
        jq \
        less \
        procps \
        ripgrep \
        shellcheck \
        sqlite3 \
        tree \
        unzip \
        zstd \
    && rm -rf /var/lib/apt/lists/*

# Codex CLI and codebase-memory-mcp are installed globally so they are
# available to the node user and to non-interactive devcontainer tasks.
RUN npm install --global \
        "@openai/codex@${CODEX_VERSION}" \
        "codebase-memory-mcp@${CODEBASE_MEMORY_MCP_VERSION}" \
    && npm cache clean --force

# Install the Rust Token Killer from its verified upstream release installer.
# RTK_VERSION can be supplied at build time to pin a specific release.
RUN RTK_INSTALL_DIR=/usr/local/bin \
    RTK_VERSION="${RTK_VERSION}" \
    sh -c "$(curl -fsSL https://raw.githubusercontent.com/rtk-ai/rtk/master/install.sh)"

USER node
WORKDIR /workspace
