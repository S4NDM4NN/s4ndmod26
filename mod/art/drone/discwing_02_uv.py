# Discwing drone body, step 2: UV layouts.
#   - dw_hull / dw_details: planar top-down projection into the hull texture
#     (upper faces -> top half, lower faces -> bottom half).
#   - small mechanical parts: smart-unwrapped and packed into one shared atlas.
#   - fan / lights / led: throwaway UVs (flat-colour shaders).
import bpy

coll = bpy.data.collections["Discwing"]
RT = 5.95   # world half-width covered by the top-down hull texture

if bpy.context.object and bpy.context.object.mode != 'OBJECT':
    bpy.ops.object.mode_set(mode='OBJECT')
bpy.context.scene.frame_set(0)


def planar_uv(obj, split_by_z=True):
    me = obj.data
    uvl = me.uv_layers.active or me.uv_layers.new(name="UVMap")
    for p in me.polygons:
        n = len(p.loop_indices)
        cs = [me.vertices[me.loops[l].vertex_index].co for l in p.loop_indices]
        zc = sum(c.z for c in cs) / n
        xc = sum(c.x for c in cs) / n
        yc = sum(c.y for c in cs) / n
        # upward-facing faces use the top half; everything else (rim band, duct wall,
        # underside, and the camera bay's walls) uses the bottom half
        top = (p.normal.z > 0.05) and not (xc > 4.75 and abs(yc) < 1.05 and zc < 0.12) if split_by_z else True
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


planar_uv(coll.objects["dw_hull"])
planar_uv(coll.objects["dw_details"], split_by_z=False)

unwrap([coll.objects["dw_fan"]], 0.02)
bpy.ops.object.mode_set(mode='OBJECT')
unwrap([coll.objects["dw_lights"], coll.objects["dw_led"]], 0.02)
bpy.ops.object.mode_set(mode='OBJECT')

PARTS = ["dw_hub", "dw_vanes", "dw_antbase", "dw_antwhip", "dw_cam", "dw_lens", "dw_pylons"]
unwrap([coll.objects[n] for n in PARTS], 0.003)
bpy.ops.uv.select_all(action='SELECT')
bpy.ops.uv.pack_islands(margin=0.006, rotate=False)
bpy.ops.object.mode_set(mode='OBJECT')

summary = {n: len(coll.objects[n].data.uv_layers) for n in
           ["dw_hull", "dw_details", "dw_fan", "dw_lights", "dw_led"] + PARTS}
