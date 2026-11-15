# Discwing drone body, step 1: geometry + two-frame shape keys.
#
# Run inside Blender (Scripting tab, or via the Blender MCP). Builds the "Discwing"
# collection: 1 Blender unit == 1 game unit, +X forward, origin = body centre,
# horizontal radius 5.8 (the drone hitbox is +/-6 in g_active.c).
#
# Frame 0 = parked (3D fan blades), frame 1 = spinning (blades collapsed, blur disc).
import bpy, bmesh, math
from mathutils import Matrix, Vector

old = bpy.data.collections.get("Drone")          # the earlier quad model, if present
if old:
    old.hide_viewport = True
    old.hide_render = True
coll = bpy.data.collections.get("Discwing") or bpy.data.collections.new("Discwing")
if coll.name not in bpy.context.scene.collection.children:
    bpy.context.scene.collection.children.link(coll)
for o in list(coll.objects):
    bpy.data.objects.remove(o, do_unlink=True)
if bpy.context.object and bpy.context.object.mode != 'OBJECT':
    bpy.ops.object.mode_set(mode='OBJECT')

R = 5.8
SEG = 32
HSEG = 48       # hull segments

# Hull profile (radius, z) as one closed loop, revolved about Z. UPPER/LOWER are the
# outer skin, used to seat details on the surface.
UPPER = [(3.55, 1.60), (3.80, 1.45), (4.40, 1.15), (5.00, 0.80), (5.45, 0.45),
         (5.66, 0.30), (5.78, 0.14), (5.80, 0.02)]
LOWER = [(5.80, -0.14), (5.74, -0.26), (5.55, -0.45), (5.30, -0.62), (4.60, -0.88),
         (3.90, -1.00), (3.45, -1.02)]
HULL_PROFILE = ([(3.22, 1.50)] + UPPER + LOWER +
                [(3.20, -0.95), (3.12, -0.60), (3.10, 0.0), (3.12, 0.80)])


def interp(pts, r):
    for (r0, z0), (r1, z1) in zip(pts, pts[1:]):
        lo, hi = min(r0, r1), max(r0, r1)
        if lo <= r <= hi:
            t = (r - r0) / (r1 - r0) if r1 != r0 else 0
            return z0 + t * (z1 - z0), (z1 - z0) / (r1 - r0) if r1 != r0 else 0.0
    return pts[-1][1], 0.0


def z_top(r):
    return interp(UPPER, r)


def z_bot(r):
    return interp([(5.80, -0.14)] + LOWER[1:], r)


def mat(name, rgb, metal=0.0, rough=0.5):
    m = bpy.data.materials.get(name) or bpy.data.materials.new(name)
    m.diffuse_color = (*rgb, 1)
    m.use_nodes = True
    b = m.node_tree.nodes.get("Principled BSDF")
    b.inputs["Base Color"].default_value = (*rgb, 1)
    b.inputs["Metallic"].default_value = metal
    b.inputs["Roughness"].default_value = rough
    return m


def new_obj(name, bm, m, smooth=False):
    me = bpy.data.meshes.new(name)
    bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
    bm.to_mesh(me)
    bm.free()
    o = bpy.data.objects.new(name, me)
    o.data.materials.append(m)
    for p in me.polygons:
        p.use_smooth = smooth
    coll.objects.link(o)
    return o


def revolve(bm, prof, seg):
    rings = []
    for r, z in prof:
        rings.append([bm.verts.new((r * math.cos(2 * math.pi * k / seg),
                                    r * math.sin(2 * math.pi * k / seg), z)) for k in range(seg)])
    n = len(rings)
    for i in range(n):
        a, b = rings[i], rings[(i + 1) % n]
        for k in range(seg):
            bm.faces.new((a[k], a[(k + 1) % seg], b[(k + 1) % seg], b[k]))


def box(bm, size, M):
    bmesh.ops.create_cube(bm, size=1.0, matrix=M @ Matrix.Diagonal((*size, 1.0)))


def cyl(bm, r1, r2, h, M, seg=12):
    bmesh.ops.create_cone(bm, cap_ends=True, cap_tris=False, segments=seg,
                          radius1=r1, radius2=r2, depth=h, matrix=M)


