# Installs the S4NDMoD26 launcher (Windows x64).  The launcher then installs and updates the game itself.
#   $env:RTCW_BASE="https://host"; iwr -useb https://host/install.ps1 | iex
# Optional: $env:S4ND_DIR to choose where the launcher goes (default %LOCALAPPDATA%\S4NDMoD26).
$ErrorActionPreference = "Stop"
$BASE = if ($env:RTCW_BASE) { $env:RTCW_BASE.TrimEnd('/') } else { "https://s4ndmod.com" }
$DEST = if ($env:S4ND_DIR)  { $env:S4ND_DIR } else { Join-Path $env:LOCALAPPDATA "S4NDMoD26" }

[void](New-Item -ItemType Directory -Force $DEST)
$exe = Join-Path $DEST "s4ndmod-launcher.exe"

Write-Host "Downloading the launcher to $exe"
Invoke-WebRequest -Uri "$BASE/downloads/launcher/s4ndmod-launcher.exe" -OutFile "$exe.new" -UseBasicParsing
Move-Item -Force "$exe.new" $exe

# a Start Menu shortcut, so it can be opened again later
try {
    $lnk = Join-Path ([Environment]::GetFolderPath("Programs")) "S4NDMoD26.lnk"
    $sc = (New-Object -ComObject WScript.Shell).CreateShortcut($lnk)
    $sc.TargetPath = $exe
    $sc.WorkingDirectory = $DEST
    $sc.Save()
    Write-Host "Added a Start Menu shortcut: S4NDMoD26"
} catch { }

Start-Process -FilePath $exe -ArgumentList @("--server", $BASE) -WorkingDirectory $DEST
