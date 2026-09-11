#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$repo_root/build/build-linux-debug/app/gilstreaming"
coordinator_url="${1:-${GILSTREAMING_COORDINATOR_URL:-http://127.0.0.1:6766}}"

if [[ ! -x "$binary" ]]; then
    echo "Debug client not found. Run bash scripts/build-client.sh debug first." >&2
    exit 1
fi

export GILSTREAMING_COORDINATOR_URL="$coordinator_url"
echo "Starting debug client against $coordinator_url"
started_at="$(date +%s)"
"$binary" &
client_pid=$!
tail_pid=""

cleanup() {
    if [[ -n "$tail_pid" ]]; then
        kill "$tail_pid" 2>/dev/null || true
    fi
}
interrupt() {
    kill "$client_pid" 2>/dev/null || true
    exit 130
}
trap cleanup EXIT
trap interrupt INT TERM

log_file=""
for _ in {1..50}; do
    candidate="$(ls -1t /tmp/GilStreaming-*.log 2>/dev/null | head -n 1 || true)"
    if [[ -n "$candidate" ]]; then
        log_epoch="${candidate##*/GilStreaming-}"
        log_epoch="${log_epoch%.log}"
        if [[ "$log_epoch" =~ ^[0-9]+$ ]] && (( log_epoch >= started_at - 1 )); then
            log_file="$candidate"
            break
        fi
    fi
    if ! kill -0 "$client_pid" 2>/dev/null; then
        break
    fi
    sleep 0.2
done

if [[ -n "$log_file" ]]; then
    echo "Following $log_file (Ctrl+C stops the debug client)"
    tail -n +1 -f "$log_file" &
    tail_pid=$!
else
    echo "GilStreaming log file was not found in /tmp." >&2
fi

wait "$client_pid"
