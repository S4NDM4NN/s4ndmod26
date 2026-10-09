# Axis drone body (Schattendrohne), step 3a: paint the hull texture (procedural, numpy only).
#
# Output: mod/main/models/drone/shadow_hull.jpg, 1024 x 2048.
#   top half    = upper hull, planar top-down map (world X right, Y up, +/-RT)
#   bottom half = belly, same projection
# Planform / decal positions match shadow_01_model.py.
import bpy, numpy as np, math

OUT = r"\\wsl.localhost\Ubuntu\home\travis\dev\s4ndmod26-drone\mod\main\models\drone\shadow_hull.jpg"
PREVIEW = r"\\wsl.localhost\Ubuntu\tmp\claude-1000\-home-travis-dev\1da70d37-1aa0-438a-a235-4e69c167914a\scratchpad\shadow_top_prev.png"
RT = 7.0
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
    '-': [".....", ".....", ".....", "#####", ".....", ".....", "....."],
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



# in-game vertex lighting darkens lightingDiffuse surfaces, so paint brighter than it should read
GAIN = 1.9

# ---- geometry shared with shadow_01_model.py --------------------------------
HALF = [(6.2, 0.0), (1.9, 3.9), (-2.4, 6.1), (-3.3, 4.8), (-3.1, 1.7)]
OUTLINE = HALF + [(-2.2, 0.0)] + [(x, -y) for (x, y) in reversed(HALF[1:])]
PIVOT = (0.4, 0.0)
SPINE = [(4.6, 0.0), (3.4, 0.95), (0.0, 0.95), (-0.9, 0.65), (-0.9, -0.65), (0.0, -0.95), (3.4, -0.95)]
DUCTS = [(-0.2, 2.95), (-0.2, -2.95)]


def poly_mask(pts, soft=0.0):
    """1 inside the polygon (world coords), 0 outside (even-odd rule)."""
    inside = np.zeros(X.shape, dtype=bool)
    n = len(pts)
    for i in range(n):
        x0, y0 = pts[i]
        x1, y1 = pts[(i + 1) % n]
        if y0 == y1:
            continue
        cond = (y0 > Y) != (y1 > Y)
        xint = x0 + (Y - y0) * (x1 - x0) / (y1 - y0)
        inside ^= cond & (X < xint)
    return inside.astype(np.float32)


def scaled(pts, s, pivot=PIVOT):
    return [(pivot[0] + (x - pivot[0]) * s, pivot[1] + (y - pivot[1]) * s) for (x, y) in pts]


def seams_and_rivets(col, spacing=1.7, region=None):
    """World-grid panel lines with rivet rows, masked to the hull."""
    seam = np.zeros_like(RR)
    rivet = np.zeros_like(RR)
    shadow = np.zeros_like(RR)
    for axis, other in ((X, Y), (Y, X)):
        d = ((axis + spacing / 2) % spacing) - spacing / 2
        seam = np.maximum(seam, line(d, 0.016))
        for off in (-0.07, 0.07):
            along = ((other + 0.08) % 0.16) - 0.08
            dd = np.hypot(along, d - off)
            near = np.abs(d) < 0.13
            rivet = np.maximum(rivet, ((dd < 0.026) & near).astype(np.float32))
            shadow = np.maximum(shadow, ((dd >= 0.026) & (dd < 0.040) & near).astype(np.float32))
    if region is not None:
        seam, rivet, shadow = seam * region, rivet * region, shadow * region
    col = col * (1 - 0.55 * seam[..., None]) * (1 - 0.35 * shadow[..., None])
    col = lerp(col, (0.36, 0.36, 0.34), rivet * 0.8)
    return col, seam


def weather_dark(col, edge, amount=1.0):
    hi = vnoise(256) * 0.6 + vnoise(128) * 0.4
    chip = np.clip((hi - 0.60) * 8, 0, 1) * np.clip(edge * 1.1 + 0.04, 0, 1) * amount
    bare = np.array([0.40, 0.38, 0.34], dtype=np.float32)[None, None, :] * (0.8 + 0.4 * vnoise(512))[..., None]
    col = col * (1 - 0.85 * chip[..., None]) + bare * (0.85 * chip[..., None])
    patch = vnoise(48) * 0.55 + vnoise(130) * 0.45
    return col, np.clip((patch - 0.56) * 4, 0, 0.7)


