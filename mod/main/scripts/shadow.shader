// Drone-sim spectator body, Axis version: the "Schattendrohne" (models/drone/shadow.md3),
// picked with /dronesim red. Same two-frame scheme as discwing.md3: frame 0 = parked
// (3D fan blades), 1 = spinning (blades collapsed, blur discs visible). The blades, blur
// disc and eye reuse models/drone/dw_fan, dw_disc and dw_led from discwing.shader.

// dark armour plating, top (upper half of the texture) and belly (lower half)
models/drone/sh_hull
{
	{
		map models/drone/shadow_hull.jpg
		rgbGen lightingDiffuse
	}
}

// fan hubs, stator struts, antennas, eye bezel, landing feet (one shared atlas)
models/drone/sh_parts
{
	{
		map models/drone/shadow_parts.tga
		rgbGen lightingDiffuse
	}
}