def on_hull(x, y, lift=0.0):
    """Matrix seated on the upper hull at (x, y): local X radial-out, Y tangential, Z surface normal."""
    r = math.hypot(x, y)
    th = math.atan2(y, x)
    z, dz = z_top(r)
    a = math.atan(-dz)
    er = Vector((math.cos(th), math.sin(th), 0))
    et = Vector((-math.sin(th), math.cos(th), 0))
    ez = Vector((0, 0, 1))
    tx = er * math.cos(a) - ez * math.sin(a)
    nz = er * math.sin(a) + ez * math.cos(a)
    M = Matrix((tx, et, nz)).transposed().to_4x4()
    M.translation = Vector((x, y, z)) + nz * lift
    return M


# materials: dw_hull / dw_fan / dw_disc / dw_light / dw_led are the exported shaders;
# dw_m_* are per-part colours used only to bake the small-parts atlas.
M_hull = mat("models/drone/dw_hull", (0.28, 0.31, 0.20), 0.3, 0.6)
M_fan = mat("models/drone/dw_fan", (0.12, 0.12, 0.13), 0.7, 0.4)
M_disc = mat("models/drone/dw_disc", (0.1, 0.1, 0.1))
M_light = mat("models/drone/dw_light", (1.0, 0.78, 0.40))
M_led = mat("models/drone/dw_led", (1.0, 0.1, 0.05))
P_bronze = mat("dw_m_bronze", (0.42, 0.30, 0.14), 0.8, 0.35)
P_gun = mat("dw_m_gun", (0.16, 0.17, 0.17), 0.8, 0.4)
P_black = mat("dw_m_black", (0.03, 0.03, 0.03), 0.3, 0.5)
P_glass = mat("dw_m_glass", (0.02, 0.03, 0.04), 0.0, 0.1)

# ---------------- hull ----------------
bm = bmesh.new()
revolve(bm, HULL_PROFILE, HSEG)
hull = new_obj("dw_hull", bm, M_hull)

# Camera bay: a pocket cut into the rim at the nose (+X). Done with a boolean
# difference, evaluated into a fresh mesh (no operators, so no context needed).
BAY_X0, BAY_X1 = 4.90, 6.30          # back wall .. past the rim
BAY_HALF_W = 0.90                    # half-width (Y)
BAY_Z0, BAY_Z1 = -0.38, 0.02         # floor .. ceiling (opens through the rim band + lower bevel)
BAY_ZC = (BAY_Z0 + BAY_Z1) / 2
cb = bmesh.new()
box(cb, (BAY_X1 - BAY_X0, 2 * BAY_HALF_W, BAY_Z1 - BAY_Z0),
    Matrix.Translation(((BAY_X0 + BAY_X1) / 2, 0, BAY_ZC)))
cutter = new_obj("dw_cutter", cb, M_hull)
mod = hull.modifiers.new("bay", 'BOOLEAN')
mod.operation = 'DIFFERENCE'
mod.object = cutter
mod.solver = 'EXACT'
dg = bpy.context.evaluated_depsgraph_get()
cut_mesh = bpy.data.meshes.new_from_object(hull.evaluated_get(dg))
old_mesh = hull.data
hull.modifiers.remove(mod)
hull.data = cut_mesh
bpy.data.meshes.remove(old_mesh)
bpy.data.objects.remove(cutter, do_unlink=True)
for p_ in hull.data.polygons:
    p_.use_smooth = False

# ---------------- hull details (share the hull texture) ----------------
bm = bmesh.new()
for (x, y, sz) in [(4.45, 0, (1.4, 1.1, 0.07)), (-4.45, 0, (1.4, 1.1, 0.07))]:   # hatch plates fore/aft
    box(bm, sz, on_hull(x, y, 0.02))
def polar(r, deg):
    a = math.radians(deg)
    return r * math.cos(a), r * math.sin(a)


# Layout on the top hull (r, theta in degrees; theta 0 = nose). The skin in
# discwing_03_textures.py paints decals at the same spots, so keep them in sync:
#   roundels (4.45, +/-90)   hatches (4.45, 0/180)   US ARMY text (4.85, -35)
#   DANGER text (3.95, 150)  vents (4.55, 35/-145)   blisters (5.15, 20/-125)
#   antennas (3.95, -60/120/-20)   camera bay: nose rim, theta 0
VENTS = [(4.55, 35), (4.55, -145)]
BLISTERS = [(5.15, 20), (5.15, -125)]
ANTENNAS = [(3.95, -60, 3.0), (3.95, 120, 2.3), (3.95, -20, 1.7)]

