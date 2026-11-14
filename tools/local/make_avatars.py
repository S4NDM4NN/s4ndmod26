#!/usr/bin/env python3
"""Makes the profile pictures (infra/web/nginx/html/img/avatars/<class>_<team>.png) from the limbo menu class
portraits in the RTCW demo's demopak0.pk3 (ui_mp/assets/portraits/*.tga: allies soldier/medic/eng/lt, axis *_ger).
Crops each 256x256 portrait below its caption and scales it to 96x96, keeping the transparency.  Pure python.
  tools/local/make_avatars.py /path/to/demopak0.pk3 [outdir]"""
import os, struct, sys, zipfile, zlib

SRC = {'soldier': 'soldier_allies', 'medic': 'medic_allies', 'eng': 'engineer_allies', 'lt': 'lieutenant_allies',
       'soldier_ger': 'soldier_axis', 'medic_ger': 'medic_axis', 'eng_ger': 'engineer_axis', 'LT_ger': 'lieutenant_axis'}
X0, Y0, CROP, OUT = 40, 50, 176, 96   # below the caption text, centred on the head


def tga(b):
    idlen, cmap, kind = b[0], b[1], b[2]
    w, h = struct.unpack('<HH', b[12:16]); bpp, desc = b[16], b[17]
    if cmap or kind not in (2, 10):
        raise ValueError('unsupported TGA')
    bp, off, raw = bpp // 8, 18 + idlen, bytearray()
    if kind == 2:
        raw = b[off:off + w * h * bp]
    else:
        i = off
        while len(raw) < w * h * bp:
            c = b[i]; i += 1; n = (c & 127) + 1
            if c & 128: raw += b[i:i + bp] * n; i += bp
            else: raw += b[i:i + n * bp]; i += n * bp
    rows = []
    for y in range(h):
        row = bytearray()
        for x in range(w):
            o = (y * w + x) * bp
            row += bytes((raw[o + 2], raw[o + 1], raw[o], raw[o + 3] if bp == 4 else 255))
        rows.append(bytes(row))
    if not desc & 0x20: rows.reverse()
    return rows


def scale(rows, x0, y0, size, out):
    f, res = size / out, []
    for oy in range(out):
        row = bytearray()
        for ox in range(out):
            xa, ya = int(x0 + ox * f), int(y0 + oy * f)
            xb, yb = max(xa + 1, int(x0 + (ox + 1) * f)), max(ya + 1, int(y0 + (oy + 1) * f))
            r = g = bl = a = n = 0
            for y in range(ya, yb):
                for x in range(xa, xb):
                    p = rows[y][x * 4:x * 4 + 4]
                    r += p[0] * p[3]; g += p[1] * p[3]; bl += p[2] * p[3]; a += p[3]; n += 1
            row += bytes((r // a, g // a, bl // a, a // n)) if a else b'\0\0\0\0'
        res.append(bytes(row))
    return res


def png(path, w, h, rows):
    def ch(t, d):
        c = struct.pack('>I', len(d)) + t + d
        return c + struct.pack('>I', zlib.crc32(t + d))
    raw = b''.join(b'\0' + r for r in rows)
    open(path, 'wb').write(b'\x89PNG\r\n\x1a\n' + ch(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 6, 0, 0, 0)) + ch(b'IDAT', zlib.compress(raw, 9)) + ch(b'IEND', b''))


if __name__ == '__main__':
    z = zipfile.ZipFile(sys.argv[1])
    out = sys.argv[2] if len(sys.argv) > 2 else os.path.join(os.path.dirname(__file__), '../../infra/web/nginx/html/img/avatars')
    os.makedirs(out, exist_ok=True)
    for src, dst in SRC.items():
        png(os.path.join(out, dst + '.png'), OUT, OUT, scale(tga(z.read('ui_mp/assets/portraits/%s.tga' % src)), X0, Y0, CROP, OUT))
        print(dst)
