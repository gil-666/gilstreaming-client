[CmdletBinding()]
param(
    [string]$CoordinatorUrl = "http://127.0.0.1:6766",
    [string]$QtBin = ""
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$buildDir = Join-Path $repoRoot "build\build-x64-debug"
$binary = Join-Path $buildDir "app\debug\GilStreaming.exe"
$dependencyDir = Join-Path $repoRoot "libs\windows\lib\x64"
$antiHooking = Join-Path $buildDir "AntiHooking\debug"

if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Debug client not found. Run .\scripts\build-client.ps1 -Configuration Debug first."
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

$qtRuntime = Split-Path -Parent $qmake
$env:Path = "$qtRuntime;$dependencyDir;$antiHooking;$env:Path"
$env:GILSTREAMING_COORDINATOR_URL = $CoordinatorUrl
$started = Get-Date

Write-Host "Starting debug client against $CoordinatorUrl"
$process = Start-Process -FilePath $binary -WorkingDirectory $repoRoot -PassThru

$log = $null
$deadline = (Get-Date).AddSeconds(10)
do {
    $log = Get-ChildItem $env:TEMP -Filter "GilStreaming-*.log" -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTime -ge $started.AddSeconds(-2) } |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $log) { Start-Sleep -Milliseconds 200 }
} while (-not $log -and -not $process.HasExited -and (Get-Date) -lt $deadline)

if (-not $log) {
    if ($process.HasExited) {
        throw "GilStreaming exited with code $($process.ExitCode) before creating a log."
    }
    throw "GilStreaming started, but its log file was not found in $env:TEMP."
}

Write-Host "Following $($log.FullName) (Ctrl+C stops following the log)"
$stream = [System.IO.File]::Open($log.FullName, [System.IO.FileMode]::Open,
    [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
$reader = [System.IO.StreamReader]::new($stream)
try {
    while (-not $process.HasExited) {
        while (-not $reader.EndOfStream) {
            Write-Host $reader.ReadLine()
        }
        Start-Sleep -Milliseconds 200
        $process.Refresh()
    }
    while (-not $reader.EndOfStream) {
        Write-Host $reader.ReadLine()
    }
}
finally {
    $reader.Dispose()
    $stream.Dispose()
}
exit $process.ExitCode
