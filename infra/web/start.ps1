$ErrorActionPreference = "Stop"
$DIR = Split-Path -Parent $MyInvocation.MyCommand.Path
$BASE = "https://s4ndmod.com"

New-Item -ItemType Directory -Force "$DIR\main" | Out-Null

Write-Host "Checking base game paks..."
foreach ($pak in @("demopak0")) {
  $path = "$DIR\main\$pak.pk3"
  if (-not (Test-Path $path)) {
    Write-Host "  Downloading $pak.pk3..."
    Invoke-WebRequest -Uri "$BASE/downloads/main/$pak.pk3" -OutFile $path -UseBasicParsing
  }
}

# Loose cgame/ui modules are loaded ahead of the pk3 by an unpure server and go stale on update;
# the engine re-extracts them from the pk3 when they are missing.
Remove-Item -Force -ErrorAction SilentlyContinue "$DIR\s4ndmod26\cgame_mp_x64.dll","$DIR\s4ndmod26\ui_mp_x64.dll"

# fs_homepath = install dir keeps everything portable (otherwise files go to Documents\RTCW)
& "$DIR\ioWolfMP.x64.exe" +set fs_basepath "$DIR" +set fs_homepath "$DIR" +set fs_game s4ndmod26 +connect s4ndmod.com
