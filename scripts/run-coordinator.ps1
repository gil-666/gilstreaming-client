[CmdletBinding()]
param(
    [string]$Config = "coordinator/config.json",
    [string]$EnvFile = "coordinator/.env"
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot

function Get-RepoPath([string]$Path) {
    if ([System.IO.Path]::IsPathRooted($Path)) {
        return $Path
    }
    return Join-Path $repoRoot $Path
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go 1.22 or newer is required and must be available on PATH."
}

$configPath = Get-RepoPath $Config
$envPath = Get-RepoPath $EnvFile
if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) {
    throw "Coordinator config not found: $configPath. Copy coordinator/config.example.json to coordinator/config.json first."
}

$outputDir = Join-Path $repoRoot "build/coordinator"
$binary = Join-Path $outputDir "gilstreaming-coordinator.exe"
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

Write-Host "Building GilStreaming coordinator..."
Push-Location (Join-Path $repoRoot "coordinator")
try {
    & go build -o $binary .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
finally {
    Pop-Location
}

Write-Host "Starting coordinator with $configPath"
& $binary -config $configPath -env-file $envPath
exit $LASTEXITCODE
