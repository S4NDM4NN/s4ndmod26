package replay

import (
	"math"
	"sort"
)

// What the recording holds besides the players: missiles and dropped items are recorded as entities, one sample every 50 ms for as
// long as they exist.  That gives real paths for grenades, rockets and airstrike markers, and the lifetime of a dropped pack.  The
// recording has no "exploded" event, but a missile's last sample is where it blew up (it matches the damage events' blast point), so
// an explosion is derived from that.  What the recording does not say is whose a missile or pack was; Owner below is worked out from
// who was standing there holding the matching weapon, and is -1 when that is not clear.

// Entity types and item ids from the game (entityType_t, and bg_itemlist's IL_* indexes).
const (
	etPlayer  = 1
	etItem    = 2
	etMissile = 3

	ilHealth         = 4
	ilMagicAmmo      = 35
	ilAmmoGrenades   = 50
	ilAmmoGrenadesUS = 51
)

// Weapon numbers (WP_*) a missile or a player carries.
const (
	wpGrenade     = 6
	wpPanzer      = 7
	wpGrenadeUS   = 17
	wpPanzerUS    = 18
	wpAmmo        = 27
	wpArty        = 28
	wpDynamite    = 32
	wpDynamite2   = 33
	wpSmokeTrail  = 38
	wpMortar      = 41
	wpMedkit      = 43
	wpSmokeNade   = 45
	spotterMinMs  = 2000 // an artillery round recorded this long is the spotter's smoke, not a shell (shells only appear as they land)
	ownerReachSq  = 160 * 160
	lifeGapMs     = 150 // a gap this long in an entity's samples means the entity number was reused
	dropExpiryMs  = 29000
	defuseSlopMs  = 600
	strikeNearSq  = 900 * 900
	strikeLookMs  = 25000
	explosionKeep = 1
)

// splashRadius is the blast radius in game units of what a missile does when it stops (g_missile.c, g_weapon.c).  A rocket's is its
// damage, 400, unless the server halves it for non-soldiers.
var splashRadius = map[int32]int32{
	wpGrenade: 250, wpGrenadeUS: 250, wpDynamite: 600, wpDynamite2: 600, wpMortar: 120, wpPanzer: 400, wpPanzerUS: 400,
}

const (
	bombRadius    = 400 // airstrike bombs and artillery shells
	spotterRadius = 50
)

// weaponNames are the equipped-weapon names a card shows.
var weaponNames = map[int32]string{
	1: "Knife", 2: "Luger", 3: "MP-40", 4: "Mauser", 5: "FG42", 6: "Grenade", 7: "Panzerfaust", 8: "Venom", 9: "Flamethrower",
	12: "Knife", 13: "Colt", 14: "Thompson", 15: "Garand", 16: "BAR", 17: "Grenade", 18: "Panzerfaust", 19: "Sniper", 20: "Snooper",
	21: "Venom", 23: "FG42 scope", 24: "BAR", 25: "Sten", 26: "Syringe", 27: "Ammo pack", 28: "Binoculars", 29: "Silenced pistol",
	30: "Akimbo", 32: "Dynamite", 33: "Dynamite", 43: "Medic pack", 44: "Pliers", 45: "Airstrike", 46: "Binoculars",
}

// WeaponName is the name of a WP_* number ("" when unknown).
func WeaponName(w int32) string { return weaponNames[w] }

// Projectile is something thrown or fired that travelled: where it was every 50 ms from when it appeared until it stopped.
type Projectile struct {
	Kind   string  `json:"kind"` // grenade, dynamite, rocket, mortar, marker (the airstrike smoke canister)
	Weapon int32   `json:"w"`
	Slot   int32   `json:"slot"` // who threw or fired it, -1 when not known
	Team   int32   `json:"team"`
	T0     int32   `json:"t0"`
	T1     int32   `json:"t1"`
	Pts    []int32 `json:"p"` // time, x, y, z for each sample
}

