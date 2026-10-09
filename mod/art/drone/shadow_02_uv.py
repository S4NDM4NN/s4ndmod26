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


planar_uv(coll.objects["sh_hull"])
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

summary = {o.name: len(o.data.uv_layers) for o in coll.objects}
