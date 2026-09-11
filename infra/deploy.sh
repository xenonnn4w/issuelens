#!/usr/bin/env bash
set -euo pipefail
REMOTE="${1:?Usage: bash infra/deploy.sh <ssh-alias-or-user@host>}"
if [[ ! "$REMOTE" =~ ^[a-zA-Z0-9_.@-]+$ || "$REMOTE" == -* ]]; then
  echo "Invalid SSH destination" >&2
  exit 1
fi
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REMOTE_PATH=/opt/issue-lens
npm ci --prefix "$ROOT/web"
npm run build --prefix "$ROOT/web"
# Prepare this isolated directory and infra/.env on the host before deployment.
ssh "$REMOTE" "test -d $REMOTE_PATH && test -f $REMOTE_PATH/infra/.env && command -v docker >/dev/null"
rsync -az \
  --exclude '.git' --exclude '.env' --exclude '.venv' \
  --exclude 'data/' --exclude '__pycache__/' --exclude '.pytest_cache/' \
  --exclude 'node_modules/' --exclude '/web/out/static/lucide.js' \
  "$ROOT/" "$REMOTE:$REMOTE_PATH/"
ssh "$REMOTE" "cd $REMOTE_PATH/infra && docker compose config -q && docker compose up -d --build --wait && docker compose up -d --force-recreate --wait caddy && docker compose ps"