// Explosion is where and when a missile blew up, and which living players were inside its blast radius at that moment.  The radius is
// how far the blast reaches, not who was hurt: damage falls off with distance and walls stop it.
type Explosion struct {
	T      int32    `json:"t"`
	Pos    [3]int32 `json:"pos"`
	Radius int32    `json:"r"`
	Kind   string   `json:"kind"` // grenade, dynamite, rocket, mortar, airstrike, artillery, shell (strike of unknown kind), spotter
	Slot   int32    `json:"slot"`
	Team   int32    `json:"team"`
	Inside []int32  `json:"inside"`
}

// Drop is a medic pack or ammo pack somebody dropped, from when it left their hands until somebody took it or it timed out.
type Drop struct {
	Kind  string   `json:"kind"` // medpack, ammo
	Slot  int32    `json:"slot"` // who dropped it, -1 when not known
	Team  int32    `json:"team"`
	T0    int32    `json:"t0"`
	T1    int32    `json:"t1"`
	From  [3]int32 `json:"from"`
	At    [3]int32 `json:"at"`              // where it came to rest
	Fate  string   `json:"fate"`            // "taken", "expired", or "" when the recording ended first
	Known bool     `json:"known,omitempty"` // the owner comes from a pickup event, not a guess
	Death bool     `json:"death,omitempty"` // dropped because its owner died, not handed out on purpose
}

// WeaponChange is a player equipping a weapon.
type WeaponChange struct {
	T int32 `json:"t"`
	W int32 `json:"w"`
}

// PlayerWeapons is what a player had equipped over time (each change; the first is what they started with).
type PlayerWeapons struct {
	Slot    int32          `json:"slot"`
	Changes []WeaponChange `json:"c"`
}

// Entities is everything besides player paths the viewer draws.
type Entities struct {
	Projectiles []Projectile     `json:"projectiles"`
	Explosions  []Explosion      `json:"explosions"`
	Drops       []Drop           `json:"drops"`
	Weapons     []PlayerWeapons  `json:"weapons"`
	WeaponNames map[int32]string `json:"weapon_names"`
}

type entLife struct {
	num        int32
	et, w, mi  int32
	t0, t1     int32
	pts        []int32
	first, end [3]float32
	dropped    bool
}

