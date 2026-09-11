#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
dependency_root="$repo_root/moonlight-common-c/moonlight-common-c"
patch_file="$repo_root/patches/moonlight-common-c-first-frame-timeout.patch"

if git -C "$dependency_root" apply --check "$patch_file" 2>/dev/null; then
    git -C "$dependency_root" apply "$patch_file"
    echo "Applied GilStreaming first-frame startup patch."
elif git -C "$dependency_root" apply --reverse --check "$patch_file" 2>/dev/null; then
    echo "GilStreaming first-frame startup patch is already applied."
else
    echo "Unable to apply the GilStreaming first-frame startup patch." >&2
    echo "Check moonlight-common-c/src/VideoStream.c and src/Limelight.h for conflicting changes." >&2
    exit 1
fi
