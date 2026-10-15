$ErrorActionPreference = "Stop"
$BASE = if ($env:RTCW_BASE) { $env:RTCW_BASE } else { "http://26.s4ndmod.com" }
# Default to the folder the engine itself uses for configs (Documents\RTCW, wherever Documents is redirected),
# so existing wolfconfig_mp.cfg / binds carry over and base + home path are the same folder.
$DEST = if ($env:RTCW_DIR)  { $env:RTCW_DIR  } else { Join-Path ([Environment]::GetFolderPath("MyDocuments")) "RTCW" }

Write-Host "Installing iortcw + s4ndmod26 to $DEST"
foreach ($d in @("$DEST\main", "$DEST\s4ndmod26")) {
    [void](New-Item -ItemType Directory -Force $d)
}

function dl_skip_existing($url, $path) {
    if (Test-Path $path) {
        Write-Host "  $path (already present, skipping)"
        return
    }
    Write-Host "  $path"
    Invoke-WebRequest -Uri $url -OutFile $path -UseBasicParsing
}

function dl_force($url, $path) {
    Write-Host "  $path"
    Invoke-WebRequest -Uri $url -OutFile $path -UseBasicParsing
}

dl_force "$BASE/downloads/windows/ioWolfMP.x64.exe"            "$DEST\ioWolfMP.x64.exe"
dl_force "$BASE/downloads/windows/renderer_mp_opengl1_x64.dll" "$DEST\renderer_mp_opengl1_x64.dll"
dl_force "$BASE/downloads/windows/SDL264.dll"                  "$DEST\SDL264.dll"
dl_force "$BASE/downloads/windows/OpenAL64.dll"                "$DEST\OpenAL64.dll"
dl_force "$BASE/downloads/windows/libstdc++-6.dll"             "$DEST\libstdc++-6.dll"
dl_force "$BASE/downloads/windows/libgcc_s_seh-1.dll"          "$DEST\libgcc_s_seh-1.dll"
dl_force "$BASE/downloads/windows/libwinpthread-1.dll"         "$DEST\libwinpthread-1.dll"
# cgame/ui ship inside the pk3.  A loose copy would be loaded ahead of it by an unpure server,
# so remove any left behind by older installs instead of downloading them.
New-Item -ItemType Directory -Force "$DEST\s4ndmod26" | Out-Null
Remove-Item -Force -ErrorAction SilentlyContinue "$DEST\s4ndmod26\cgame_mp_x64.dll","$DEST\s4ndmod26\ui_mp_x64.dll"

Write-Host "Downloading base paks..."
foreach ($pk in @("demopak0")) {
    dl_skip_existing "$BASE/downloads/main/$pk.pk3" "$DEST\main\$pk.pk3"
}

# The pk3 name carries the build version; latest.txt names the current one.  Remove older copies.
$Pk3 = (Invoke-WebRequest -Uri "$BASE/downloads/s4ndmod26/latest.txt" -UseBasicParsing).Content.Trim()
Remove-Item -Force -ErrorAction SilentlyContinue "$DEST\s4ndmod26\s4ndmod26.pk3","$DEST\s4ndmod26\s4ndmod26.*.pk3","$DEST\s4ndmod26\s4ndmod26_*.pk3"
dl_force "$BASE/downloads/s4ndmod26/$Pk3" "$DEST\s4ndmod26\$Pk3"

$ConnectHost = ($BASE -replace '^[a-zA-Z]+://','') -replace '[:/].*',''

# start.cmd next to the exe (a .cmd runs under any PowerShell execution policy and is double-clickable).
# Paths come from the script's own folder, so nothing depends on ~ expansion.  "%~dp0." avoids the
# trailing-backslash-before-quote problem of a bare "%~dp0".
@(
  '@echo off',
  '"%~dp0ioWolfMP.x64.exe" +set fs_basepath "%~dp0." +set fs_homepath "%~dp0." +set fs_game s4ndmod26 +connect ' + $ConnectHost
) | Set-Content -Encoding ASCII "$DEST\start.cmd"

Write-Host ""
Write-Host "Done. Run the game with:"
Write-Host "  & `"$DEST\start.cmd`""
