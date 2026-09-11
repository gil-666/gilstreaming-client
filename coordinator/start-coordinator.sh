#!/usr/bin/env bash
set -euo pipefail

coordinator_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
binary="$coordinator_root/gilstreaming-coordinator"
config="$coordinator_root/config.json"
env_file="$coordinator_root/.env"

if [[ ! -f "$config" ]]; then
    echo "config.json is missing. Copy config.example.json to config.json and edit it first." >&2
    exit 1
fi

cd "$coordinator_root"
exec "$binary" -config "$config" -env-file "$env_file"
