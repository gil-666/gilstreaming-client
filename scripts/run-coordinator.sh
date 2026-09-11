#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
config="${1:-$repo_root/coordinator/config.json}"
env_file="${2:-$repo_root/coordinator/.env}"

if ! command -v go >/dev/null 2>&1; then
    echo "Go 1.22 or newer is required and must be available on PATH." >&2
    exit 1
fi
if [[ ! -f "$config" ]]; then
    echo "Coordinator config not found: $config" >&2
    echo "Copy coordinator/config.example.json to coordinator/config.json first." >&2
    exit 1
fi

output_dir="$repo_root/build/coordinator"
binary="$output_dir/gilstreaming-coordinator"
mkdir -p "$output_dir"

echo "Building GilStreaming coordinator..."
(
    cd "$repo_root/coordinator"
    go build -o "$binary" .
)

echo "Starting coordinator with $config"
cd "$repo_root/coordinator"
exec "$binary" -config "$config" -env-file "$env_file"
