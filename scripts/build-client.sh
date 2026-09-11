#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
configuration="debug"

for argument in "$@"; do
    case "$argument" in
        debug|release) configuration="$argument" ;;
        *)
            echo "Usage: $0 [debug|release]" >&2
            exit 2
            ;;
    esac
done

if ! command -v git >/dev/null 2>&1; then
    echo "Git is required and must be available on PATH." >&2
    exit 1
fi
if command -v qmake6 >/dev/null 2>&1; then
    qmake_command="qmake6"
elif command -v qmake >/dev/null 2>&1; then
    qmake_command="qmake"
else
    echo "Qt qmake was not found. Install the Qt development packages listed in README.md." >&2
    exit 1
fi
if ! command -v make >/dev/null 2>&1; then
    echo "GNU make and a C++ compiler are required." >&2
    exit 1
fi

echo "Initializing submodules..."
git -C "$repo_root" submodule update --init --recursive

build_dir="$repo_root/build/build-linux-$configuration"
mkdir -p "$build_dir"

echo "Configuring GilStreaming client..."
(
    cd "$build_dir"
    "$qmake_command" "$repo_root/moonlight-qt.pro"
)

jobs=1
if command -v nproc >/dev/null 2>&1; then
    jobs="$(nproc)"
elif command -v getconf >/dev/null 2>&1; then
    jobs="$(getconf _NPROCESSORS_ONLN)"
fi

echo "Building GilStreaming client ($configuration)..."
make -C "$build_dir" -j"$jobs" "$configuration"

binary="$build_dir/app/gilstreaming"
if [[ ! -x "$binary" ]]; then
    echo "Build completed but the client executable was not found at $binary" >&2
    exit 1
fi
echo "Client built at $binary"
