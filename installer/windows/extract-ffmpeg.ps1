# Pull ffmpeg.exe and ffprobe.exe out of an ffmpeg build zip into a folder.
# Used by the Windows installer (shrink-your-shit.iss); works in Windows PowerShell 5.1 and PowerShell 7.
param(
  [Parameter(Mandatory)][string]$Zip,
  [Parameter(Mandatory)][string]$Dest
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$archive = [IO.Compression.ZipFile]::OpenRead($Zip)
try {
  $found = 0
  foreach ($entry in $archive.Entries) {
    if ($entry.FullName -match '/bin/(ffmpeg|ffprobe)\.exe$') {
      [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, (Join-Path $Dest ([IO.Path]::GetFileName($entry.FullName))), $true)
      $found++
    }
  }
} finally {
  $archive.Dispose()
}
if ($found -ne 2) { throw "expected ffmpeg.exe and ffprobe.exe in the zip, found $found of them" }
