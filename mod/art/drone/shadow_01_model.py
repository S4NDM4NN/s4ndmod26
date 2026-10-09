# "Schattendrohne" (Axis drone body), step 1: geometry + two-frame shape keys.
#
# Run inside Blender (Scripting tab, or via the Blender MCP). Builds the "Shadow"
# collection: 1 Blender unit == 1 game unit, +X forward, origin = body centre.
# A flat dark delta/kite hull with two ducted fans, a raised armoured spine plate,
# and a red sensor eye recessed into a pocket in the belly at the nose.
#
# Frame 0 = parked (3D fan blades), frame 1 = spinning (blades collapsed, blur discs).
import bpy, bmesh, math
from mathutils import Matrix, Vector

for n in ("Drone", "Discwing"):
    c = bpy.data.collections.get(n)
    if c:
        c.hide_viewport = True
        c.hide_render = True
coll = bpy.data.collections.get("Shadow") or bpy.data.collections.new("Shadow")
if coll.name not in bpy.context.scene.collection.children:
    bpy.context.scene.collection.children.link(coll)
for o in list(coll.objects):
    bpy.data.objects.remove(o, do_unlink=True)
if bpy.context.object and bpy.context.object.mode != 'OBJECT':
    bpy.ops.object.mode_set(mode='OBJECT')

# planform, counter-clockwise seen from above; mirrored about Y
HALF = [(6.2, 0.0), (1.9, 3.9), (-2.4, 6.1), (-3.3, 4.8), (-3.1, 1.7)]
OUTLINE = HALF + [(-2.2, 0.0)] + [(x, -y) for (x, y) in reversed(HALF[1:])]
PIVOT = (0.4, 0.0)
# (scale about PIVOT, z) from the top plateau down to the belly plateau
RINGS = [(0.80, 0.78), (0.97, 0.30), (1.00, 0.10), (1.00, -0.10), (0.93, -0.35), (0.84, -0.62)]
DUCT_C = [(-0.2, 2.95), (-0.2, -2.95)]
DUCT_R = 1.65           # hole radius through the hull
FAN_Z, DISC_Z = 0.15, 0.20


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


def box(bm, size, M):
    bmesh.ops.create_cube(bm, size=1.0, matrix=M @ Matrix.Diagonal((*size, 1.0)))


def cyl(bm, r1, r2, h, M, seg=12):
    bmesh.ops.create_cone(bm, cap_ends=True, cap_tris=False, segments=seg,
                          radius1=r1, radius2=r2, depth=h, matrix=M)


def revolve(bm, prof, seg, cx=0.0, cy=0.0):
    rings = []
    for r, z in prof:
        rings.append([bm.verts.new((cx + r * math.cos(2 * math.pi * k / seg),
                                    cy + r * math.sin(2 * math.pi * k / seg), z)) for k in range(seg)])
    n = len(rings)
    for i in range(n):
        a, b = rings[i], rings[(i + 1) % n]
        for k in range(seg):
            bm.faces.new((a[k], a[(k + 1) % seg], b[(k + 1) % seg], b[k]))


def loft(bm, outline, pivot, rings):
    """Solid from an outline: rings = [(scale, z)], capped top and bottom."""
    rs = []
    for s, z in rings:
        rs.append([bm.verts.new((pivot[0] + (x - pivot[0]) * s, pivot[1] + (y - pivot[1]) * s, z))
                   for (x, y) in outline])
    n = len(outline)
    for a, b in zip(rs, rs[1:]):
        for k in range(n):
            bm.faces.new((a[k], b[k], b[(k + 1) % n], a[(k + 1) % n]))
    caps = [bm.faces.new(rs[0]), bm.faces.new(rs[-1][::-1])]
    bmesh.ops.triangulate(bm, faces=caps, quad_method='BEAUTY', ngon_method='EAR_CLIP')