def eagle_mask(lx, ly, s):
    """Spread-wing eagle, head toward +lx, centred on the local origin, wingspan ~1.8*s."""
    f, w = lx / s, np.abs(ly) / s
    wing = [(0.20, 0.12), (0.05, 0.45), (-0.15, 0.82), (-0.45, 0.90), (-0.36, 0.70), (-0.58, 0.72),
            (-0.46, 0.52), (-0.64, 0.50), (-0.48, 0.30), (-0.52, 0.12)]
    # polygon test on (f, w): reuse the even-odd test with local arrays
    inside = np.zeros(f.shape, dtype=bool)
    for i in range(len(wing)):
        x0, y0 = wing[i]
        x1, y1 = wing[(i + 1) % len(wing)]
        cond = (y0 > w) != (y1 > w)
        xint = x0 + (w - y0) * (x1 - x0) / (y1 - y0)
        inside ^= cond & (f < xint)
    body = (((f + 0.05) / 0.52) ** 2 + (w / 0.15) ** 2) < 1
    head = ((f - 0.55) ** 2 + w ** 2) < 0.011
    beak = (f > 0.60) & (f < 0.76) & (w < 0.075 * (0.76 - f) / 0.16)
    tail = (f < -0.45) & (f > -0.90) & (w < 0.12 + 0.12 * (-0.45 - f) / 0.45)
    return (inside | body | head | beak | tail).astype(np.float32)


def balkenkreuz(col, cx, cy, half, fade):
    lx, ly = X - cx, Y - cy
    arm_w = half * 0.36

    def cross(h, w):
        return np.maximum(rect(lx, ly, h, w, 0.012), rect(lx, ly, w, h, 0.012))
    col = lerp(col, (0.80, 0.79, 0.74), cross(half * 1.00, arm_w * 1.00) * fade * 0.92)
    col = lerp(col, (0.02, 0.02, 0.02), cross(half * 0.86, arm_w * 0.72) * fade * 0.97)
    return col


