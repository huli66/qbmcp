param(
    [string]$BinaryPath,
    [switch]$Enable,
    [switch]$Start
)
$ErrorActionPreference = 'Stop'
if (-not $BinaryPath) {
    $BinaryPath = Join-Path $PSScriptRoot 'qbmcp.exe'
    if (-not (Test-Path -LiteralPath $BinaryPath)) {
        $BinaryPath = Join-Path $PSScriptRoot 'dist\qbmcp.exe'
    }
}
$BinaryPath = (Resolve-Path -LiteralPath $BinaryPath).Path
& $BinaryPath install
if ($LASTEXITCODE -ne 0) { throw 'qbmcp install failed. See the error above.' }
if ($env:QBMCP_HOME) {
    $qbBin = Join-Path $env:QBMCP_HOME 'bin'
} else {
    $qbBin = Join-Path $env:LOCALAPPDATA 'Programs\qbmcp'
}
if (($env:Path -split ';') -notcontains $qbBin) { $env:Path = "$qbBin;$env:Path" }
$installed = Join-Path $qbBin 'qbmcp.exe'
if ($Enable) {
    & $installed enable
    if ($LASTEXITCODE -ne 0) { throw 'qbmcp enable failed.' }
}
if ($Start) {
    & $installed start
    if ($LASTEXITCODE -ne 0) { throw 'qbmcp start failed.' }
}
Write-Host 'Ready: qbmcp help (other open terminals may need to be restarted).'