M_hull = mat("models/drone/sh_hull", (0.12, 0.12, 0.13), 0.4, 0.6)
M_fan = mat("models/drone/dw_fan", (0.12, 0.12, 0.13), 0.7, 0.4)
M_disc = mat("models/drone/dw_disc", (0.1, 0.1, 0.1))
M_eye = mat("models/drone/dw_led", (1.0, 0.1, 0.05))
P_bronze = mat("sh_m_bronze", (0.35, 0.28, 0.15), 0.8, 0.35)
P_gun = mat("sh_m_gun", (0.12, 0.12, 0.13), 0.8, 0.4)
P_black = mat("sh_m_black", (0.03, 0.03, 0.03), 0.3, 0.5)

# ---------------- hull: loft, then boolean the duct holes + the belly eye pocket ----------------
bm = bmesh.new()
loft(bm, OUTLINE, PIVOT, RINGS)
hull = new_obj("sh_hull", bm, M_hull)

EYE_X0, EYE_X1, EYE_HW = 3.10, 4.50, 0.65         # pocket footprint (x range, half width)
EYE_Z0, EYE_Z1 = -1.00, -0.30                     # cutter floor (open below) .. pocket ceiling
cb = bmesh.new()
for (cx, cy) in DUCT_C:
    cyl(cb, DUCT_R, DUCT_R, 3.0, Matrix.Translation((cx, cy, 0.0)), seg=32)
box(cb, (EYE_X1 - EYE_X0, 2 * EYE_HW, EYE_Z1 - EYE_Z0),
    Matrix.Translation(((EYE_X0 + EYE_X1) / 2, 0, (EYE_Z0 + EYE_Z1) / 2)))
cutter = new_obj("sh_cutter", cb, M_hull)
mod = hull.modifiers.new("cut", 'BOOLEAN')
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
# raised, bevelled spine plate (carries the eagle)
SPINE = [(4.6, 0.0), (3.4, 0.95), (0.0, 0.95), (-0.9, 0.65), (-0.9, -0.65), (0.0, -0.95), (3.4, -0.95)]
loft(bm, SPINE, (1.9, 0.0), [(1.0, 0.70), (0.90, 1.08)])
# duct lips
for (cx, cy) in DUCT_C:
    revolve(bm, [(1.62, -0.50), (1.62, 0.95), (1.80, 1.02), (1.98, 0.93), (2.02, 0.55), (1.98, 0.0)], 32, cx, cy)
# two small sensor blisters on the shoulders
for (x, y) in [(2.2, 1.6), (2.2, -1.6)]:
    cyl(bm, 0.26, 0.22, 0.20, Matrix.Translation((x, y, 0.86)), seg=10)
new_obj("sh_details", bm, M_hull)

# ---------------- fan hubs + stator struts ----------------
bm = bmesh.new()
for (cx, cy) in DUCT_C:
    revolve(bm, [(0.46, -0.30), (0.46, 0.40), (0.34, 0.58), (0.12, 0.68), (0.02, 0.70), (0.02, -0.30)], 16, cx, cy)
new_obj("sh_hub", bm, P_bronze, smooth=True)

bm = bmesh.new()
for (cx, cy) in DUCT_C:
    for k in range(3):
        a = math.radians(90 + 120 * k)
        box(bm, (1.25, 0.10, 0.40), Matrix.Translation((cx, cy, -0.12)) @ Matrix.Rotation(a, 4, 'Z') @ Matrix.Translation((1.05, 0, 0)))
new_obj("sh_stator", bm, P_gun)

# ---------------- antennas (rear of the spine) ----------------
bm = bmesh.new()
bw = bmesh.new()
for (x, y, L) in [(-0.95, 0.40, 2.4), (-0.95, -0.40, 1.9)]:
    cyl(bm, 0.22, 0.18, 0.22, Matrix.Translation((x, y, 0.88)), seg=10)
    cyl(bw, 0.05, 0.02, L, Matrix.Translation((x, y, 0.99 + L / 2)), seg=6)
new_obj("sh_antbase", bm, P_bronze)
new_obj("sh_antwhip", bw, P_black)

