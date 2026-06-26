#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

ACTION="${1:-up}"
if [ "$#" -gt 0 ]; then
  shift
fi

if [ -n "${DATABASE_URL:-}" ] && [ -z "${DB_DSN:-}" ]; then
  export DB_DSN="$DATABASE_URL"
fi

echo "AINode migration action: ${ACTION}"
exec go run ./cmd/migrate "${ACTION}" "$@"
