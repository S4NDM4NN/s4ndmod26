# Optional: show the exported textures on the Shadow objects in Blender's viewport
# (the MD3 only stores shader names, so this is purely for looking at the model).
import bpy

D = r"\\wsl.localhost\Ubuntu\home\travis\dev\s4ndmod26-drone\mod\main\models\drone" + "\\"
MAP = {   # material -> texture file
    "models/drone/sh_hull": "shadow_hull.jpg",
    "sh_m_bronze": "shadow_parts.tga", "sh_m_gun": "shadow_parts.tga", "sh_m_black": "shadow_parts.tga",
    "models/drone/dw_fan": "drone_prop.tga",
    "models/drone/dw_led": "drone_led.tga",
}
for mname, fname in MAP.items():
    m = bpy.data.materials.get(mname)
    if not m:
        continue
    for img in [i for i in bpy.data.images if i.filepath.endswith(fname)]:
        img.reload()
    img = next((i for i in bpy.data.images if i.filepath.endswith(fname)), None) or bpy.data.images.load(D + fname)
    m.use_nodes = True
    nt = m.node_tree
    bsdf = nt.nodes.get("Principled BSDF")
    tex = nt.nodes.get("tex") or nt.nodes.new("ShaderNodeTexImage")
    tex.name = "tex"
    tex.image = img
    nt.links.new(tex.outputs["Color"], bsdf.inputs["Base Color"])
summary = "ok"
