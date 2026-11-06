# Build the Windows installer: dist\shrink-your-shit-setup.exe
# Needs: Go, Inno Setup 6 (ISCC.exe). Run on Windows:  pwsh installer\windows\build.ps1 -Version 0.1.0
param([string]$Version = '0.0.0-dev')
$ErrorActionPreference = 'Stop'
$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$out = Join-Path $root 'dist\windows'
New-Item -ItemType Directory -Force -Path $out | Out-Null

Push-Location $root
try {
  $env:CGO_ENABLED = '0'
  go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $out 'shrink-your-shit.exe') .
  if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} finally { Pop-Location }

$iscc = @(
  "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
  "$env:ProgramFiles\Inno Setup 6\ISCC.exe",
  "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $iscc) { throw 'Inno Setup 6 (ISCC.exe) not found. Install it: choco install innosetup' }

& $iscc "/DAppVersion=$Version" (Join-Path $PSScriptRoot 'shrink-your-shit.iss')
if ($LASTEXITCODE -ne 0) { throw 'Inno Setup failed' }
Get-Item (Join-Path $root 'dist\shrink-your-shit-setup.exe')
