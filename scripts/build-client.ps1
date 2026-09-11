[CmdletBinding()]
param(
    [ValidateSet("Debug", "Release")]
    [string]$Configuration = "Debug",
    [string]$QtBin = ""
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$configurationName = $Configuration.ToLowerInvariant()

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    throw "Git is required and must be available on PATH."
}

$qmake = $null
if ($QtBin) {
    $candidate = Join-Path $QtBin "qmake.exe"
    if (Test-Path -LiteralPath $candidate -PathType Leaf) { $qmake = $candidate }
}
foreach ($name in @("qmake6.exe", "qmake.exe")) {
    if (-not $qmake) {
        $command = Get-Command $name -ErrorAction SilentlyContinue
        if ($command) { $qmake = $command.Source }
    }
}
if (-not $qmake) {
    $candidate = Get-ChildItem "C:\Qt\*\msvc*_64\bin\qmake.exe" -ErrorAction SilentlyContinue |
        Sort-Object FullName -Descending | Select-Object -First 1
    if ($candidate) { $qmake = $candidate.FullName }
}
if (-not $qmake) {
    throw "Qt qmake was not found. Add the Qt MSVC bin directory to PATH or pass -QtBin."
}

$vswhereCandidates = @(
    (Get-Command vswhere.exe -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
    "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe",
    "$env:ProgramFiles\Microsoft Visual Studio\Installer\vswhere.exe"
) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) }
$vswhere = $vswhereCandidates | Select-Object -First 1
if (-not $vswhere) {
    throw "Visual Studio Build Tools with the MSVC x64 workload are required."
}
$vsInstall = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if (-not $vsInstall) {
    throw "No Visual Studio installation with MSVC x64 tools was found."
}
$vcvars = Join-Path $vsInstall "VC\Auxiliary\Build\vcvarsall.bat"

Write-Host "Initializing submodules..."
& git -C $repoRoot submodule update --init --recursive
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$dependencyDir = Join-Path $repoRoot "libs\windows\lib\x64"
if (-not (Test-Path -LiteralPath $dependencyDir -PathType Container)) {
    Write-Host "Downloading Windows client dependencies..."
    & (Join-Path $repoRoot "setup-deps.ps1")
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

$buildDir = Join-Path $repoRoot "build\build-x64-$configurationName"
New-Item -ItemType Directory -Force -Path $buildDir | Out-Null
$project = Join-Path $repoRoot "moonlight-qt.pro"
$command = 'call "' + $vcvars + '" x64 && cd /d "' + $buildDir +
    '" && "' + $qmake + '" "' + $project + '" && nmake ' + $configurationName

Write-Host "Building GilStreaming client ($Configuration)..."
& $env:ComSpec /d /c $command
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$binary = Join-Path $buildDir "app\$configurationName\GilStreaming.exe"
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Build completed but the client executable was not found at $binary"
}
Write-Host "Client built at $binary"
