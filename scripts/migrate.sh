#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

ACTION="${1:-up}"
shift || true

if [[ -n "${DATABASE_URL:-}" && -z "${DB_DSN:-}" ]]; then
  export DB_DSN="$DATABASE_URL"
fi

echo "AINode migration action: ${ACTION}"
go run ./cmd/migrate "${ACTION}" "$@"
