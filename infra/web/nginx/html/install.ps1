$ErrorActionPreference = "Stop"
$BASE = if ($env:RTCW_BASE) { $env:RTCW_BASE } else { "http://26.s4ndmod.com" }
$DEST = if ($env:RTCW_DIR)  { $env:RTCW_DIR  } else { "$env:USERPROFILE\wolf" }

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
dl_force "$BASE/downloads/windows/s4ndmod26/cgame_mp_x64.dll"  "$DEST\s4ndmod26\cgame_mp_x64.dll"
dl_force "$BASE/downloads/windows/s4ndmod26/ui_mp_x64.dll"     "$DEST\s4ndmod26\ui_mp_x64.dll"

Write-Host "Downloading base paks..."
foreach ($pk in @("pak0","mp_pak0","mp_pak1","mp_pak2","mp_pak3","mp_pak4","mp_pak5")) {
    dl_skip_existing "$BASE/downloads/main/$pk.pk3" "$DEST\main\$pk.pk3"
}

dl_force "$BASE/downloads/s4ndmod26/s4ndmod26.pk3" "$DEST\s4ndmod26\s4ndmod26.pk3"

Write-Host ""
Write-Host "Done. Run the game with:"
Write-Host "  $DEST\ioWolfMP.x64.exe +set fs_basepath `"$DEST`" +set fs_game s4ndmod26 +connect s4ndmod.com"
