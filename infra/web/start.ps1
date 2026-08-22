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

& "$DIR\ioWolfMP.x64.exe" +set fs_basepath "$DIR" +set fs_game s4ndmod26 +connect s4ndmod.com