# ============================= TOP (upper hull) ==============================
def paint_top():
    base = np.array([0.150, 0.152, 0.158], dtype=np.float32)
    m = fbm(4)
    tint = fbm(3) - 0.5
    col = base[None, None, :] * (0.78 + 0.44 * m)[..., None]
    col[..., 0] *= 1 + 0.20 * tint          # warm / cool mottling
    col[..., 2] *= 1 - 0.20 * tint

    hull = poly_mask(OUTLINE)
    plateau = poly_mask(scaled(OUTLINE, 0.80))
    col, seam = seams_and_rivets(col, 1.7, plateau)

    # sloped band between plateau and rim: a little lighter where the armour edge catches light
    band = hull * (1 - plateau)
    col = lerp(col, (0.17, 0.17, 0.175), band * 0.15)
    ring_seam = np.maximum(line(0, 1) * 0, np.clip(poly_mask(scaled(OUTLINE, 0.815)) - poly_mask(scaled(OUTLINE, 0.79)), 0, 1))
    col = col * (1 - 0.55 * ring_seam[..., None])
    rim = hull - poly_mask(scaled(OUTLINE, 0.965))
    col = lerp(col, (0.25, 0.24, 0.22), np.clip(rim, 0, 1) * 0.6)       # worn rim edge

    # spine plate
    sp = poly_mask(SPINE)
    sp_in = poly_mask(scaled(SPINE, 0.90, (1.9, 0.0)))
    col = lerp(col, (0.21, 0.21, 0.215), sp * 0.9)
    col = lerp(col, (0.30, 0.29, 0.26), np.clip(sp - sp_in, 0, 1) * 0.7)
    plate_noise = (0.9 + 0.2 * vnoise(256))[..., None]
    col = col * np.where(sp[..., None] > 0, plate_noise, 1.0)

    # ducts: raised lip, sooty surround
    for (cx, cy) in DUCTS:
        rr = np.hypot(X - cx, Y - cy)
        col = lerp(col, (0.07, 0.07, 0.075), sstep(2.10, 1.62, rr) * 0.9)
        col = lerp(col, (0.26, 0.26, 0.26), line(rr - 1.95, 0.05) * 0.7)
        col = lerp(col, (0.04, 0.04, 0.045), sstep(1.66, 1.60, rr))
        # intake grille ticks around the lip
        ang = np.arctan2(Y - cy, X - cx)
        tick = (((ang * 24 / (2 * math.pi)) % 1.0) < 0.32).astype(np.float32) * line(rr - 1.82, 0.07)
        col = lerp(col, (0.03, 0.03, 0.035), tick * 0.9)

    # sensor blisters
    for (bx, by) in [(2.2, 1.6), (2.2, -1.6)]:
        rr = np.hypot(X - bx, Y - by)
        col = lerp(col, (0.04, 0.04, 0.045), np.clip((0.30 - rr) / 0.015, 0, 1))
        col = lerp(col, (0.30, 0.30, 0.32), np.clip((0.15 - rr) / 0.015, 0, 1) * 0.7)

    # weathering, soot, then decals
    edge = np.clip(rim * 1.2 + band * 0.4 + seam * 0.3, 0, 1)
    col, wear = weather_dark(col, edge, amount=0.5)
    fade = 1 - wear * 0.8
    col = col * (1 - 0.30 * sstep(2.6, 1.7, np.minimum(np.hypot(X + 0.2, Y - 2.95), np.hypot(X + 0.2, Y + 2.95)))[..., None])

    # eagle on the spine plate (silver), head toward the nose
    em = eagle_mask(X - 2.0, Y, 0.90)
    col = lerp(col, (0.68, 0.66, 0.60), em * np.clip(fade + 0.15, 0, 1) * 0.92)
    # Balkenkreuz on each wing
    for sgn in (1, -1):
        col = balkenkreuz(col, -1.7, sgn * 5.0, 0.48, fade)
    # stencil
    lx, ly = X - 0.45, Y
    sd = text_mask(lx, ly, "SD-1", 0.30)
    col = lerp(col, (0.70, 0.68, 0.62), sd * np.clip(fade + 0.1, 0, 1) * 0.9)

    col = col * (0.96 + 0.08 * vnoise(512))[..., None]
    col = col * (1 - 0.18 * sstep(0.93, 1.0, 1 - poly_mask(scaled(OUTLINE, 0.99))))[..., None]
    return np.clip(col * GAIN, 0, 1)


# ============================= BOTTOM (belly) ================================
def paint_bottom():
    base = np.array([0.105, 0.107, 0.112], dtype=np.float32)
    m = fbm(4)
    col = base[None, None, :] * (0.78 + 0.44 * m)[..., None]
    hull = poly_mask(OUTLINE)
    plateau = poly_mask(scaled(OUTLINE, 0.84))
    col, seam = seams_and_rivets(col, 1.9, plateau)
    col = lerp(col, (0.15, 0.15, 0.155), hull * (1 - plateau) * 0.6)
    rim = hull - poly_mask(scaled(OUTLINE, 0.965))
    col = lerp(col, (0.22, 0.21, 0.20), np.clip(rim, 0, 1) * 0.6)
    for (cx, cy) in DUCTS:                                   # duct walls map here: plain dark
        rr = np.hypot(X - cx, Y - cy)
        col = lerp(col, (0.045, 0.045, 0.05), sstep(1.85, 1.62, rr))
    # eye pocket (floor and walls): dark recess, lighter machined edge
    col = lerp(col, (0.26, 0.26, 0.26), rect(X - 3.8, Y, 0.78, 0.72, 0.02) * 0.8)
    col = lerp(col, (0.03, 0.03, 0.035), rect(X - 3.8, Y, 0.72, 0.66, 0.03))
    # landing-foot scuff marks
    for (fx, fy) in [(3.0, 1.7), (3.0, -1.7), (-2.2, 1.2), (-2.2, -1.2)]:
        rr = np.hypot(X - fx, Y - fy)
        col = lerp(col, (0.04, 0.04, 0.04), np.clip((0.34 - rr) / 0.02, 0, 1) * 0.6)
    edge = np.clip(rim + seam * 0.3, 0, 1)
    col, _ = weather_dark(col, edge, amount=1.2)
    grime = (vnoise(10) * 0.6 + vnoise(40) * 0.4)
    col = col * (1 - 0.40 * np.clip((grime - 0.45) * 2.0, 0, 1)[..., None])
    col = col * (0.96 + 0.08 * vnoise(512))[..., None]
    return np.clip(col * GAIN, 0, 1)


