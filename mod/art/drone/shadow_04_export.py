# Axis drone body, step 4: export mod/main/models/drone/shadow.md3 (2 frames).
#
# Needs the "Export ID3 engine MD3" add-on (export_scene.id3_md3). The small
# mechanical parts are exported from temporary duplicates sharing ONE material,
# models/drone/sh_parts; the originals keep their per-part materials.
import bpy

OUT = r"\\wsl.localhost\Ubuntu\home\travis\dev\s4ndmod26-drone\mod\main\models\drone\shadow.md3"
coll = bpy.data.collections["Shadow"]
sc = bpy.context.scene

PARTS = ["sh_hub", "sh_stator", "sh_antbase", "sh_antwhip", "sh_bezel", "sh_feet"]
DIRECT = ["sh_hull", "sh_details", "sh_fan", "sh_disc", "sh_eye"]   # already carry exported materials

pm = bpy.data.materials.get("models/drone/sh_parts") or bpy.data.materials.new("models/drone/sh_parts")
pm.use_nodes = True

if bpy.context.object and bpy.context.object.mode != 'OBJECT':
    bpy.ops.object.mode_set(mode='OBJECT')
for o in bpy.context.view_layer.objects:
    o.select_set(False)

dups = []
for n in PARTS:
    o = coll.objects[n]
    d = o.copy()
    d.data = o.data.copy()
    d.name = "EXPORT_" + n
    d.data.materials.clear()
    d.data.materials.append(pm)
    coll.objects.link(d)
    d.select_set(True)
    dups.append(d)
for n in DIRECT:
    coll.objects[n].select_set(True)
bpy.context.view_layer.objects.active = dups[0]

sc.frame_set(0)
try:
    r = bpy.ops.export_scene.id3_md3(filepath=OUT, only_selected=True, preset='MATERIALS',
                                     limits='LEGACY', start_frame=0, end_frame=1)
    summary = {"op": list(r)}
finally:
    for d in dups:
        me = d.data
        bpy.data.objects.remove(d, do_unlink=True)
        bpy.data.meshes.remove(me)
    sc.frame_set(0)
