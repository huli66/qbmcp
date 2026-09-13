param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Architecture = 'amd64'
)
$ErrorActionPreference = 'Stop'
$repo = Split-Path $PSScriptRoot -Parent
$dist = Join-Path $repo 'dist'
$source = Get-Content -LiteralPath (Join-Path $repo 'internal\config\config.go') -Raw
if ($source -notmatch 'const Version = "([^"]+)"') { throw 'Cannot read the application version.' }
$version = $Matches[1]
$packageName = "qbmcp_${version}_windows_${Architecture}"
$binary = Join-Path $dist 'qbmcp.exe'
$archive = Join-Path $dist "$packageName.zip"
$staging = Join-Path $dist ('.package-' + [Guid]::NewGuid().ToString('N'))
$previousEnv = @{}
foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
    $previousEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
Push-Location $repo
try {
    New-Item -ItemType Directory -Path $dist -Force | Out-Null
    $env:GOOS = 'windows'
    $env:GOARCH = $Architecture
    $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags '-s -w' -o $binary ./cmd/qbmcp
    if ($LASTEXITCODE -ne 0) { throw 'Build failed; no package was created.' }
    New-Item -ItemType Directory -Path (Join-Path $staging 'docs') -Force | Out-Null
    Copy-Item -LiteralPath $binary -Destination (Join-Path $staging 'qbmcp.exe')
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'install.ps1') -Destination $staging
    Copy-Item -LiteralPath (Join-Path $repo 'README.md') -Destination $staging
    foreach ($doc in @('architecture.md', 'protocol.md', 'install.md', 'testing.md')) {
        Copy-Item -LiteralPath (Join-Path $repo "docs\$doc") -Destination (Join-Path $staging 'docs')
    }
    Compress-Archive -Path (Join-Path $staging '*') -DestinationPath $archive -Force
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$archive.sha256", "$hash  $packageName.zip`n", [Text.UTF8Encoding]::new($false))
    Write-Host "Version: $version"
    Write-Host "Binary: $binary"
    Write-Host "Package: $archive"
    Write-Host "SHA256: $hash"
} finally {
    foreach ($name in $previousEnv.Keys) {
        [Environment]::SetEnvironmentVariable($name, $previousEnv[$name], 'Process')
    }
    Pop-Location
    if (Test-Path -LiteralPath $staging) {
        $resolvedStaging = [IO.Path]::GetFullPath($staging)
        $resolvedDist = [IO.Path]::GetFullPath($dist).TrimEnd('\') + '\'
        if (-not $resolvedStaging.StartsWith($resolvedDist, [StringComparison]::OrdinalIgnoreCase)) {
            throw 'Refusing to clean a staging directory outside dist.'
        }
        Remove-Item -LiteralPath $resolvedStaging -Recurse -Force
    }
}
