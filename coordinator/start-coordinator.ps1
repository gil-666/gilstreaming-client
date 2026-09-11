$ErrorActionPreference = "Stop"

$coordinatorRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$binary = Join-Path $coordinatorRoot "gilstreaming-coordinator.exe"
$config = Join-Path $coordinatorRoot "config.json"
$envFile = Join-Path $coordinatorRoot ".env"

if (-not (Test-Path -LiteralPath $config -PathType Leaf)) {
    throw "config.json is missing. Copy config.example.json to config.json and edit it first."
}

Push-Location $coordinatorRoot
try {
    & $binary -config $config -env-file $envFile
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