for (r_, d_) in VENTS:                                                              # intake vents
    Mv = on_hull(*polar(r_, d_), 0.06)
    box(bm, (1.1, 0.7, 0.14), Mv)
    for k in range(5):
        box(bm, (0.08, 0.62, 0.10), Mv @ Matrix.Translation((-0.40 + 0.2 * k, 0, 0.09)))
for (r_, d_) in BLISTERS:                                                           # sensor blisters
    Md = on_hull(*polar(r_, d_), 0.0)
    cyl(bm, 0.34, 0.30, 0.22, Md @ Matrix.Translation((0, 0, 0.08)), seg=10)
    cyl(bm, 0.28, 0.12, 0.14, Md @ Matrix.Translation((0, 0, 0.26)), seg=10)
new_obj("dw_details", bm, M_hull)

# ---------------- central fan: hub + stator vanes ----------------
bm = bmesh.new()
revolve(bm, [(0.95, -0.35), (0.95, 0.45), (0.92, 0.62), (0.78, 0.95), (0.50, 1.20),
             (0.20, 1.33), (0.02, 1.38), (0.02, -0.35)], 20)
new_obj("dw_hub", bm, P_bronze, smooth=True)

bm = bmesh.new()
for k in range(8):
    a = math.radians(22.5 + 45 * k)
    box(bm, (2.2, 0.10, 0.55), Matrix.Rotation(a, 4, 'Z') @ Matrix.Translation((2.0, 0, -0.25)))
new_obj("dw_vanes", bm, P_gun)

# ---------------- antennas ----------------
bm = bmesh.new()
bw = bmesh.new()
for (r_, d_, L) in ANTENNAS:
    Ma = on_hull(*polar(r_, d_), 0.0)
    cyl(bm, 0.30, 0.24, 0.30, Ma @ Matrix.Translation((0, 0, 0.10)), seg=10)
    cyl(bw, 0.06, 0.025, L, Ma @ Matrix.Translation((0, 0, 0.25 + L / 2)), seg=6)
new_obj("dw_antbase", bm, P_bronze)
new_obj("dw_antwhip", bw, P_black)

# ---------------- camera module, seated in the bay (looks along +X) ----------------
CAMX = 0.5 * (BAY_X0 + BAY_X1)
bm = bmesh.new()
# housing ring + barrel, lined up on the bay's centre line
cyl(bm, 0.46, 0.46, 0.22, Matrix.Translation((BAY_X0 + 0.22, 0, BAY_ZC)) @ Matrix.Rotation(math.pi / 2, 4, 'Y'), seg=16)
cyl(bm, 0.33, 0.33, 0.34, Matrix.Translation((BAY_X0 + 0.50, 0, BAY_ZC)) @ Matrix.Rotation(math.pi / 2, 4, 'Y'), seg=16)
cyl(bm, 0.38, 0.38, 0.06, Matrix.Translation((BAY_X0 + 0.70, 0, BAY_ZC)) @ Matrix.Rotation(math.pi / 2, 4, 'Y'), seg=16)  # lens rim
# bezel frame around the opening
box(bm, (0.12, 0.10, BAY_Z1 - BAY_Z0 + 0.14), Matrix.Translation((5.80, -BAY_HALF_W - 0.04, BAY_ZC)))
box(bm, (0.12, 0.10, BAY_Z1 - BAY_Z0 + 0.14), Matrix.Translation((5.80, BAY_HALF_W + 0.04, BAY_ZC)))
box(bm, (0.12, 2 * BAY_HALF_W + 0.18, 0.08), Matrix.Translation((5.80, 0, BAY_Z1 + 0.04)))
box(bm, (0.12, 2 * BAY_HALF_W + 0.18, 0.08), Matrix.Translation((5.74, 0, BAY_Z0 - 0.02)))
new_obj("dw_cam", bm, P_bronze)
bm = bmesh.new()
cyl(bm, 0.27, 0.27, 0.05, Matrix.Translation((BAY_X0 + 0.74, 0, BAY_ZC)) @ Matrix.Rotation(math.pi / 2, 4, 'Y'), seg=16)   # glass
new_obj("dw_lens", bm, P_glass)

