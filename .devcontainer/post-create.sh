#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

echo "==> Fixing persistent tool volume ownership"
if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
  sudo chown -R "$(id -u):$(id -g)" "$HOME/.codex" "$HOME/.cache"
fi

if ! codex mcp list --json 2>/dev/null | jq -e '.[] | select(.name == "codebase-memory")' >/dev/null; then
  codex mcp add codebase-memory -- /usr/local/share/npm-global/bin/codebase-memory-mcp
fi

echo "==> Preparing Python environment"
if [ ! -d .venv ]; then
  python3 -m venv .venv
fi
.venv/bin/python -m pip install --upgrade pip
.venv/bin/pip install -r requirements.txt

echo "==> Downloading Go modules"
(cd app/orchestrator && go mod download)

echo "==> Installing panel dependencies"
(cd app/static/panel-react && npm install --no-audit --no-fund)

echo "==> Verifying developer tools"
codex --version
rtk --version
codebase-memory-mcp --version || true
docker compose version
go version
python3 --version
node --version

echo ""
echo "Dev container ready. Useful commands:"
echo "  cd app/orchestrator && go test ./..."
echo "  cd app/static/panel-react && npm run dev -- --host 0.0.0.0"
echo "  rtk gain"
echo "  codex mcp list"
