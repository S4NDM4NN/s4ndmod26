# Axis drone body ("Schattendrohne"), step 2: UV layouts.
#   - sh_hull / sh_details: planar top-down projection into the hull texture
#     (upward faces -> top half, everything else -> bottom half).
#   - sh_eye / sh_fan: throwaway UVs. sh_disc keeps its centred per-duct UVs.
#   - small mechanical parts: smart-unwrapped and packed into one shared atlas.
import bpy

coll = bpy.data.collections["Shadow"]
RT = 7.0   # world half-width covered by the top-down hull texture

if bpy.context.object and bpy.context.object.mode != 'OBJECT':
    bpy.ops.object.mode_set(mode='OBJECT')
bpy.context.scene.frame_set(0)


def planar_uv(obj):
    me = obj.data
    uvl = me.uv_layers.active or me.uv_layers.new(name="UVMap")
    for p in me.polygons:
        top = p.normal.z > 0.05
        for l in p.loop_indices:
            co = me.vertices[me.loops[l].vertex_index].co
            u = 0.5 + co.x / (2 * RT)
            v = 0.5 + co.y / (2 * RT)
            uvl.data[l].uv = (u, 0.5 + 0.5 * v) if top else (u, 0.5 * v)


def unwrap(objs, margin):
    for o in bpy.context.view_layer.objects:
        o.select_set(False)
    for o in objs:
        if not o.data.uv_layers:
            o.data.uv_layers.new(name="UVMap")
        o.select_set(True)
    bpy.context.view_layer.objects.active = objs[0]
    bpy.ops.object.mode_set(mode='EDIT')
    bpy.ops.mesh.select_all(action='SELECT')
    bpy.ops.uv.smart_project(angle_limit=1.15, island_margin=margin)


DUCT_C = [(-0.2, 2.95), (-0.2, -2.95)]
PIVOT_X = 0.4
Z_BOT, Z_TOP = -0.62, 0.78        # hull rim height range, mapped across the rim strip


_HALF = [(6.2, 0.0), (1.9, 3.9), (-2.4, 6.1), (-3.3, 4.8), (-3.1, 1.7)]
_OUTLINE = _HALF + [(-2.2, 0.0)] + [(x, -y) for (x, y) in reversed(_HALF[1:])]   # same as shadow_01_model.py
_LEN = [0.0]
for _i in range(len(_OUTLINE)):
    _a, _b = _OUTLINE[_i], _OUTLINE[(_i + 1) % len(_OUTLINE)]
    _LEN.append(_LEN[-1] + ((_b[0] - _a[0]) ** 2 + (_b[1] - _a[1]) ** 2) ** 0.5)


def perimeter_param(x, y):
    """0..1 along the outline from the nose, for the outline point on the ray from the pivot
    through (x, y). The hull rings are scaled copies of the outline about the pivot, so a vertex
    and its ring-mates share this value, and texture length follows real length."""
    import math
    dx, dy = x - PIVOT_X, y
    if abs(dx) < 1e-9 and abs(dy) < 1e-9:
        return 0.0
    best = None
    for i in range(len(_OUTLINE)):
        ax, ay = _OUTLINE[i][0] - PIVOT_X, _OUTLINE[i][1]
        bx, by = _OUTLINE[(i + 1) % len(_OUTLINE)][0] - PIVOT_X, _OUTLINE[(i + 1) % len(_OUTLINE)][1]
        ex, ey = bx - ax, by - ay
        den = dx * ey - dy * ex
        if abs(den) < 1e-12:
            continue
        t = (ax * ey - ay * ex) / den                                              # distance along the ray
        s = (ax * dy - ay * dx) / den                                              # position along the edge
        if t > 0 and -1e-6 <= s <= 1 + 1e-6:
            best = (i, min(max(s, 0.0), 1.0))
            break
    if best is None:
        return 0.0
    i, s = best
    return ((_LEN[i] + s * (_LEN[i + 1] - _LEN[i])) / _LEN[-1]) % 1.0


def rim_strip_uv(obj):
    """Near-vertical rim faces get their own side-on strip (left 18% of the texture, full
    height): u = height on the rim, v = angle round the hull. A face that straddles the
    angle seam is unwrapped locally and the strip texture tiles vertically."""
    import math
    me = obj.data
    uvl = me.uv_layers.active
    n = 0
    for p in me.polygons:
        c = p.center
        if abs(p.normal.z) >= 0.97:                      # flat top/belly plateaus stay planar
            continue
        if min(math.hypot(c.x - dx, c.y - dy) for dx, dy in DUCT_C) < 1.75:
            continue                                   # duct wall
        if 3.0 < c.x < 4.6 and abs(c.y) < 0.75 and c.z < -0.2:
            continue                                   # eye pocket wall
        angs = [perimeter_param(me.vertices[me.loops[l].vertex_index].co.x,
                                me.vertices[me.loops[l].vertex_index].co.y) for l in p.loop_indices]
        if max(angs) - min(angs) > 0.5:
            angs = [a + 1.0 if a < 0 else a for a in angs]
        for l, a in zip(p.loop_indices, angs):
            z = me.vertices[me.loops[l].vertex_index].co.z
            t = min(max((z - Z_BOT) / (Z_TOP - Z_BOT), 0.0), 1.0)
            uvl.data[l].uv = (0.01 + 0.17 * t, 0.5 + a)
        n += 1
    return n


planar_uv(coll.objects["sh_hull"])
rim_faces = rim_strip_uv(coll.objects["sh_hull"])
planar_uv(coll.objects["sh_details"])
unwrap([coll.objects["sh_fan"]], 0.02)
bpy.ops.object.mode_set(mode='OBJECT')
unwrap([coll.objects["sh_eye"]], 0.02)
bpy.ops.object.mode_set(mode='OBJECT')

PARTS = ["sh_hub", "sh_stator", "sh_antbase", "sh_antwhip", "sh_bezel", "sh_feet"]
unwrap([coll.objects[n] for n in PARTS], 0.003)
bpy.ops.uv.select_all(action='SELECT')
bpy.ops.uv.pack_islands(margin=0.006, rotate=False)
bpy.ops.object.mode_set(mode='OBJECT')

summary = {"rim_faces": rim_faces}