# ---------------- belly: pylons, amber landing lights, red LED ----------------
bm = bmesh.new()
for k in range(6):
    a = math.radians(15 + 60 * k)
    r = 4.4
    zb, _ = z_bot(r)
    cyl(bm, 0.16, 0.12, 0.45, Matrix.Translation((r * math.cos(a), r * math.sin(a), zb - 0.18)), seg=8)
new_obj("dw_pylons", bm, P_gun)

bm = bmesh.new()
for k in range(6):
    a = math.radians(30 + 60 * k)
    r = 5.0
    zb, _ = z_bot(r)
    cyl(bm, 0.28, 0.26, 0.10, Matrix.Translation((r * math.cos(a), r * math.sin(a), zb - 0.02)), seg=10)
new_obj("dw_lights", bm, M_light)

bm = bmesh.new()
zb, _ = z_bot(4.2)
box(bm, (0.24, 0.24, 0.10), Matrix.Translation((-4.2, 0, zb - 0.02)))
new_obj("dw_led", bm, M_led)

# ---------------- fan rotor (16 twisted blades) + blur disc, two-frame shape keys ----------------
ZF, ZD = 0.45, 0.50
stations = [(1.00, 0.30, 38, 0.05), (1.50, 0.36, 32, 0.05), (2.00, 0.42, 27, 0.05),
            (2.50, 0.42, 23, 0.05), (2.90, 0.36, 20, 0.05), (3.05, 0.30, 18, 0.04)]
bm = bmesh.new()
for b in range(16):
    rings = []
    for r, c, tw, t in stations:
        th = math.radians(tw)
        pts = [(+0.35 * c, 0.0), (+0.05 * c, +t), (-0.65 * c, 0.0), (+0.05 * c, -t * 0.4)]
        rings.append([bm.verts.new((r, y * math.cos(th) - z * math.sin(th),
                                    y * math.sin(th) + z * math.cos(th))) for y, z in pts])
    for a_, b_ in zip(rings, rings[1:]):
        for i in range(4):
            j = (i + 1) % 4
            bm.faces.new((a_[i], a_[j], b_[j], b_[i]))
    bm.faces.new(rings[-1])
    bm.faces.new(rings[0][::-1])
    bmesh.ops.transform(bm, matrix=Matrix.Translation((0, 0, ZF)) @ Matrix.Rotation(2 * math.pi * b / 16, 4, 'Z'),
                        verts=[v for ring in rings for v in ring])
fan = new_obj("dw_fan", bm, M_fan)

bm = bmesh.new()
DR_IN, DR_OUT = 0.90, 3.08
inner = [bm.verts.new((DR_IN * math.cos(2 * math.pi * k / SEG), DR_IN * math.sin(2 * math.pi * k / SEG), ZD)) for k in range(SEG)]
outer = [bm.verts.new((DR_OUT * math.cos(2 * math.pi * k / SEG), DR_OUT * math.sin(2 * math.pi * k / SEG), ZD)) for k in range(SEG)]
uvl = bm.loops.layers.uv.new("UVMap")
for k in range(SEG):
    f = bm.faces.new((inner[k], outer[k], outer[(k + 1) % SEG], inner[(k + 1) % SEG]))
    for loop in f.loops:
        v = loop.vert.co
        loop[uvl].uv = (0.5 + v.x / (2 * DR_OUT), 0.5 + v.y / (2 * DR_OUT))   # centred, so tcMod rotate pivots on the axis
disc = new_obj("dw_disc", bm, M_disc)


def collapse_key(obj, z):
    obj.shape_key_add(name="Basis", from_mix=False)
    k = obj.shape_key_add(name="collapsed", from_mix=False)
    for i in range(len(obj.data.vertices)):
        k.data[i].co = Vector((0, 0, z))
    return k


k = collapse_key(fan, ZF)                       # frame 0 blades shown, frame 1 collapsed
k.value = 0
k.keyframe_insert("value", frame=0)
k.value = 1
k.keyframe_insert("value", frame=1)
k = collapse_key(disc, ZD)                      # frame 0 disc collapsed, frame 1 shown
k.value = 1
k.keyframe_insert("value", frame=0)
k.value = 0
k.keyframe_insert("value", frame=1)
bpy.context.scene.frame_start = 0
bpy.context.scene.frame_end = 1
bpy.context.scene.frame_set(0)

summary = {o.name: (len(o.data.vertices), len(o.data.polygons)) for o in coll.objects}
