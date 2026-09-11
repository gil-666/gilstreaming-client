[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$dependencyRoot = Join-Path $repoRoot "moonlight-common-c\moonlight-common-c"
$patchFile = Join-Path $repoRoot "patches\moonlight-common-c-first-frame-timeout.patch"

& git -C $dependencyRoot apply --check $patchFile 2>$null
if ($LASTEXITCODE -eq 0) {
    & git -C $dependencyRoot apply $patchFile
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Write-Host "Applied GilStreaming first-frame startup patch."
    exit 0
}

& git -C $dependencyRoot apply --reverse --check $patchFile 2>$null
if ($LASTEXITCODE -eq 0) {
    Write-Host "GilStreaming first-frame startup patch is already applied."
    exit 0
}

Write-Error "Unable to apply the GilStreaming first-frame startup patch. Check moonlight-common-c/src/VideoStream.c and src/Limelight.h for conflicting changes."
exit 1
