# Discwing drone body, step 3a: paint the hull texture (procedural, numpy only).
#
# Output: mod/main/models/drone/discwing_hull.jpg, 1024 x 2048.
#   top half    = upper hull, planar top-down map (world X right, Y up, +/-RT)
#   bottom half = belly, same projection
# Decal positions match the layout comment in discwing_01_model.py.
import bpy, numpy as np, math

OUT = r"\\wsl.localhost\Ubuntu\home\travis\dev\s4ndmod26-drone\mod\main\models\drone\discwing_hull.jpg"
PREVIEW = r"\\wsl.localhost\Ubuntu\tmp\claude-1000\-home-travis-dev\1da70d37-1aa0-438a-a235-4e69c167914a\scratchpad\hull_top_prev.png"
RT = 5.95
W = H = 1024
rng = np.random.default_rng(1943)

yy, xx = np.mgrid[0:H, 0:W].astype(np.float32)
X = (xx + 0.5) / W * 2 * RT - RT
Y = (yy + 0.5) / H * 2 * RT - RT
RR = np.hypot(X, Y)
TH = np.arctan2(Y, X)


def sstep(a, b, v):
    t = np.clip((v - a) / (b - a), 0, 1)
    return t * t * (3 - 2 * t)


def line(d, w):
    return np.exp(-(d / w) ** 2)


def vnoise(gx, gy=None):
    gy = gy or gx
    grid = rng.random((gy + 1, gx + 1)).astype(np.float32)
    tx = np.linspace(0, gx, W, endpoint=False, dtype=np.float32)
    ty = np.linspace(0, gy, H, endpoint=False, dtype=np.float32)
    ix, iy = tx.astype(int), ty.astype(int)
    fx, fy = tx - ix, ty - iy
    fx, fy = fx * fx * (3 - 2 * fx), fy * fy * (3 - 2 * fy)
    a, b = grid[np.ix_(iy, ix)], grid[np.ix_(iy, ix + 1)]
    c, d = grid[np.ix_(iy + 1, ix)], grid[np.ix_(iy + 1, ix + 1)]
    fx, fy = fx[None, :], fy[:, None]
    return (a * (1 - fx) + b * fx) * (1 - fy) + (c * (1 - fx) + d * fx) * fy


def fbm(g0, octs=4):
    out, amp, tot = 0, 1.0, 0
    for i in range(octs):
        out = out + amp * vnoise(g0 * 2 ** i)
        tot += amp
        amp *= 0.5
    return out / tot


def lerp(col, c, m):
    return col * (1 - m[..., None]) + np.array(c, dtype=np.float32)[None, None, :] * m[..., None]


def local(cx, cy, ang):
    dx, dy = X - cx, Y - cy
    ca, sa = math.cos(ang), math.sin(ang)
    return dx * ca + dy * sa, -dx * sa + dy * ca


def rect(lx, ly, hx, hy, soft=0.012):
    return np.clip((hx - np.abs(lx)) / soft, 0, 1) * np.clip((hy - np.abs(ly)) / soft, 0, 1)


def polar(r, deg):
    a = math.radians(deg)
    return r * math.cos(a), r * math.sin(a)


