# syntax=docker/dockerfile:1
#
# Builds a self-contained iortcw dedicated server with Omnibot RTCW AI bots.
# Also produces game modules for all platforms (for client mod distribution).
#
# Server stages:
#   game-linux-64       — qagame/cgame/ui Linux x86_64
#   game-linux-32       — cgame/ui Linux i386 (OG 32-bit Linux RTCW)
#   game-win-64         — qagame/cgame/ui Windows x64 (iortcw Windows)
#   game-win-32         — cgame/ui Windows x86 (OG 32-bit Windows RTCW)
#   omnibot-lib-builder — omnibot_rtcw.x86_64.so
#   iortcw-builder      — iowolfded.x86_64
#   runtime             — server image (Linux 64-bit only)
#   mod-package         — all platform binaries collected together

# ── Linux source base (no MinGW — keeps toolset unambiguous) ─────────────────
FROM debian:bullseye-slim AS game-src-linux
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ gcc-multilib g++-multilib \
    libboost-tools-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY mod /build/mod
COPY omnibot/Common /build/omnibot/Common
COPY omnibot/RTCW   /build/omnibot/RTCW
COPY third_party/zlib /build/third_party/zlib

# ── Windows source base (MinGW only) ─────────────────────────────────────────
FROM debian:bullseye-slim AS game-src-windows
RUN apt-get update && apt-get install -y --no-install-recommends \
    mingw-w64 \
    libboost-tools-dev \
    && rm -rf /var/lib/apt/lists/*

# Tell bjam about the MinGW cross-compilers
RUN printf 'using gcc : mingw32 : i686-w64-mingw32-g++ ;\nusing gcc : mingw64 : x86_64-w64-mingw32-g++ ;\n' \
    > /root/user-config.jam

WORKDIR /build
COPY mod /build/mod
COPY omnibot/Common /build/omnibot/Common
COPY omnibot/RTCW   /build/omnibot/RTCW
COPY third_party/zlib /build/third_party/zlib

# ── Linux 64-bit (iortcw Linux, server) ───────────────────────────────────────
FROM game-src-linux AS game-linux-64
WORKDIR /build/mod/src
RUN --mount=type=cache,target=/build/mod/src/build,id=rtcw-linux-64 \
    bjam -a -q address-model=64 strip=on release \
    && mkdir -p /out \
    && find build -name "*.so" -exec cp {} /out/ \;

# ── Linux 32-bit (cgame/ui only — for OG 32-bit Linux RTCW clients) ──────────
FROM game-src-linux AS game-linux-32
WORKDIR /build/mod/src
RUN --mount=type=cache,target=/build/mod/src/build,id=rtcw-linux-32v3 \
    bjam -a -q address-model=32 architecture=x86 strip=on release \
    && mkdir -p /out \
    && find build -name "cgame.mp.i386.so" -exec cp {} /out/ \; \
    && find build -name "ui.mp.i386.so" -exec cp {} /out/ \;

# ── Windows 64-bit (iortcw Windows) ───────────────────────────────────────────
FROM game-src-windows AS game-win-64
WORKDIR /build/mod/src
RUN --mount=type=cache,target=/build/mod/src/build,id=rtcw-win-64 \
    bjam -a -q toolset=gcc-mingw64 target-os=windows address-model=64 release \
    && mkdir -p /out \
    && find build -name "*.dll" -exec cp {} /out/ \; \
    && find /usr -path '*x86_64-w64-mingw32*' -name 'libstdc++-6.dll' -exec cp {} /out/ \; -quit \
    && find /usr -path '*x86_64-w64-mingw32*' -name 'libgcc_s_seh-1.dll' -exec cp {} /out/ \; -quit \
    && find /usr -path '*x86_64-w64-mingw32*' -name 'libwinpthread-1.dll' -exec cp {} /out/ \; -quit

# ── Windows 32-bit (cgame/ui only — for OG 32-bit Windows RTCW clients) ──────
FROM game-src-windows AS game-win-32
WORKDIR /build/mod/src
RUN --mount=type=cache,target=/build/mod/src/build,id=rtcw-win-32v2 \
    bjam -a toolset=gcc-mingw32 target-os=windows address-model=32 release; \
    mkdir -p /out \
    && find build -name "cgame_mp_x86.dll" -exec cp {} /out/ \; \
    && find build -name "ui_mp_x86.dll" -exec cp {} /out/ \; \
    && find /usr -path '*i686-w64-mingw32*' -name 'libstdc++-6.dll' -exec cp {} /out/ \; -quit \
    && find /usr -path '*i686-w64-mingw32*' -name 'libgcc_s_dw2-1.dll' -exec cp {} /out/ \; -quit \
    && find /usr -path '*i686-w64-mingw32*' -name 'libwinpthread-1.dll' -exec cp {} /out/ \; -quit

# ── omnibot_rtcw.x86_64.so ────────────────────────────────────────────────────
FROM debian:bullseye-slim AS omnibot-lib-builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    cmake make gcc g++ git ca-certificates \
    libboost-dev libboost-system-dev libboost-filesystem-dev \
    libboost-regex-dev libboost-date-time-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY omnibot /build/omnibot
COPY third_party/zlib /build/omnibot/dependencies/physfs/zlib123

RUN --mount=type=cache,target=/tmp/omnibot-build-cache,id=omnibot-lib-cache-v2 \
    rm -f /tmp/omnibot-build-cache/CMakeCache.txt \
    && cmake \
        -B /tmp/omnibot-build-cache \
        -S /build/omnibot \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_C_FLAGS="-m64" \
        -DCMAKE_CXX_FLAGS="-m64" \
        -DCMAKE_SHARED_LINKER_FLAGS="-m64" \
        -DOMNIBOT_ET=OFF \
        -DOMNIBOT_RTCW=ON \
        -DOMNIBOT_STATIC_BOOST=OFF \
    && cmake --build /tmp/omnibot-build-cache --config Release --parallel $(nproc) \
    && mkdir -p /out \
    && find /tmp/omnibot-build-cache -name "omnibot_rtcw*.so" -exec cp {} /out/ \;

# ── s4ndmod26.pk3 (client-facing content pk3) ───────────────────────────────────
# A pk3 is just a zip. Unpack ob_media.pk3 and any other custom assets,
# then repack everything into a single s4ndmod26.pk3 the server serves to clients.
FROM debian:bullseye-slim AS pk3-builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    unzip zip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /pk3-work

# Start with the ob_media content (crosshair shaders, hit sounds, HUD gfx, UI menus)
COPY assets/rtcw/game/ob_media.pk3 /tmp/ob_media.pk3
RUN unzip -q /tmp/ob_media.pk3

# Stamp the version into the pk3 so clients/admins can identify it
COPY assets/rtcw/changelog.txt changelog.txt

# Windows client DLLs — iortcw loads cgame/ui from inside the pk3 on Windows
# qagame is server-side only and intentionally excluded
COPY --from=game-win-64 /out/cgame_mp_x64.dll ./
COPY --from=game-win-64 /out/ui_mp_x64.dll    ./
COPY --from=game-win-32 /out/cgame_mp_x86.dll ./
COPY --from=game-win-32 /out/ui_mp_x86.dll    ./

# Linux client modules
COPY --from=game-linux-64 /out/cgame.mp.x86_64.so ./
COPY --from=game-linux-64 /out/ui.mp.x86_64.so    ./
COPY --from=game-linux-32 /out/cgame.mp.i386.so   ./
COPY --from=game-linux-32 /out/ui.mp.i386.so      ./

# Controller default config — players exec this once to enable controller support
COPY mod/main/controller.cfg ./

# Custom menus (controller settings page, modified controls.menu, updated menus.txt)
COPY mod/main/ui_mp/ ui_mp/

# Repack as s4ndmod26.pk3
RUN mkdir -p /out && zip -rq /out/s4ndmod26.pk3 .

# ── iortcw dedicated server ────────────────────────────────────────────────────
FROM debian:bullseye-slim AS iortcw-builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ make zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

COPY iortcw/ /iortcw/
COPY mod/src/game/g_public.h    /iortcw/code/game/g_public.h
COPY mod/src/game/bg_public.h   /iortcw/code/game/bg_public.h
COPY mod/src/cgame/cg_public.h  /iortcw/code/cgame/cg_public.h
COPY mod/src/ui/ui_public.h     /iortcw/code/ui/ui_public.h
WORKDIR /iortcw
RUN --mount=type=cache,target=/iortcw/build,id=iortcw-server-cache-v2 \
    make \
        BUILD_CLIENT=0 \
        BUILD_GAME_SO=0 \
        BUILD_GAME_QVM=0 \
        BUILD_RENDERER_OPENGL1=0 \
        BUILD_RENDERER_OPENGL2=0 \
        USE_SDL=0 USE_OPENAL=0 USE_CURL=0 \
        USE_CODEC_VORBIS=0 USE_VOIP=0 \
        BUILD_STANDALONE=1 \
        release \
    && mkdir -p /out \
    && cp build/release-linux-x86_64/iowolfded.x86_64 /out/

# ── iortcw client — Linux x86_64 ─────────────────────────────────────────────
FROM debian:bullseye-slim AS iortcw-client-linux-64
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ make \
    libsdl2-dev libopenal-dev libcurl4-openssl-dev \
    libvorbis-dev \
    && rm -rf /var/lib/apt/lists/*

COPY iortcw/ /iortcw/
COPY mod/src/game/g_public.h    /iortcw/code/game/g_public.h
COPY mod/src/game/bg_public.h   /iortcw/code/game/bg_public.h
COPY mod/src/cgame/cg_public.h  /iortcw/code/cgame/cg_public.h
COPY mod/src/ui/ui_public.h     /iortcw/code/ui/ui_public.h
WORKDIR /iortcw
RUN --mount=type=cache,target=/iortcw/build,id=iortcw-client-linux64-cache \
    make \
        BUILD_CLIENT=1 BUILD_SERVER=0 BUILD_GAME_SO=0 BUILD_GAME_QVM=0 \
        BUILD_RENDERER_OPENGL1=1 BUILD_RENDERER_OPENGL2=0 \
        USE_SDL=1 USE_OPENAL=1 USE_CURL=1 \
        USE_CODEC_VORBIS=1 USE_VOIP=1 \
        BUILD_STANDALONE=1 \
        release \
    && mkdir -p /out \
    && cp build/release-linux-x86_64/iowolfmp.x86_64 /out/ \
    && cp build/release-linux-x86_64/renderer_mp_opengl1_x86_64.so /out/

# ── iortcw client — Windows x64 (MinGW cross-compile) ────────────────────────
FROM debian:bullseye-slim AS iortcw-client-windows-64
RUN apt-get update && apt-get install -y --no-install-recommends \
    mingw-w64 make gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

COPY iortcw/ /iortcw/
COPY mod/src/game/g_public.h    /iortcw/code/game/g_public.h
COPY mod/src/game/bg_public.h   /iortcw/code/game/bg_public.h
COPY mod/src/cgame/cg_public.h  /iortcw/code/cgame/cg_public.h
COPY mod/src/ui/ui_public.h     /iortcw/code/ui/ui_public.h
WORKDIR /iortcw
RUN --mount=type=cache,target=/iortcw/build,id=iortcw-client-win64-cache \
    make \
        PLATFORM=mingw64 \
        TOOLS_CC=gcc \
        BUILD_CLIENT=1 BUILD_SERVER=0 BUILD_GAME_SO=0 BUILD_GAME_QVM=0 \
        USE_LOCAL_HEADERS=1 \
        BUILD_RENDERER_OPENGL1=1 BUILD_RENDERER_OPENGL2=0 \
        USE_SDL=1 USE_OPENAL=1 USE_CURL=1 \
        USE_CODEC_VORBIS=1 USE_VOIP=1 \
        BUILD_STANDALONE=1 \
        release \
    && mkdir -p /out \
    && x86_64-w64-mingw32-strip build/release-mingw64-x86_64/ioWolfMP.x64.exe \
    && x86_64-w64-mingw32-strip build/release-mingw64-x86_64/renderer_mp_opengl1_x64.dll \
    && cp build/release-mingw64-x86_64/ioWolfMP.x64.exe /out/ \
    && cp build/release-mingw64-x86_64/renderer_mp_opengl1_x64.dll /out/ \
    && cp code/libs/win64/SDL264.dll /out/ \
    && cp code/libs/win64/OpenAL64.dll /out/

# ── Runtime server image ───────────────────────────────────────────────────────
FROM debian:bullseye-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    wget ca-certificates \
    libboost-filesystem1.74.0 libboost-regex1.74.0 \
    libboost-date-time1.74.0 libboost-system1.74.0 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=iortcw-builder /out/iowolfded.x86_64 /rtcw/iowolfded.x86_64
RUN chmod +x /rtcw/iowolfded.x86_64

# main/ — base game paks only (downloaded below)
RUN mkdir -p /rtcw/main

# s4ndmod26/ — the mod folder.
# sv_pure 1 only checks THIS folder, so players' extra main/ pk3s are irrelevant.
# Clients missing s4ndmod26.pk3 get it via in-band UDP download on connect.
RUN mkdir -p /rtcw/s4ndmod26
COPY --from=game-linux-64 /out/qagame.mp.x86_64.so /rtcw/s4ndmod26/qagame.mp.x86_64.so
COPY --from=game-linux-64 /out/cgame.mp.x86_64.so  /rtcw/s4ndmod26/cgame.mp.x86_64.so
COPY --from=game-linux-64 /out/ui.mp.x86_64.so     /rtcw/s4ndmod26/ui.mp.x86_64.so
COPY --from=pk3-builder /out/s4ndmod26.pk3          /rtcw/s4ndmod26/s4ndmod26.pk3
COPY infra/docker/server.cfg                        /rtcw/s4ndmod26/server.cfg

RUN mkdir -p /rtcw/omni-bot/rtcw/scripts /rtcw/omni-bot/rtcw/nav
COPY --from=omnibot-lib-builder /out/omnibot_rtcw.x86_64.so /rtcw/omni-omnibot_rtcw.x86_64.so
COPY assets/rtcw/scripts/        /rtcw/omni-bot/rtcw/scripts/
COPY assets/rtcw/nav/            /rtcw/omni-bot/rtcw/nav/
COPY assets/rtcw/global_scripts/ /rtcw/omni-bot/global_scripts/

RUN --mount=type=cache,target=/var/cache/rtcw-main,id=rtcw-main-paks \
    set -eu; \
    mkdir -p /var/cache/rtcw-main /rtcw/main; \
    for pak in \
        pak0.pk3 \
        mp_pak0.pk3 \
        mp_pak1.pk3 \
        mp_pak2.pk3 \
        mp_pak3.pk3 \
        mp_pak4.pk3 \
        mp_pak5.pk3; do \
        if [ ! -f "/var/cache/rtcw-main/$pak" ]; then \
            wget -q --show-progress --progress=bar:force:noscroll \
                -O "/var/cache/rtcw-main/$pak" \
                "http://s4ndmod.com/downloads/main/$pak"; \
        fi; \
        cp "/var/cache/rtcw-main/$pak" "/rtcw/main/$pak"; \
    done \
    && apt-get purge -y --auto-remove wget ca-certificates \
    && rm -rf /var/lib/apt/lists/*

EXPOSE 27960/udp
WORKDIR /rtcw
ENTRYPOINT ["./iowolfded.x86_64", \
    "+set", "dedicated", "2", \
    "+set", "fs_basepath", "/rtcw", \
    "+set", "fs_game", "s4ndmod26", \
    "+set", "omnibot_path", "/rtcw/omni-bot", \
    "+exec", "server.cfg"]

# ── Download zips (client + mod pk3, no base paks) ────────────────────────────
FROM debian:bullseye-slim AS zip-builder
RUN apt-get update && apt-get install -y --no-install-recommends zip && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /out /linux/s4ndmod26 /windows/s4ndmod26

COPY --from=iortcw-client-linux-64   /out/iowolfmp.x86_64               /linux/
COPY --from=iortcw-client-linux-64   /out/renderer_mp_opengl1_x86_64.so /linux/
COPY --from=pk3-builder              /out/s4ndmod26.pk3                  /linux/s4ndmod26/
COPY infra/web/start.sh /linux/start.sh
RUN chmod +x /linux/start.sh && cd /linux && zip -r /out/s4ndmod26-linux.zip .

COPY --from=iortcw-client-windows-64 /out/ioWolfMP.x64.exe              /windows/
COPY --from=iortcw-client-windows-64 /out/renderer_mp_opengl1_x64.dll   /windows/
COPY --from=iortcw-client-windows-64 /out/SDL264.dll                    /windows/
COPY --from=iortcw-client-windows-64 /out/OpenAL64.dll                  /windows/
COPY --from=game-win-64              /out/libstdc++-6.dll               /windows/
COPY --from=game-win-64              /out/libgcc_s_seh-1.dll            /windows/
COPY --from=game-win-64              /out/libwinpthread-1.dll           /windows/
COPY --from=pk3-builder              /out/s4ndmod26.pk3                 /windows/s4ndmod26/
COPY infra/web/start.ps1 /windows/start.ps1
RUN cd /windows && zip -r /out/s4ndmod26-windows.zip .

# ── Web frontend (status API + nginx) ─────────────────────────────────────────
FROM golang:1.22-bookworm AS status-api-builder
WORKDIR /src
COPY infra/web/status-api/go.mod ./
RUN go mod download
COPY infra/web/status-api/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/status-api .

FROM nginx:1.27-alpine AS web
COPY infra/web/nginx/nginx.conf /etc/nginx/nginx.conf
ARG CACHE_BUST
RUN echo "$CACHE_BUST" > /dev/null
COPY infra/web/nginx/html/     /usr/share/nginx/html/
COPY infra/web/entrypoint.sh   /entrypoint.sh
COPY --from=status-api-builder /out/status-api /usr/local/bin/status-api
RUN chmod +x /entrypoint.sh /usr/local/bin/status-api

RUN mkdir -p /usr/share/nginx/html/downloads/linux/s4ndmod26 \
             /usr/share/nginx/html/downloads/windows/s4ndmod26 \
             /usr/share/nginx/html/downloads/main \
             /usr/share/nginx/html/downloads/linux32/s4ndmod26 \
             /usr/share/nginx/html/downloads/windows32/s4ndmod26
# iortcw client binaries + renderer
COPY --from=iortcw-client-linux-64   /out/iowolfmp.x86_64               /usr/share/nginx/html/downloads/linux/
COPY --from=iortcw-client-linux-64   /out/renderer_mp_opengl1_x86_64.so /usr/share/nginx/html/downloads/linux/
COPY --from=iortcw-client-windows-64 /out/ioWolfMP.x64.exe              /usr/share/nginx/html/downloads/windows/
COPY --from=iortcw-client-windows-64 /out/renderer_mp_opengl1_x64.dll   /usr/share/nginx/html/downloads/windows/
COPY --from=iortcw-client-windows-64 /out/SDL264.dll                    /usr/share/nginx/html/downloads/windows/
COPY --from=iortcw-client-windows-64 /out/OpenAL64.dll                  /usr/share/nginx/html/downloads/windows/
COPY --from=game-win-64            /out/libstdc++-6.dll                 /usr/share/nginx/html/downloads/windows/
COPY --from=game-win-64            /out/libgcc_s_seh-1.dll              /usr/share/nginx/html/downloads/windows/
COPY --from=game-win-64            /out/libwinpthread-1.dll             /usr/share/nginx/html/downloads/windows/
# Game modules — served here for manual/diagnostic use.
# With sv_pure 1 + STANDALONE build, clients load these from inside s4ndmod26.pk3, not loose files.
COPY --from=game-linux-64 /out/cgame.mp.x86_64.so /usr/share/nginx/html/downloads/linux/s4ndmod26/
COPY --from=game-linux-64 /out/ui.mp.x86_64.so    /usr/share/nginx/html/downloads/linux/s4ndmod26/
COPY --from=game-linux-32 /out/cgame.mp.i386.so   /usr/share/nginx/html/downloads/linux32/s4ndmod26/
COPY --from=game-linux-32 /out/ui.mp.i386.so      /usr/share/nginx/html/downloads/linux32/s4ndmod26/
COPY --from=game-win-64   /out/cgame_mp_x64.dll   /usr/share/nginx/html/downloads/windows/s4ndmod26/
COPY --from=game-win-64   /out/ui_mp_x64.dll      /usr/share/nginx/html/downloads/windows/s4ndmod26/
COPY --from=game-win-32   /out/cgame_mp_x86.dll   /usr/share/nginx/html/downloads/windows32/s4ndmod26/
COPY --from=game-win-32   /out/ui_mp_x86.dll      /usr/share/nginx/html/downloads/windows32/s4ndmod26/
COPY --from=game-win-32   /out/libstdc++-6.dll    /usr/share/nginx/html/downloads/windows32/
COPY --from=game-win-32   /out/libgcc_s_dw2-1.dll /usr/share/nginx/html/downloads/windows32/
COPY --from=game-win-32   /out/libwinpthread-1.dll /usr/share/nginx/html/downloads/windows32/
COPY --from=pk3-builder   /out/s4ndmod26.pk3       /usr/share/nginx/html/downloads/
COPY --from=zip-builder   /out/s4ndmod26-linux.zip   /usr/share/nginx/html/downloads/
COPY --from=zip-builder   /out/s4ndmod26-windows.zip /usr/share/nginx/html/downloads/
# Base game paks — copied from the runtime image so the web server is the single source of truth
COPY --from=runtime /rtcw/main/pak0.pk3    /usr/share/nginx/html/downloads/main/
COPY --from=runtime /rtcw/main/mp_pak0.pk3 /usr/share/nginx/html/downloads/main/
COPY --from=runtime /rtcw/main/mp_pak1.pk3 /usr/share/nginx/html/downloads/main/
COPY --from=runtime /rtcw/main/mp_pak2.pk3 /usr/share/nginx/html/downloads/main/
COPY --from=runtime /rtcw/main/mp_pak3.pk3 /usr/share/nginx/html/downloads/main/
COPY --from=runtime /rtcw/main/mp_pak4.pk3 /usr/share/nginx/html/downloads/main/
COPY --from=runtime /rtcw/main/mp_pak5.pk3 /usr/share/nginx/html/downloads/main/

EXPOSE 80
ENTRYPOINT ["/entrypoint.sh"]

# ── Mod package (all platform binaries + pk3 collected) ───────────────────────
# Build with: docker build --target mod-package --output type=local,dest=./mod-out .
FROM scratch AS mod-package
COPY --from=game-linux-64             /out/ /s4ndmod26/linux/
COPY --from=game-linux-32             /out/ /s4ndmod26/linux/
COPY --from=game-win-64               /out/ /s4ndmod26/windows/
COPY --from=game-win-32               /out/ /s4ndmod26/windows/
COPY --from=pk3-builder               /out/ /s4ndmod26/
COPY --from=iortcw-client-linux-64    /out/ /iortcw-client/linux/
COPY --from=iortcw-client-windows-64  /out/ /iortcw-client/windows/
COPY --from=iortcw-builder            /out/ /iortcw-server/linux/
