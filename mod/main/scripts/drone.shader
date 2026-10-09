// Drone-sim spectator body (models/drone/drone.md3)
//
// The MD3 has two frames: 0 = parked (3D blades visible), 1 = spinning
// (blades collapsed, blur discs visible). The server picks the frame from
// the pilot's throttle. The discs spin via tcMod rotate; their UVs are
// centred on the full 0..1 square so the rotation pivots on the motor.

models/drone/body
{
	{
		map models/drone/drone_body.tga
		rgbGen lightingDiffuse
	}
}

models/drone/prop
{
	{
		map models/drone/drone_prop.tga
		rgbGen lightingDiffuse
	}
}

// rear status LED, pulsing, unlit
models/drone/glow
{
	{
		map models/drone/drone_led.tga
		rgbGen wave sin 0.65 0.35 0 1.5
	}
}

// propeller blur discs: clockwise / counter-clockwise motors
models/drone/disc_cw
{
	cull none
	nopicmip
	{
		map models/drone/drone_disc.tga
		blendFunc blend
		rgbGen lightingDiffuse
		tcMod rotate -1200
	}
}

models/drone/disc_ccw
{
	cull none
	nopicmip
	{
		map models/drone/drone_disc.tga
		blendFunc blend
		rgbGen lightingDiffuse
		tcMod rotate 1200
	}
}