# ---- 5x7 stencil font -------------------------------------------------------
G = {
    'A': [".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"],
    'D': ["####.", "#...#", "#...#", "#...#", "#...#", "#...#", "####."],
    'E': ["#####", "#....", "#....", "####.", "#....", "#....", "#####"],
    'G': [".####", "#....", "#....", "#..##", "#...#", "#...#", ".###."],
    'I': ["#####", "..#..", "..#..", "..#..", "..#..", "..#..", "#####"],
    'J': ["..###", "...#.", "...#.", "...#.", "...#.", "#..#.", ".##.."],
    'K': ["#...#", "#..#.", "#.#..", "##...", "#.#..", "#..#.", "#...#"],
    'M': ["#...#", "##.##", "#.#.#", "#.#.#", "#...#", "#...#", "#...#"],
    'N': ["#...#", "##..#", "#.#.#", "#..##", "#...#", "#...#", "#...#"],
    'R': ["####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"],
    'S': [".####", "#....", "#....", ".###.", "....#", "....#", "####."],
    'T': ["#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."],
    'U': ["#...#", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."],
    'Y': ["#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."],
    '1': ["..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."],
    '3': ["####.", "....#", "....#", ".###.", "....#", "....#", "####."],
    '4': ["...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."],
    '7': ["#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."],
    '.': [".....", ".....", ".....", ".....", ".....", ".##..", ".##.."],
    '*': ["..#..", "#.#.#", ".###.", "#####", ".###.", "#.#.#", "..#.."],
    ' ': ["....."] * 7,
}


def text_mask(lx, ly, text, cap_h):
    """Mask of `text` centred on the local origin; reads along +lx, up is +ly."""
    dot = cap_h / 7.0
    n = len(text)
    bmp = np.zeros((7, n * 6), dtype=bool)
    for i, ch in enumerate(text):
        g = G[ch]
        for r in range(7):
            for c in range(5):
                bmp[r, i * 6 + c] = g[r][c] == '#'
    total_w = n * 6 * dot
    gx = np.floor((lx + total_w / 2) / dot).astype(int)
    gy = np.floor((cap_h / 2 - ly) / dot).astype(int)
    ok = (gx >= 0) & (gx < n * 6) & (gy >= 0) & (gy < 7)
    out = np.zeros(lx.shape, dtype=np.float32)
    out[ok] = bmp[gy[ok], gx[ok]]
    return out


def star_mask(rr, ang, R, soft=0.012):
    """Five-point star of outer radius R, one tip toward angle `ang`."""
    r_in = 0.382 * R
    s36, c36 = math.sin(math.radians(36)), math.cos(math.radians(36))
    d = np.abs(((ang - 0.0 + math.pi / 5) % (2 * math.pi / 5)) - math.pi / 5)      # 0..36 deg from nearest tip
    rho = (r_in * s36 * R) / (r_in * s36 * np.cos(d) + (R - r_in * c36) * np.sin(d))
    return np.clip((rho - rr) / soft, 0, 1)


# ---- shared panel work: seams, rivets, mottling -----------------------------
def panels(col, rings, radial_bands, rivet_rows=True):
    seam = np.zeros_like(RR)
    for r0 in rings:
        seam = np.maximum(seam, line(RR - r0, 0.016))
    perp_for_rivets = []
    for (rmin, rmax, step_deg, off_deg) in radial_bands:
        step = math.radians(step_deg)
        dphi = ((TH - math.radians(off_deg) + step / 2) % step) - step / 2
        dperp = RR * np.sin(dphi)
        band = ((RR > rmin) & (RR < rmax)).astype(np.float32)
        seam = np.maximum(seam, line(dperp, 0.016) * band)
        perp_for_rivets.append((dperp, band))
    col = col * (1 - 0.50 * seam[..., None])

    rivet = np.zeros_like(RR)
    shadow = np.zeros_like(RR)
    if rivet_rows:
        for r0 in rings:
            step = 0.16 / r0
            dth = ((TH + step / 2) % step) - step / 2
            along = dth * r0
            across = np.minimum(np.abs(RR - r0 - 0.07), np.abs(RR - r0 + 0.07))
            d = np.hypot(along, across)
            near = (np.abs(RR - r0) < 0.13)
            rivet = np.maximum(rivet, ((d < 0.027) & near).astype(np.float32))
            shadow = np.maximum(shadow, ((d >= 0.027) & (d < 0.042) & near).astype(np.float32))
        for dperp, band in perp_for_rivets:
            along = ((RR + 0.08) % 0.16) - 0.08
            across = np.minimum(np.abs(dperp - 0.07), np.abs(dperp + 0.07))
            d = np.hypot(along, across)
            rivet = np.maximum(rivet, ((d < 0.027) & (band > 0)).astype(np.float32))
            shadow = np.maximum(shadow, ((d >= 0.027) & (d < 0.042) & (band > 0)).astype(np.float32))
    col = col * (1 - 0.38 * shadow[..., None])
    col = lerp(col, (0.46, 0.47, 0.34), rivet * 0.8)
    return col, seam


def weather(col, edge, amount=1.0):
    hi = vnoise(256) * 0.6 + vnoise(128) * 0.4
    chip = np.clip((hi - 0.60) * 8, 0, 1) * np.clip(edge * 1.1 + 0.04, 0, 1) * amount
    bare = np.array([0.50, 0.47, 0.39], dtype=np.float32)[None, None, :] * (0.8 + 0.4 * vnoise(512))[..., None]
    col = col * (1 - 0.85 * chip[..., None]) + bare * (0.85 * chip[..., None])
    # radial dirt streaks
    ths = np.linspace(-math.pi, math.pi, 181)
    n1 = rng.random(181)
    n1[-1] = n1[0]
    streak = np.interp(TH.reshape(-1), ths, n1).reshape(TH.shape).astype(np.float32)
    streak = np.clip((streak - 0.42) * 2.2, 0, 1) * (0.4 + 0.6 * vnoise(24)) * sstep(3.3, 5.7, RR)
    col = col * (1 - 0.30 * streak[..., None])
    patch = vnoise(48) * 0.55 + vnoise(130) * 0.45
    return col, np.clip((patch - 0.56) * 4, 0, 0.7)


# ============================= TOP (upper hull) ==============================
def paint_top():
    base = np.array([0.255, 0.275, 0.185], dtype=np.float32)
    m = fbm(4)
    tint = fbm(3) - 0.5
    col = base[None, None, :] * (0.80 + 0.40 * m)[..., None]
    col[..., 0] *= 1 + 0.18 * tint
    col[..., 2] *= 1 - 0.18 * tint

    rings = [4.05, 5.10, 5.58]
    bands = [(3.55, 4.05, 60, 30), (4.05, 5.10, 30, 0), (5.10, 5.58, 30, 15)]
    col, seam = panels(col, rings, bands)

    # duct lip + wall, rim band
    col = lerp(col, (0.15, 0.15, 0.16), sstep(3.24, 3.20, RR))
    col = lerp(col, (0.09, 0.09, 0.10), sstep(3.17, 3.13, RR))
    lip = sstep(3.62, 3.55, RR) * sstep(3.18, 3.24, RR)
    col = lerp(col, (0.34, 0.33, 0.27), lip * 0.55)
    col = lerp(col, (0.20, 0.22, 0.15), sstep(5.64, 5.70, RR) * 0.75)
    col = lerp(col, (0.46, 0.43, 0.36), sstep(5.77, 5.80, RR) * 0.9)

    # hatch plates (fore / aft), intake vents, sensor blisters
    for deg in (0, 180):
        cx, cy = polar(4.45, deg)
        lx, ly = local(cx, cy, math.radians(deg))
        border = rect(lx, ly, 0.72, 0.57)
        inner = rect(lx, ly, 0.68, 0.53)
        col = lerp(col, (0.12, 0.12, 0.10), border * 0.9)
        plate = np.array([0.40, 0.38, 0.33], dtype=np.float32)[None, None, :] * (0.85 + 0.30 * vnoise(256))[..., None]
        col = col * (1 - inner[..., None]) + plate * inner[..., None]
        for sx in (-1, 1):
            for sy in (-1, 1):
                rv = np.clip(1 - np.hypot(lx - sx * 0.60, ly - sy * 0.45) / 0.035, 0, 1)
                col = lerp(col, (0.25, 0.25, 0.22), rv)
        col = lerp(col, (0.10, 0.10, 0.09), rect(lx - 0.25, ly, 0.16, 0.05))        # handle slot
    for deg in (35, -145):
        cx, cy = polar(4.55, deg)
        lx, ly = local(cx, cy, math.radians(deg))
        col = lerp(col, (0.04, 0.04, 0.04), rect(lx, ly, 0.58, 0.38))
        for k in range(5):
            col = lerp(col, (0.22, 0.23, 0.18), rect(lx + 0.4 - 0.2 * k, ly, 0.045, 0.32))
    for deg in (20, -125):
        cx, cy = polar(5.15, deg)
        rr = np.hypot(X - cx, Y - cy)
        col = lerp(col, (0.10, 0.11, 0.08), np.clip((0.37 - rr) / 0.015, 0, 1))
        col = lerp(col, (0.26, 0.28, 0.20), np.clip((0.26 - rr) / 0.015, 0, 1) * 0.8)

    # weathering (before decals so paint chips with the panel)
    edge = np.clip(sstep(5.30, 5.75, RR) + sstep(3.80, 3.30, RR) + seam * 0.4, 0, 1)
    col, wear = weather(col, edge)
    col = col * (1 - 0.35 * sstep(3.95, 3.25, RR)[..., None])                         # soot around the duct
    fade = 1 - wear * 0.8

    # --- roundels on the sides: navy disc, white star (tip toward the fan), white bars w/ navy edge
    NAVY, WHITE = (0.06, 0.09, 0.17), (0.86, 0.84, 0.76)
    for sgn in (+1, -1):
        cx, cy = 0.0, sgn * 4.45
        dx, dy = X - cx, Y - cy
        rr = np.hypot(dx, dy)
        tip = math.radians(-90 if sgn > 0 else 90)
        disc = np.clip((0.88 - rr) / 0.012, 0, 1)
        star = star_mask(rr, np.arctan2(dy, dx) - tip + math.pi / 2 * 0 + 0.0, 0.66) * disc
        # star_mask measures angle from +x; rotate so a tip points along `tip`
        star = star_mask(rr, (np.arctan2(dy, dx) - tip), 0.66) * disc
        edge_bar = rect(np.abs(dx) - 1.38, dy, 0.60, 0.25)
        white_bar = rect(np.abs(dx) - 1.38, dy, 0.52, 0.19)
        navy_m = np.maximum(disc, edge_bar)
        col = lerp(col, NAVY, navy_m * fade * 0.95)
        col = lerp(col, WHITE, np.maximum(star, white_bar) * fade * 0.93)

    # --- stencilled markings (up = toward the fan, so it reads from outside the rim)
    TXT = (0.86, 0.84, 0.76)
    th = math.radians(-35)
    cx, cy = polar(4.78, -35)
    ang = th + math.pi / 2
    lx, ly = local(cx, cy, ang)
    m1 = text_mask(lx, ly - 0.0, "U.S. ARMY", 0.44)
    m2 = text_mask(lx, ly + 0.62, "* 4173", 0.44)
    col = lerp(col, TXT, np.maximum(m1, m2) * np.clip(fade + 0.1, 0, 1) * 0.95)

    RED = (0.62, 0.08, 0.06)
    th = math.radians(150)
    cx, cy = polar(3.95, 150)
    ang = th + math.pi / 2
    lx, ly = local(cx, cy, ang)
    d1 = text_mask(lx, ly - 0.36, "DANGER", 0.27)
    d2 = text_mask(lx, ly + 0.44, "JET INTAKE", 0.27)
    tri = ((ly > -0.17) & (ly < 0.12) & (np.abs(lx) < 0.17 * (ly + 0.17) / 0.29)).astype(np.float32)
    col = lerp(col, RED, np.maximum(np.maximum(d1, d2), tri) * np.clip(fade + 0.1, 0, 1) * 0.95)

    # --- final grain + gentle vignette toward the rim
    col = col * (0.96 + 0.08 * vnoise(512))[..., None]
    col = col * (1 - 0.15 * sstep(5.5, 5.9, RR))[..., None]
    return np.clip(col, 0, 1)


# ============================= BOTTOM (belly) ================================
def paint_bottom():
    base = np.array([0.205, 0.22, 0.17], dtype=np.float32)
    m = fbm(4)
    col = base[None, None, :] * (0.78 + 0.44 * m)[..., None]
    rings = [4.20, 4.95]
    bands = [(3.5, 4.20, 45, 10), (4.20, 4.95, 30, 0), (4.95, 5.7, 30, 15)]
    col, seam = panels(col, rings, bands)
    col = lerp(col, (0.12, 0.12, 0.13), sstep(3.30, 3.22, RR))
    col = lerp(col, (0.08, 0.08, 0.09), sstep(3.18, 3.12, RR))
    col = lerp(col, (0.17, 0.19, 0.13), sstep(5.60, 5.70, RR) * 0.8)
    col = lerp(col, (0.44, 0.41, 0.34), sstep(5.76, 5.80, RR) * 0.9)
    edge = np.clip(sstep(5.3, 5.75, RR) + sstep(3.8, 3.3, RR) + seam * 0.4, 0, 1)
    col, _ = weather(col, edge, amount=1.2)
    # camera bay at the nose: dark recess with a lighter machined edge (walls map here)
    bay = rect(X - 5.55, Y, 0.80, 0.93, soft=0.02)
    col = lerp(col, (0.30, 0.29, 0.25), rect(X - 5.55, Y, 0.80, 0.93, soft=0.02) * 0.9)
    col = lerp(col, (0.05, 0.05, 0.05), rect(X - 5.50, Y, 0.72, 0.86, soft=0.03))
    # oil / grime, heavier than on top
    grime = (vnoise(10) * 0.6 + vnoise(40) * 0.4)
    col = col * (1 - 0.35 * np.clip((grime - 0.45) * 2.0, 0, 1)[..., None])
    col = col * (0.96 + 0.08 * vnoise(512))[..., None]
    return np.clip(col, 0, 1)


top = paint_top()
bottom = paint_bottom()
full = np.ones((2 * H, W, 4), dtype=np.float32)
full[:H, :, :3] = bottom
full[H:, :, :3] = top

old = bpy.data.images.get("discwing_hull")
if old:
    bpy.data.images.remove(old)
im = bpy.data.images.new("discwing_hull", W, 2 * H, alpha=False)
im.colorspace_settings.name = 'sRGB'
im.pixels.foreach_set(full.reshape(-1))
im.update()

sc = bpy.context.scene
s = sc.render.image_settings
s.file_format = 'JPEG'
s.color_mode = 'RGB'
s.quality = 92
im.save_render(filepath=OUT, scene=sc)

# preview of the top half (PNG, scratchpad only)
old = bpy.data.images.get("discwing_hull_prev")
if old:
    bpy.data.images.remove(old)
pv = bpy.data.images.new("discwing_hull_prev", W, H, alpha=False)
pv.colorspace_settings.name = 'sRGB'
pvd = np.ones((H, W, 4), dtype=np.float32)
pvd[..., :3] = top
pv.pixels.foreach_set(pvd.reshape(-1))
pv.update()
s.file_format = 'PNG'
pv.save_render(filepath=PREVIEW, scene=sc)

summary = {"top_mean": [round(float(v), 3) for v in top.mean((0, 1))],
           "bottom_mean": [round(float(v), 3) for v in bottom.mean((0, 1))]}
