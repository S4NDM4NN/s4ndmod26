// Drone-sim spectator body: the "Discwing" saucer (models/drone/discwing.md3)
//
// The MD3 has two frames: 0 = parked (3D fan blades visible), 1 = spinning (blades
// collapsed, blur disc visible). The server picks the frame from the pilot's
// throttle. The disc's UVs are centred on the full 0..1 square, so tcMod rotate
// pivots on the fan axis.

// riveted olive-drab hull, top (upper half of the texture) and belly (lower half)
models/drone/dw_hull
{
	{
		map models/drone/discwing_hull.jpg
		rgbGen lightingDiffuse
	}
}

// hub, stator vanes, antennas, camera module, belly pylons (one shared atlas)
models/drone/dw_parts
{
	{
		map models/drone/discwing_parts.tga
		rgbGen lightingDiffuse
	}
}

// rotor blades (only visible on the parked frame)
models/drone/dw_fan
{
	{
		map models/drone/drone_prop.tga
		rgbGen lightingDiffuse
	}
}

// fan blur (only visible on the spinning frame)
models/drone/dw_disc
{
	cull none
	nopicmip
	{
		map models/drone/discwing_disc.tga
		blendFunc blend
		rgbGen lightingDiffuse
		tcMod rotate -540
	}
}

// belly landing lights, unlit
models/drone/dw_light
{
	{
		map models/drone/discwing_light.tga
		rgbGen identity
	}
}

// rear status LED, pulsing, unlit
models/drone/dw_led
{
	{
		map models/drone/drone_led.tga
		rgbGen wave sin 0.65 0.35 0 1.5
	}
}
