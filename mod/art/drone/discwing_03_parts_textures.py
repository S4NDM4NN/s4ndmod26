# Discwing drone body, step 3b: small-parts atlas, fan blur disc, light texture.
#
# The parts atlas is painted straight from the UVs (flat colour per part, shaded by
# face normal, plus grain) instead of baking in Cycles.
#   -> mod/main/models/drone/discwing_parts.tga   (512x512)
#   -> mod/main/models/drone/discwing_disc.tga    (128x128 RGBA)
#   -> mod/main/models/drone/discwing_light.tga   (16x16)
import bpy, numpy as np, math

D = r"\\wsl.localhost\Ubuntu\home\travis\dev\s4ndmod26-drone\mod\main\models\drone"
coll = bpy.data.collections["Discwing"]
SZ = 512
rng = np.random.default_rng(1943)

PART_COLOURS = {                    # sRGB, a little lifted: in-game vertex light darkens it
    "dw_hub":     (0.52, 0.38, 0.18),
    "dw_vanes":   (0.22, 0.23, 0.23),
    "dw_antbase": (0.46, 0.34, 0.17),
    "dw_antwhip": (0.06, 0.06, 0.06),
    "dw_cam":     (0.24, 0.25, 0.25),
    "dw_lens":    (0.03, 0.05, 0.08),
    "dw_pylons":  (0.21, 0.22, 0.22),
}

img = np.zeros((SZ, SZ, 3), dtype=np.float32)
paint = np.zeros((SZ, SZ), dtype=bool)

for name, base in PART_COLOURS.items():
    me = coll.objects[name].data
    uv = me.uv_layers.active.data
    base = np.array(base, dtype=np.float32)
    for p in me.polygons:
        pts = [np.array(uv[l].uv) * SZ for l in p.loop_indices]
        lit = 0.78 + 0.22 * (p.normal.z * 0.5 + 0.5)
        col = base * lit
        for i in range(1, len(pts) - 1):
            a, b, c = pts[0], pts[i], pts[i + 1]
            x0, x1 = int(max(min(a[0], b[0], c[0]) - 1, 0)), int(min(max(a[0], b[0], c[0]) + 1, SZ - 1))
            y0, y1 = int(max(min(a[1], b[1], c[1]) - 1, 0)), int(min(max(a[1], b[1], c[1]) + 1, SZ - 1))
            if x1 < x0 or y1 < y0:
                continue
            gx, gy = np.meshgrid(np.arange(x0, x1 + 1) + 0.5, np.arange(y0, y1 + 1) + 0.5)
            d = (b[1] - c[1]) * (a[0] - c[0]) + (c[0] - b[0]) * (a[1] - c[1])
            if abs(d) < 1e-9:
                continue
            l1 = ((b[1] - c[1]) * (gx - c[0]) + (c[0] - b[0]) * (gy - c[1])) / d
            l2 = ((c[1] - a[1]) * (gx - c[0]) + (a[0] - c[0]) * (gy - c[1])) / d
            inside = (l1 >= -0.02) & (l2 >= -0.02) & (l1 + l2 <= 1.02)
            sub = img[y0:y1 + 1, x0:x1 + 1]
            sub[inside] = col
            paint[y0:y1 + 1, x0:x1 + 1] |= inside

# fill the gaps between islands (so mip-mapping doesn't bleed black)
fill = np.array(PART_COLOURS["dw_cam"], dtype=np.float32)
for _ in range(8):
    miss = ~paint
    acc = np.zeros_like(img)
    cnt = np.zeros(paint.shape, dtype=np.float32)
    for dy, dx in ((1, 0), (-1, 0), (0, 1), (0, -1)):
        sh = np.roll(np.roll(img, dy, 0), dx, 1)
        pm = np.roll(np.roll(paint, dy, 0), dx, 1)
        acc += sh * pm[..., None]
        cnt += pm
    grow = miss & (cnt > 0)
    img[grow] = acc[grow] / cnt[grow][..., None]
    paint |= grow
img[~paint] = fill

img *= (1.0 + 0.06 * (rng.random((SZ, SZ, 1), dtype=np.float32) * 2 - 1))     # grain
img = np.clip(img, 0, 1)


def make(name, arr, alpha):
    h, w = arr.shape[:2]
    old = bpy.data.images.get(name)
    if old:
        bpy.data.images.remove(old)
    im = bpy.data.images.new(name, w, h, alpha=alpha)
    im.colorspace_settings.name = 'sRGB'
    if alpha:
        im.alpha_mode = 'STRAIGHT'
    out = np.ones((h, w, 4), dtype=np.float32)
    out[..., :arr.shape[2]] = arr
    im.pixels.foreach_set(out.reshape(-1))
    im.update()
    return im


def save_tga(im, path, rgba):
    sc = bpy.context.scene
    s = sc.render.image_settings
    s.file_format = 'TARGA_RAW'           # uncompressed: the engine's TGA loader is happiest with it
    s.color_mode = 'RGBA' if rgba else 'RGB'
    im.save_render(filepath=path, scene=sc)


save_tga(make("discwing_parts", img, False), D + r"\discwing_parts.tga", False)

# ---- fan blur disc: 16 swirled lobes in a ring (UVs are centred on the disc axis)
N = 128
y, x = np.mgrid[0:N, 0:N].astype(np.float32)
dx, dy = (x + 0.5 - N / 2) / (N / 2), (y + 0.5 - N / 2) / (N / 2)
rn = np.sqrt(dx * dx + dy * dy)
th = np.arctan2(dy, dx)


def sstep(a, b, v):
    t = np.clip((v - a) / (b - a), 0, 1)
    return t * t * (3 - 2 * t)


env = sstep(0.25, 0.38, rn) * (1 - sstep(0.88, 1.0, rn))
lobes = 0.5 + 0.5 * np.cos(16 * (th - 0.9 * rn)) ** 3
a = np.clip(env * (0.16 + 0.40 * lobes) * (0.65 + 0.35 * sstep(0.3, 0.9, rn)), 0, 1)
disc = np.zeros((N, N, 4), dtype=np.float32)
disc[..., 0], disc[..., 1], disc[..., 2], disc[..., 3] = 0.17, 0.17, 0.18, a
save_tga(make("discwing_disc", disc, True), D + r"\discwing_disc.tga", True)

# ---- amber landing-light texture
light = np.zeros((16, 16, 3), dtype=np.float32)
light[...] = (1.0, 0.77, 0.43)
save_tga(make("discwing_light", light, False), D + r"\discwing_light.tga", False)

summary = {"parts_mean": [round(float(v), 3) for v in img.mean((0, 1))], "disc_alpha_max": round(float(a.max()), 3)}
