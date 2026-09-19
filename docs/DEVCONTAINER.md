# Dev Container

The repository includes a development container for the hybrid Python, Go, and
React/Vite stack.

## Open the project

1. Install Docker and the VS Code Dev Containers extension.
2. Open the repository in VS Code.
3. Run **Dev Containers: Reopen in Container**.

The container installs Python 3.11, Go 1.25, Node.js 20, Docker CLI/Compose,
Codex CLI, RTK, and `codebase-memory-mcp`.

The Docker socket is mounted from the host because the orchestrator provisions
and monitors Docker containers. This gives code running in the dev container
control over the host Docker daemon; only use it with repositories and code you
trust.

## Common commands

```bash
cd app/orchestrator && go test ./...
cd app/static/panel-react && npm run dev -- --host 0.0.0.0
docker compose up -d
```

The Compose file starts the published runtime image and requires the host
Docker daemon. For the Go and frontend development loops, run the commands
above directly from the container.

## Codex and MCP

The `openai.chatgpt` extension is installed for VS Code, and Codex CLI is
available as `codex`. Authenticate inside the container as needed, then use
`/plugins` or `codex mcp list` from the CLI.

The project-level `.codex/config.toml` registers `codebase-memory-mcp`. Its
index is persisted in a Docker volume so rebuilding the container does not
force a full re-index every time. The graph UI is available with:

```bash
codebase-memory-mcp --ui=true --port=9749
```

## RTK

RTK is installed as the Rust Token Killer. Use `rtk gain` to inspect savings
and invoke supported commands through `rtk`, for example `rtk git status`.
The Codex integration can be enabled per user with `rtk init -g --codex` if you
want RTK instructions added to your Codex setup.
