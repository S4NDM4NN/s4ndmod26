#!/usr/bin/env python3
"""Bake a TrueType font into the engine's pre-rendered font files, following
iortcw/code/renderer/tr_font.c (RE_RegisterFont, BUILD_FREETYPE path):
    fonts/fontImage_<size>.dat       (fontInfo_t, 256 glyphs, little endian)
    fonts/fontImage_<page>_<size>.tga (256x256 white + alpha pages)
usage: bakefont.py <ttf> <pointSize> <outdir>      (needs Pillow: pip install pillow)
  e.g. bakefont.py /usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf 28 mod/main
  writes <outdir>/fonts/fontImage_<size>.dat and fontImage_<page>_<size>.tga; add them to the mod pk3 (mod/main/fonts is
  packed by the Dockerfile and tools/local/build_wasm.sh).  The engine keys fonts by point size only, so pick an unused size.
Metrics come from PIL's FreeType, not FT_LOAD_DEFAULT hinting, so glyph bitmaps can differ by a pixel
from what the engine's own baker would produce; the file layout is the same.
"""
import os
import struct
import sys

from PIL import Image, ImageDraw, ImageFont

ttf, size, outdir = sys.argv[1], int(sys.argv[2]), sys.argv[3]
MAX_QPATH = 64
font = ImageFont.truetype(ttf, size, layout_engine=ImageFont.Layout.BASIC)
os.makedirs(os.path.join(outdir, "fonts"), exist_ok=True)

blank = set(range(0, 32)) | set(range(127, 160))   # nothing the HUD draws; saves atlas room


def glyph_for(c):
    ch = chr(c)
    adv = font.getlength(ch)
    if c in blank or c == 32:
        return dict(w=0, h=0, pitch=0, top=0, bottom=0, xskip=int(adv) + 1, bmp=None)
    x0, y0, x1, y1 = font.getbbox(ch, anchor="ls")   # relative to baseline origin, y grows downward
    w, h = x1 - x0, y1 - y0
    if w <= 0 or h <= 0:
        return dict(w=0, h=0, pitch=0, top=0, bottom=0, xskip=int(adv) + 1, bmp=None)
    pitch = (w + 3) & ~3
    img = Image.new("L", (pitch, h), 0)
    ImageDraw.Draw(img).text((-x0, -y0), ch, font=font, fill=255, anchor="ls")
    return dict(w=w, h=h, pitch=pitch, top=-y0 + 1, bottom=(-y1) * 64, xskip=int(adv) + 1, bmp=img)


glyphs = [glyph_for(c) for c in range(256)]
max_h = max(g["h"] for g in glyphs)

pages = []            # list of 256x256 'L' images
page_of = [0] * 256
cur = Image.new("L", (256, 256), 0)
x = y = 0
for c, g in enumerate(glyphs):
    if g["bmp"] is None:
        g.update(s=0.0, t=0.0, s2=0.0, t2=0.0)
        page_of[c] = len(pages)
        continue
    if x + g["pitch"] + 1 >= 255:
        x = 0
        y += max_h + 1
    if y + max_h + 1 >= 255:               # page full, start a new one
        pages.append(cur)
        cur = Image.new("L", (256, 256), 0)
        x = y = 0
    cur.paste(g["bmp"], (x, y))
    g["s"], g["t"] = x / 256.0, y / 256.0
    g["s2"], g["t2"] = g["s"] + g["pitch"] / 256.0, g["t"] + g["h"] / 256.0
    page_of[c] = len(pages)
    x += g["pitch"] + 1
pages.append(cur)

# glyphs without a bitmap (space etc.) just point at the page current when they were reached
for i, pg in enumerate(pages):
    mx = pg.getextrema()[1] or 1
    scale = 255.0 / mx                      # engine normalises each page so its brightest pixel is 255
    a = pg.point(lambda v: min(255, int(v * scale)))
    rgba = Image.merge("RGBA", (Image.new("L", (256, 256), 255),) * 3 + (a,))
    px = rgba.tobytes("raw", "BGRA")
    rows = [px[r * 1024:(r + 1) * 1024] for r in range(256)][::-1]   # TGA is stored bottom-up
    hdr = bytearray(18)
    hdr[2] = 2
    hdr[12:14] = struct.pack("<H", 256)
    hdr[14:16] = struct.pack("<H", 256)
    hdr[16] = 32
    with open(os.path.join(outdir, "fonts", "fontImage_%d_%d.tga" % (i, size)), "wb") as f:
        f.write(bytes(hdr) + b"".join(rows))

dat = bytearray()
for c, g in enumerate(glyphs):
    shader = ("fonts/fontImage_%d_%d.tga" % (page_of[c], size)).encode()
    dat += struct.pack("<7i", g["h"], g["top"], g["bottom"], g["pitch"], g["xskip"], g["pitch"], g["h"])
    dat += struct.pack("<4f", g["s"], g["t"], g["s2"], g["t2"])
    dat += struct.pack("<i", 0)
    dat += shader.ljust(32, b"\0")
dat += struct.pack("<f", 48.0 / size)       # glyphScale = 72/dpi * 48/pointSize, dpi 72
dat += ("fonts/fontImage_%d.dat" % size).encode().ljust(MAX_QPATH, b"\0")
assert len(dat) == 256 * 80 + 4 + MAX_QPATH, len(dat)
with open(os.path.join(outdir, "fonts", "fontImage_%d.dat" % size), "wb") as f:
    f.write(dat)
print("%s @%d: %d page(s), maxHeight=%d, %d bytes .dat" % (os.path.basename(ttf), size, len(pages), max_h, len(dat)))