# ---------------- belly: eye + bezel frame in the pocket, landing feet ----------------
EYE_C = ((EYE_X0 + EYE_X1) / 2, 0.0, -0.40)
bm = bmesh.new()
bmesh.ops.create_uvsphere(bm, u_segments=16, v_segments=10, radius=0.42,
                          matrix=Matrix.Translation(EYE_C))
new_obj("sh_eye", bm, M_eye, smooth=True)

bm = bmesh.new()
zc = -0.60
for sy in (-1, 1):                                                       # bezel frame round the opening
    box(bm, (EYE_X1 - EYE_X0 + 0.20, 0.10, 0.07), Matrix.Translation(((EYE_X0 + EYE_X1) / 2, sy * (EYE_HW + 0.03), zc)))
for sx in (EYE_X0 - 0.05, EYE_X1 + 0.05):
    box(bm, (0.10, 2 * EYE_HW + 0.20, 0.07), Matrix.Translation((sx, 0, zc)))
new_obj("sh_bezel", bm, P_gun)

bm = bmesh.new()
for (x, y) in [(3.0, 1.7), (3.0, -1.7), (-2.2, 1.2), (-2.2, -1.2)]:
    cyl(bm, 0.20, 0.16, 0.50, Matrix.Translation((x, y, -0.80)), seg=8)
new_obj("sh_feet", bm, P_gun)

# ---------------- rotors (7 twisted blades per duct) + blur discs, two-frame shape keys ----------------
stations = [(0.50, 0.22, 38, 0.04), (0.85, 0.30, 32, 0.04), (1.20, 0.32, 27, 0.04),
            (1.50, 0.28, 22, 0.04), (1.58, 0.22, 18, 0.03)]
NB = 7
bm = bmesh.new()
for (cx, cy) in DUCT_C:
    for b in range(NB):
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
        bmesh.ops.transform(bm, matrix=Matrix.Translation((cx, cy, FAN_Z)) @ Matrix.Rotation(2 * math.pi * b / NB, 4, 'Z'),
                            verts=[v for ring in rings for v in ring])
fan = new_obj("sh_fan", bm, M_fan)

SEG = 32
bm = bmesh.new()
uvl = bm.loops.layers.uv.new("UVMap")
DR_IN, DR_OUT = 0.46, 1.62
for (cx, cy) in DUCT_C:
    inner = [bm.verts.new((cx + DR_IN * math.cos(2 * math.pi * k / SEG), cy + DR_IN * math.sin(2 * math.pi * k / SEG), DISC_Z)) for k in range(SEG)]
    outer = [bm.verts.new((cx + DR_OUT * math.cos(2 * math.pi * k / SEG), cy + DR_OUT * math.sin(2 * math.pi * k / SEG), DISC_Z)) for k in range(SEG)]
    for k in range(SEG):
        f = bm.faces.new((inner[k], outer[k], outer[(k + 1) % SEG], inner[(k + 1) % SEG]))
        for loop in f.loops:
            v = loop.vert.co
            loop[uvl].uv = (0.5 + (v.x - cx) / (2 * DR_OUT), 0.5 + (v.y - cy) / (2 * DR_OUT))   # centred per duct
disc = new_obj("sh_disc", bm, M_disc)


def collapse_key(obj, z):
    obj.shape_key_add(name="Basis", from_mix=False)
    k = obj.shape_key_add(name="collapsed", from_mix=False)
    for i in range(len(obj.data.vertices)):
        k.data[i].co = Vector((0, 0, z))
    return k


k = collapse_key(fan, FAN_Z)                    # frame 0 blades shown, frame 1 collapsed
k.value = 0
k.keyframe_insert("value", frame=0)
k.value = 1
k.keyframe_insert("value", frame=1)
k = collapse_key(disc, DISC_Z)                  # frame 0 discs collapsed, frame 1 shown
k.value = 1
k.keyframe_insert("value", frame=0)
k.value = 0
k.keyframe_insert("value", frame=1)
bpy.context.scene.frame_start = 0
bpy.context.scene.frame_end = 1
bpy.context.scene.frame_set(0)

summary = {o.name: (len(o.data.vertices), len(o.data.polygons)) for o in coll.objects}