top = paint_top()
bottom = paint_bottom()
full = np.ones((2 * H, W, 4), dtype=np.float32)
full[:H, :, :3] = bottom
full[H:, :, :3] = top

# ---- rim strip: left 18% of the whole texture, u = height on the rim, v = angle (tiles vertically).
# Seams, a panel gap and a worn bottom/top edge; everything is periodic in v.
SW = int(0.18 * W)
ys = np.arange(2 * H, dtype=np.float32)[:, None] / (2 * H)
xs = np.arange(SW, dtype=np.float32)[None, :] / (0.18 * W)          # 0..1 across the strip == height t
ph = 2 * math.pi * ys
per = (0.5 + 0.25 * np.sin(ph * 37 + 1.3) + 0.15 * np.sin(ph * 91 + 0.4) + 0.10 * np.sin(ph * 211 + 2.1))
per2 = 0.5 + 0.5 * np.sin(ph * 53 + xs * 9.0)
strip = np.zeros((2 * H, SW, 3), dtype=np.float32)
strip[:] = np.array([0.150, 0.152, 0.158], dtype=np.float32)
strip *= (0.82 + 0.30 * (0.6 * per + 0.4 * per2))[..., None]
for tb in (0.193, 0.371, 0.514, 0.657):                             # ring boundaries: panel seams
    strip *= (1 - 0.55 * np.exp(-((xs - tb) / 0.012) ** 2))[..., None]
rivets = (np.abs(((ys * 2 * H) % 12.0) - 6.0) < 1.6) & (np.abs(xs - 0.44) < 0.014)
strip[rivets] = (0.36, 0.36, 0.34)
edge = np.clip((np.abs(xs - 0.5) - 0.40) * 10, 0, 1) * (0.5 + 0.5 * per)          # worn top/bottom edges
strip = strip * (1 - 0.7 * edge[..., None]) + np.array([0.26, 0.25, 0.23], dtype=np.float32) * 0.7 * edge[..., None]
strip = strip * np.array([0.0 + 1.0], dtype=np.float32)
full[:, :SW, :3] = np.clip(strip * GAIN, 0, 1)

old = bpy.data.images.get("shadow_hull")
if old:
    bpy.data.images.remove(old)
im = bpy.data.images.new("shadow_hull", W, 2 * H, alpha=False)
im.colorspace_settings.name = 'sRGB'
im.pixels.foreach_set(full.reshape(-1))
im.update()

sc = bpy.context.scene
s = sc.render.image_settings
s.file_format = 'JPEG'
s.color_mode = 'RGB'
s.quality = 92
im.save_render(filepath=OUT, scene=sc)

old = bpy.data.images.get("shadow_hull_prev")
if old:
    bpy.data.images.remove(old)
pv = bpy.data.images.new("shadow_hull_prev", W, H, alpha=False)
pv.colorspace_settings.name = 'sRGB'
pvd = np.ones((H, W, 4), dtype=np.float32)
pvd[..., :3] = top
pv.pixels.foreach_set(pvd.reshape(-1))
pv.update()
s.file_format = 'PNG'
pv.save_render(filepath=PREVIEW, scene=sc)

summary = {"top_mean": [round(float(v), 3) for v in top.mean((0, 1))],
           "bottom_mean": [round(float(v), 3) for v in bottom.mean((0, 1))]}
