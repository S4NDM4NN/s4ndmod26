package stats

// modNames is meansOfDeath_t from the game's bg_public.h (the same table the timeline page uses).
var modNames = map[int32]string{
	0: "Unknown", 1: "Shotgun", 2: "Gauntlet", 3: "Machine Gun", 4: "Grenade", 5: "Grenade Splash", 6: "Rocket", 7: "Rocket Splash",
	8: "Railgun", 9: "Lightning", 10: "BFG", 11: "BFG Splash", 12: "Knife", 13: "Knife", 14: "Knife",
	15: "Luger", 16: "Colt", 17: "MP-40", 18: "Thompson", 19: "Sten", 20: "Mauser", 21: "Sniper Rifle", 22: "Garand",
	23: "Snooper Scope", 24: "Silencer", 25: "Akimbo", 26: "BAR", 27: "FG42", 28: "FG42 Scope", 29: "Panzerfaust",
	30: "Rocket Launcher", 31: "Grenade Launcher", 32: "Venom", 33: "Venom", 34: "Flamethrower", 35: "Tesla", 36: "Speargun",
	37: "Speargun", 38: "Grenade", 39: "Cross", 40: "Mortar", 41: "Mortar", 42: "Kicked", 43: "Grabber", 44: "Dynamite",
	45: "Dynamite", 46: "Airstrike", 47: "Syringe", 48: "Ammo", 49: "Artillery", 50: "Water", 51: "Slime", 52: "Lava",
	53: "Crush", 54: "Telefrag", 55: "Falling", 56: "Suicide", 57: "Target Laser", 58: "Trigger Hurt", 59: "Grapple",
	60: "Explosive", 61: "Poison Gas", 62: "Zombie Spit", 63: "Zombie Spit", 64: "Zombie Spirit", 65: "Zombie Spirit",
	66: "Loper Leap", 67: "Loper Ground", 68: "Loper Hit", 69: "LT Ammo", 70: "LT Airstrike", 71: "Engineer", 72: "Medic",
	73: "Bat", 74: "Thrown Knife", 75: "Goomba", 76: "Poison",
}

// ModName is the weapon / cause of death for a meansOfDeath value ("" when unknown).
func ModName(mod int32) string {
	return modNames[mod]
}