// BuildEntities extracts projectiles, explosions, dropped packs and each player's weapon timeline from the recording, for the window
// in opt (everything when ToMs is 0).
func BuildEntities(rep *Replay, opt PathOptions) *Entities {
	out := &Entities{Projectiles: []Projectile{}, Explosions: []Explosion{}, Drops: []Drop{}, Weapons: []PlayerWeapons{}, WeaponNames: map[int32]string{}}
	mc := rep.Header.MaxClients
	open := map[int32]*entLife{}
	var lives []*entLife
	lastWeapon := map[int32]int32{}
	weapons := map[int32]*PlayerWeapons{}
	frameIdx := map[int32]int{} // server time -> frame, for who stood where at a moment
	for fi, f := range rep.Frames {
		frameIdx[f.ServerTime] = fi
		for _, s := range f.Samples {
			if s.ClientNum < mc {
				if s.Health > 0 && (s.Team == 1 || s.Team == 2) && opt.wantSlot(s.ClientNum) && s.Weapon > 0 {
					if w, ok := lastWeapon[s.ClientNum]; !ok || w != s.Weapon {
						pw := weapons[s.ClientNum]
						if pw == nil {
							pw = &PlayerWeapons{Slot: s.ClientNum}
							weapons[s.ClientNum] = pw
						}
						pw.Changes = append(pw.Changes, WeaponChange{T: f.ServerTime, W: s.Weapon})
						lastWeapon[s.ClientNum] = s.Weapon
					}
				}
				continue
			}
			if s.EType != etMissile && s.EType != etItem {
				continue
			}
			l := open[s.ClientNum]
			if l != nil && (f.ServerTime-l.t1 > lifeGapMs || l.et != s.EType || l.w != s.Weapon || l.mi != s.ModelIndex) {
				l = nil
			}
			if l == nil {
				l = &entLife{num: s.ClientNum, et: s.EType, w: s.Weapon, mi: s.ModelIndex, t0: f.ServerTime, first: s.Origin, dropped: s.OtherEnt2 != 0}
				open[s.ClientNum] = l
				lives = append(lives, l)
			}
			l.t1 = f.ServerTime
			l.end = s.Origin
			l.pts = append(l.pts, f.ServerTime, int32(s.Origin[0]), int32(s.Origin[1]), int32(s.Origin[2]))
		}
	}
	for _, pw := range weapons {
		out.Weapons = append(out.Weapons, *pw)
		for _, c := range pw.Changes {
			out.WeaponNames[c.W] = weaponNames[c.W]
		}
	}
	sort.Slice(out.Weapons, func(i, j int) bool { return out.Weapons[i].Slot < out.Weapons[j].Slot })

	var lastT int32
	if n := len(rep.Frames); n > 0 {
		lastT = rep.Frames[n-1].ServerTime
	}
	overlaps := func(a, b int32) bool { return opt.ToMs == 0 || (b >= opt.FromMs && a <= opt.ToMs) }

	// who was at `pos` at time t holding one of `wps` (any weapon when empty)
	whoAtClass := func(t int32, pos [3]float32, class int32, wps ...int32) (int32, int32) {
		fi, ok := frameIdx[t]
		if !ok {
			return -1, 0
		}
		best, bestD, team := int32(-1), float64(ownerReachSq), int32(0)
		ambiguous := false
		for _, s := range rep.Frames[fi].Samples {
			if s.ClientNum >= mc || s.Health <= 0 || (s.Team != 1 && s.Team != 2) {
				continue
			}
			if len(wps) > 0 {
				match := class >= 0 && s.PlayerClass == class
				for _, w := range wps {
					if s.Weapon == w {
						match = true
					}
				}
				if !match {
					continue
				}
			}
			dx, dy, dz := float64(s.Origin[0]-pos[0]), float64(s.Origin[1]-pos[1]), float64(s.Origin[2]-pos[2])
			d := dx*dx + dy*dy + dz*dz
			if d >= ownerReachSq {
				continue
			}
			if best >= 0 && math.Abs(d-bestD) < 20*20 {
				ambiguous = true
			}
			if d < bestD || best < 0 {
				best, bestD, team = s.ClientNum, d, s.Team
				ambiguous = false
			}
		}
		if ambiguous {
			return -1, 0
		}
		return best, team
	}
	whoAt := func(t int32, pos [3]float32, wps ...int32) (int32, int32) { return whoAtClass(t, pos, -1, wps...) }
	playerTeam := func(slot int32, t int32) int32 {
		if fi, ok := frameIdx[t]; ok {
			for _, s := range rep.Frames[fi].Samples {
				if s.ClientNum == slot {
					return s.Team
				}
			}
		}
		return 0
	}
	inside := func(t int32, pos [3]int32, r int32) []int32 {
		res := []int32{}
		fi, ok := frameIdx[t]
		if !ok {
			return res
		}
		for _, s := range rep.Frames[fi].Samples {
			if s.ClientNum >= mc || s.Health <= 0 || (s.Team != 1 && s.Team != 2) {
				continue
			}
			dx, dy, dz := float64(s.Origin[0]-float32(pos[0])), float64(s.Origin[1]-float32(pos[1])), float64(s.Origin[2]-float32(pos[2]))
			if dx*dx+dy*dy+dz*dz <= float64(r)*float64(r) {
				res = append(res, s.ClientNum)
			}
		}
		return res
	}
	// strike launches (airstrike / artillery calls), for naming bombs and finding who called them
	var launches []Event
	var defuses []int32
	var pickups, gives, deaths []Event
	for _, ev := range rep.Events {
		switch ev.Type {
		case EventStrikeLaunch:
			launches = append(launches, ev)
		case EventObjectiveDefuse:
			defuses = append(defuses, ev.ServerTime)
		case EventKill, EventTeamkill, EventSuicide, EventTapout:
			deaths = append(deaths, ev)
		case EventMedpackPickup:
			pickups = append(pickups, ev)
		case EventAmmoGive:
			gives = append(gives, ev)
		}
	}
	launchFor := func(t int32, pos [3]int32) *Event {
		var best *Event
		for i := range launches {
			l := &launches[i]
			if l.ServerTime > t || t-l.ServerTime > strikeLookMs {
				continue
			}
			dx, dy := float64(l.Origin[0]-float32(pos[0])), float64(l.Origin[1]-float32(pos[1]))
			if dx*dx+dy*dy > strikeNearSq {
				continue
			}
			if best == nil || l.ServerTime > best.ServerTime {
				best = l
			}
		}
		return best
	}
	dist2 := func(a [3]float32, b [3]int32) float64 {
		dx, dy, dz := float64(a[0]-float32(b[0])), float64(a[1]-float32(b[1])), float64(a[2]-float32(b[2]))
		return dx*dx + dy*dy + dz*dz
	}

	// airstrike canisters (smoke markers): the bombs land a few seconds after one stops
	type marker struct {
		t0, t1, slot, team int32
		at                 [3]int32
	}
	var markers []marker
	for _, l := range lives {
		if l.et == etMissile && l.w == wpSmokeNade {
			slot, team := whoAt(l.t0, l.first, l.w)
			if slot < 0 {
				slot, team = whoAt(l.t0, l.first)
			}
			markers = append(markers, marker{l.t0, l.t1, slot, team, [3]int32{int32(l.end[0]), int32(l.end[1]), int32(l.end[2])}})
		}
	}

	for _, l := range lives {
		if !overlaps(l.t0, l.t1) {
			continue
		}
		end := [3]int32{int32(l.end[0]), int32(l.end[1]), int32(l.end[2])}
		switch l.et {
		case etMissile:
			kind := ""
			switch l.w {
			case wpGrenade, wpGrenadeUS:
				kind = "grenade"
			case wpDynamite, wpDynamite2:
				kind = "dynamite"
			case wpPanzer, wpPanzerUS:
				kind = "rocket"
			case wpMortar:
				kind = "mortar"
			case wpSmokeNade:
				kind = "marker"
			case wpArty:
				// shells are only recorded as they land; a long-lived one is the spotter round's smoke
				kind = "shell"
				if l.t1-l.t0 >= spotterMinMs {
					kind = "spotter"
				}
			}
			if kind == "" {
				continue
			}
			slot, team := int32(-1), int32(0)
			switch kind {
			case "grenade", "dynamite", "rocket", "marker":
				slot, team = whoAt(l.t0, l.first, l.w)
				if slot < 0 {
					slot, team = whoAt(l.t0, l.first) // switched weapon already: whoever is standing right there
				}
			}
			if kind != "shell" && kind != "spotter" && overlaps(l.t0, l.t1) {
				out.Projectiles = append(out.Projectiles, Projectile{Kind: kind, Weapon: l.w, Slot: slot, Team: team, T0: l.t0, T1: l.t1, Pts: l.pts})
			}
			// what it did when it stopped
			r := int32(0)
			ek := kind
			switch kind {
			case "grenade", "dynamite", "rocket", "mortar":
				r = splashRadius[l.w]
				if kind == "dynamite" {
					for _, d := range defuses {
						if d >= l.t1-defuseSlopMs && d <= l.t1+defuseSlopMs {
							r = 0 // taken apart, not blown up
						}
					}
				}
			case "shell":
				r, ek = bombRadius, "shell"
				if ln := launchFor(l.t1, end); ln != nil && ln.Extra == StrikeArtillery {
					ek, slot, team = "artillery", ln.ActorClientNum, playerTeam(ln.ActorClientNum, ln.ServerTime)
				} else {
					// the bombs land while the marker's smoke is still burning, so anywhere in its life (and a little after) will do
					near := float64(strikeNearSq)
					for _, m := range markers {
						dx, dy := float64(m.at[0]-end[0]), float64(m.at[1]-end[1])
						if d := dx*dx + dy*dy; l.t1 >= m.t0 && l.t1 <= m.t1+3000 && d <= near {
							ek, slot, team, near = "airstrike", m.slot, m.team, d
						}
					}
					if ek == "shell" && ln != nil {
						slot, team = ln.ActorClientNum, playerTeam(ln.ActorClientNum, ln.ServerTime)
					}
				}
			case "spotter":
				r = spotterRadius
				if ln := launchFor(l.t1, end); ln != nil {
					slot = ln.ActorClientNum
					team = playerTeam(slot, ln.ServerTime)
				}
			}
			if r > 0 && l.t1 < lastT && opt.wantTime(l.t1) {
				out.Explosions = append(out.Explosions, Explosion{T: l.t1, Pos: end, Radius: r, Kind: ek, Slot: slot, Team: team, Inside: inside(l.t1, end, r)})
			}
		case etItem:
			if !l.dropped {
				continue
			}
			kind := ""
			switch l.mi {
			case ilHealth:
				kind = "medpack"
			case ilMagicAmmo, ilAmmoGrenades, ilAmmoGrenadesUS:
				kind = "ammo"
			}
			if kind == "" {
				continue
			}
			wp := int32(wpAmmo)
			if kind == "medpack" {
				wp = wpMedkit
			}
			class := int32(1) // a medic gives health, a lieutenant ammunition
			if kind == "ammo" {
				class = 3
			}
			slot, team := whoAtClass(l.t0, l.first, class, wp)
			death := false
			for _, ev := range deaths { // a player who dies or taps out drops what they carry: that is who it was, and it is not a support action
				if ev.ServerTime >= l.t0-300 && ev.ServerTime <= l.t0+300 && (ev.TargetClientNum >= 0 || ev.ActorClientNum >= 0) && dist2(ev.Origin, [3]int32{int32(l.first[0]), int32(l.first[1]), int32(l.first[2])}) < 250*250 {
					victim := ev.TargetClientNum
					if victim < 0 {
						victim = ev.ActorClientNum
					}
					slot, team, death = victim, playerTeam(victim, ev.ServerTime), true
					break
				}
			}
			d := Drop{Death: death, Kind: kind, Slot: slot, Team: team, T0: l.t0, T1: l.t1,
				From: [3]int32{int32(l.first[0]), int32(l.first[1]), int32(l.first[2])}, At: end}
			switch {
			case l.t1 >= lastT:
			case l.t1-l.t0 >= dropExpiryMs:
				d.Fate = "expired"
			default:
				d.Fate = "taken"
				// a pickup event names the dropper outright
				evs := pickups
				if kind == "ammo" {
					evs = gives
				}
				for _, ev := range evs {
					if death {
						break
					}
					if ev.ServerTime >= l.t1-150 && ev.ServerTime <= l.t1+150 && dist2(ev.Origin, end) < 120*120 && ev.ActorClientNum >= 0 {
						d.Slot, d.Team, d.Known = ev.ActorClientNum, playerTeam(ev.ActorClientNum, l.t1), true
						break
					}
				}
			}
			out.Drops = append(out.Drops, d)
		}
	}
	sort.Slice(out.Projectiles, func(i, j int) bool { return out.Projectiles[i].T0 < out.Projectiles[j].T0 })
	sort.Slice(out.Explosions, func(i, j int) bool { return out.Explosions[i].T < out.Explosions[j].T })
	sort.Slice(out.Drops, func(i, j int) bool { return out.Drops[i].T0 < out.Drops[j].T0 })
	return out
}
