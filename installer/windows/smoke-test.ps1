# End-to-end test of the Windows installer on a real Windows machine (used in CI):
# silent install -> web page up -> real conversion -> upgrade keeps settings -> uninstall.
# Usage: pwsh installer\windows\smoke-test.ps1 -Setup dist\shrink-your-shit-setup.exe
param(
  [Parameter(Mandatory)][string]$Setup,
  [string]$Library = 'C:\syp-smoke-lib',
  [int]$Port = 8484
)
$ErrorActionPreference = 'Stop'
$Setup = (Resolve-Path $Setup).Path
$app = Join-Path $env:LOCALAPPDATA 'shrink-your-shit'
$statusUrl = "http://127.0.0.1:$Port/api/status"
$setupLog = Join-Path $env:TEMP 'syp-setup.log'

$diag = Join-Path (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path 'dist\diag'

function Step($m) { Write-Host "=== $m" }

# What is running that could be part of the installer / program chain (names and parents).
function ProcessReport() {
  try {
    Get-CimInstance Win32_Process |
      Where-Object { $_.Name -match 'wscript|cscript|powershell|pwsh|shrink-your-shit|ffmpeg|ffprobe|setup|unins|cmd|conhost' } |
      Select-Object ProcessId, ParentProcessId, Name, CommandLine |
      Format-Table -AutoSize -Wrap | Out-String -Width 250
  } catch { "could not list processes: $_" }
}

# Copy what helps to debug into dist\diag (CI uploads that folder even when the test fails).
function SaveDiagnostics() {
  New-Item -ItemType Directory -Force -Path $diag | Out-Null
  foreach ($f in @($setupLog, (Join-Path $app 'shrink-your-shit.log'), (Join-Path $app 'launch.vbs'))) {
    if (Test-Path $f) { Copy-Item $f $diag -Force }
  }
  ProcessReport | Out-File (Join-Path $diag 'processes.txt')
  if (Test-Path $app) { Get-ChildItem $app -Recurse -ErrorAction SilentlyContinue | Select-Object FullName, Length | Out-File (Join-Path $diag 'app-files.txt') }
}

function Fail($m) {
  Write-Host "FAIL: $m"
  foreach ($f in @($setupLog, (Join-Path $app 'shrink-your-shit.log'))) {
    if (Test-Path $f) { Write-Host "--- $f (last 80 lines)"; Get-Content $f -Tail 80 }
  }
  Write-Host '--- processes'
  Write-Host (ProcessReport)
  SaveDiagnostics
  exit 1
}
function Status() { try { Invoke-RestMethod -Uri $statusUrl -TimeoutSec 3 } catch { $null } }
function WaitFor($what, [scriptblock]$cond, $seconds) {
  $end = (Get-Date).AddSeconds($seconds)
  while ((Get-Date) -lt $end) { if (& $cond) { return } Start-Sleep -Seconds 2 }
  Fail "timed out waiting for $what"
}

# Run an installer/uninstaller and wait for THAT process only, but never forever: after four minutes
# print what is going on, kill everything we started, and fail.
function RunAndWait([string]$exe, [string[]]$procArgs, [string]$label) {
  $p = Start-Process -FilePath $exe -ArgumentList $procArgs -PassThru
  $null = $p.Handle   # keeps ExitCode readable in Windows PowerShell 5.1
  if (-not $p.WaitForExit(240000)) {
    Write-Host "TIMEOUT: $label did not finish within 4 minutes (pid $($p.Id))"
    Write-Host '--- processes at the time of the timeout'
    Write-Host (ProcessReport)
    if (Test-Path $setupLog) { Write-Host "--- $setupLog (last 80 lines)"; Get-Content $setupLog -Tail 80 }
    SaveDiagnostics
    & taskkill.exe /F /T /PID $p.Id 2>&1 | Out-Null
    & taskkill.exe /F /T /IM shrink-your-shit.exe 2>&1 | Out-Null
    & taskkill.exe /F /T /IM wscript.exe 2>&1 | Out-Null
    Fail "$label hung"
  }
  if ($p.ExitCode -ne 0) { Fail "$label exit code $($p.ExitCode)" }
}
function RunSetup([string[]]$extra) {
  RunAndWait $Setup (@('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$setupLog") + $extra) 'the installer'
}

# Make sure the installer has to download its own ffmpeg: hide any ffmpeg already on this machine.
$env:PATH = (($env:PATH -split ';') | Where-Object { $_ -and -not (Test-Path (Join-Path $_ 'ffmpeg.exe')) }) -join ';'
if (Get-Command ffmpeg -ErrorAction SilentlyContinue) { Fail 'ffmpeg is still on PATH for the test' }
New-Item -ItemType Directory -Force -Path $Library | Out-Null

Step 'silent install'
RunSetup @("/LIBRARY=$Library")
foreach ($f in 'shrink-your-shit.exe', 'launch.vbs', 'ffmpeg\ffmpeg.exe', 'ffmpeg\ffprobe.exe') {
  if (-not (Test-Path (Join-Path $app $f))) { Fail "missing $f after install" }
}
$run = (Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -ErrorAction SilentlyContinue).'shrink-your-shit'
if (-not $run) { Fail 'autostart Run key not created' }
$lnk = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\shrink-your-shit.lnk'
if (-not (Test-Path $lnk)) { Fail 'Start Menu shortcut not created' }

Step 'web page answers'
WaitFor 'the web page' { $null -ne (Status) } 60
$ver = & (Join-Path $app 'shrink-your-shit.exe') -version
Write-Host $ver

Step 'ffmpeg can encode HEVC'
$enc = & (Join-Path $app 'ffmpeg\ffmpeg.exe') -hide_banner -encoders 2>&1 | Out-String
if ($enc -notmatch 'libx265') { Fail 'downloaded ffmpeg has no libx265' }

Step 'real conversion'
$video = Join-Path $Library 'movie.mp4'
& (Join-Path $app 'ffmpeg\ffmpeg.exe') -v error -f lavfi -i 'testsrc2=duration=12:size=1280x720:rate=30' `
  -f lavfi -i 'sine=duration=12' -c:v libx264 -b:v 8M -c:a aac $video
if ($LASTEXITCODE -ne 0) { Fail 'could not generate the test video' }
$before = (Get-Item $video).Length
(Get-Item $video).LastWriteTime = (Get-Date).AddMinutes(-2)
# A fresh install's library waits for first-run's Start (DESIGN/FIRST-RUN.md); press it through the API.
$libs = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/libraries" -TimeoutSec 5
$lib = $libs.items | Select-Object -First 1
if (-not $lib) { Fail 'no library was created from /LIBRARY' }
if ($lib.started) { Fail 'a fresh install''s library must wait for first-run, but it is already started' }
Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:$Port/api/libraries/$($lib.id)/scan" -ContentType 'application/json' -Body '{}' -TimeoutSec 10 | Out-Null
WaitFor 'the job to finish' {
  $s = Status
  $job = $s.jobs | Where-Object { $_.path -like '*movie.mp4' } | Select-Object -First 1
  if ($job -and $job.status -eq 'failed') { Fail "job failed: $($job.error)" }
  $job -and $job.status -eq 'done'
} 300
$codec = & (Join-Path $app 'ffmpeg\ffprobe.exe') -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 $video
$after = (Get-Item $video).Length
Write-Host "codec=$codec size $before -> $after"
if ($codec.Trim() -ne 'hevc') { Fail "expected hevc, got $codec" }
if ($after -ge $before) { Fail 'converted file is not smaller' }

Step 'upgrade (run the installer again, no flags) keeps the settings'
RunSetup @()
$vbs = Get-Content (Join-Path $app 'launch.vbs') -Raw
if ($vbs -notlike "*$Library*") { Fail 'library setting lost after upgrade' }
WaitFor 'the web page after upgrade' { $null -ne (Status) } 60

Step 'uninstall'
$un = Get-ChildItem $app -Filter 'unins*.exe' | Select-Object -First 1
if (-not $un) { Fail 'no uninstaller found' }
RunAndWait $un.FullName @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART') 'the uninstaller'
WaitFor 'the program to stop' { -not (Get-Process shrink-your-shit -ErrorAction SilentlyContinue) } 30
if (Test-Path (Join-Path $app 'shrink-your-shit.exe')) { Fail 'program file still there after uninstall' }
if ((Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -ErrorAction SilentlyContinue).'shrink-your-shit') { Fail 'Run key still there after uninstall' }
if (Test-Path $lnk) { Fail 'Start Menu shortcut still there after uninstall' }
if ($null -ne (Status)) { Fail 'web page still answering after uninstall' }

Write-Host 'PASS: install, convert, upgrade, uninstall'
