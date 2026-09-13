# One-time migration from v0.1. Run elevated as the SAME Windows account that
# will use qbmcp. This script does not install or start the user-level program.
param([string]$UserDataDir = (Join-Path $env:LOCALAPPDATA 'qbmcp'))
$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this one-time migration in an Administrator PowerShell for the same user.'
}
$legacy = Get-CimInstance Win32_Service -Filter "Name='qbmcp'"
if (-not $legacy) { Write-Host 'No legacy qbmcp service is installed.'; return }
$legacyExe = Join-Path $env:ProgramData 'qbmcp\bin\qbmcp.exe'
$image = $legacy.PathName
if (-not ($image.StartsWith(('"' + $legacyExe + '"'), [StringComparison]::OrdinalIgnoreCase) -or
          $image.StartsWith(($legacyExe + ' '), [StringComparison]::OrdinalIgnoreCase))) {
    throw 'The service executable is not the expected legacy qbmcp binary. No changes made.'
}
$source = Join-Path $env:ProgramData 'qbmcp\config.json'
$destination = Join-Path $UserDataDir 'config.json'
if (-not (Test-Path -LiteralPath $source)) { throw 'Legacy config.json was not found. No changes made.' }
$configText = [IO.File]::ReadAllText($source)
$config = $configText | ConvertFrom-Json
if ($config.port -lt 1 -or $config.port -gt 65535 -or $config.token -notmatch '^[0-9a-fA-F]{64}$') {
    throw 'Legacy configuration is invalid. No changes made.'
}
if ((Test-Path -LiteralPath $destination) -and [IO.File]::ReadAllText($destination).Trim() -cne $configText.Trim()) {
    throw 'A different user config.json already exists. Back it up and resolve the configuration before retrying; no service changes made.'
}
$null = New-Item -ItemType Directory -Path $UserDataDir -Force
$folder = Get-Item -LiteralPath $UserDataDir
if ($folder.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'User data directory must not be a link.' }
$acl = Get-Acl -LiteralPath $UserDataDir
$acl.SetAccessRuleProtection($true, $false)
foreach ($rule in @($acl.Access)) { $null = $acl.RemoveAccessRuleSpecific($rule) }
$rights = [Security.AccessControl.FileSystemRights]::FullControl
$inherit = [Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
$propagation = [Security.AccessControl.PropagationFlags]::None
$allow = [Security.AccessControl.AccessControlType]::Allow
foreach ($sid in @($identity.User, [Security.Principal.SecurityIdentifier]::new('S-1-5-18'), [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))) {
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, $rights, $inherit, $propagation, $allow))
}
Set-Acl -LiteralPath $UserDataDir -AclObject $acl
[IO.File]::WriteAllText($destination, $configText, [Text.UTF8Encoding]::new($false))
# Disable first so an already queued recovery cannot restart the old service.
& "$env:SystemRoot\System32\sc.exe" config qbmcp start= disabled
if ($LASTEXITCODE -ne 0) { throw 'Could not disable the old service. User config was copied; original config remains intact.' }
$service = Get-Service -Name qbmcp
if ($service.Status -ne 'Stopped') {
    Stop-Service -Name qbmcp
    $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}
& "$env:SystemRoot\System32\sc.exe" delete qbmcp
if ($LASTEXITCODE -ne 0) { throw 'Could not unregister the old service. Its files and both configuration copies remain intact.' }
Write-Host 'Legacy service stopped and unregistered. Configuration/token copied; original files retained.'
Write-Host 'Now use a NORMAL PowerShell to run install.ps1 -Enable -Start.'
