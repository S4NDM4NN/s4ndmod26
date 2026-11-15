package replay

import "testing"

// a tiny recording: slot 0 (Axis) throws a grenade at slot 1 (Allies); slot 2 (Allies medic) drops a medpack and slot 1 takes it
func TestBuildEntities(t *testing.T) {
	r := &Replay{Header: ArchiveHeader{MaxClients: 8}}
	player := func(slot, team, weapon, class int32, x float32) Sample {
		return Sample{ClientNum: slot, Health: 100, Team: team, Weapon: weapon, PlayerClass: class, EType: etPlayer, Origin: [3]float32{x, 0, 0}}
	}
	for i := int32(0); i < 100; i++ {
		ms := 1000 + i*50
		f := Frame{ServerTime: ms}
		w := int32(3) // MP-40
		if ms >= 1500 && ms < 2000 {
			w = wpGrenade // slot 0 holds a grenade while throwing it
		}
		f.Samples = append(f.Samples, player(0, 1, w, 0, 0), player(1, 2, 14, 0, 300), player(2, 2, wpMedkit, 1, 1000))
		if ms >= 1600 && ms <= 2000 { // the grenade flies 20 units per sample from the thrower toward slot 1
			f.Samples = append(f.Samples, Sample{ClientNum: 20, EType: etMissile, Weapon: wpGrenade, Origin: [3]float32{float32(ms-1600) * 0.7, 0, 50}})
		}
		if ms >= 1200 && ms <= 2400 { // the medpack dropped by slot 2
			f.Samples = append(f.Samples, Sample{ClientNum: 21, EType: etItem, ModelIndex: ilHealth, OtherEnt2: 1, Origin: [3]float32{1060, 0, 0}})
		}
		r.Frames = append(r.Frames, f)
	}
	r.Events = []Event{{Type: EventMedpackPickup, ServerTime: 2400, ActorClientNum: 2, TargetClientNum: 1, Origin: [3]float32{1060, 0, 0}}}

	e := BuildEntities(r, PathOptions{})
	if len(e.Projectiles) != 1 || e.Projectiles[0].Kind != "grenade" || e.Projectiles[0].Slot != 0 || e.Projectiles[0].Team != 1 {
		t.Fatalf("projectiles: %+v", e.Projectiles)
	}
	if len(e.Projectiles[0].Pts) != 4*9 || e.Projectiles[0].Pts[0] != 1600 {
		t.Fatalf("the grenade's path has %d values, starting at %d", len(e.Projectiles[0].Pts), e.Projectiles[0].Pts[0])
	}
	if len(e.Explosions) != 1 {
		t.Fatalf("explosions: %+v", e.Explosions)
	}
	x := e.Explosions[0]
	if x.Kind != "grenade" || x.Radius != 250 || x.T != 2000 || x.Slot != 0 {
		t.Fatalf("explosion: %+v", x)
	}
	// the grenade stopped at x=280: slot 1 (x=300) is inside the 250 radius, and so is slot 0 (x=0, 280 away: no)
	if len(x.Inside) != 1 || x.Inside[0] != 1 {
		t.Fatalf("players inside the blast: %v, want only slot 1", x.Inside)
	}
	if len(e.Drops) != 1 || e.Drops[0].Kind != "medpack" || e.Drops[0].Slot != 2 || e.Drops[0].Fate != "taken" || !e.Drops[0].Known || e.Drops[0].Death {
		t.Fatalf("drops: %+v", e.Drops)
	}
	// slot 0 equipped the MP-40, then the grenade, then the MP-40 again
	var w0 []WeaponChange
	for _, pw := range e.Weapons {
		if pw.Slot == 0 {
			w0 = pw.Changes
		}
	}
	if len(w0) != 3 || w0[0].W != 3 || w0[1].W != wpGrenade || w0[1].T != 1500 || w0[2].W != 3 {
		t.Fatalf("slot 0's weapons: %+v", w0)
	}
	// slot 2 is a medic throughout, slot 0 a soldier
	cls := map[int32][]ClassAt{}
	for _, pc := range e.Classes {
		cls[pc.Slot] = pc.Changes
	}
	if len(cls[2]) != 1 || cls[2][0].C != 1 || len(cls[0]) != 1 || cls[0][0].C != 0 {
		t.Fatalf("classes: %+v", e.Classes)
	}
	if e.WeaponNames[wpGrenade] != "Grenade" {
		t.Fatalf("weapon names: %v", e.WeaponNames)
	}
	// a window that ends before the grenade appears has none of it
	early := BuildEntities(r, PathOptions{FromMs: 1000, ToMs: 1300})
	if len(early.Projectiles) != 0 || len(early.Explosions) != 0 {
		t.Fatalf("early window: %+v %+v", early.Projectiles, early.Explosions)
	}
}

func TestDynamiteDefusedIsNotAnExplosion(t *testing.T) {
	r := &Replay{Header: ArchiveHeader{MaxClients: 8}}
	for i := int32(0); i < 60; i++ {
		f := Frame{ServerTime: 1000 + i*50, Samples: []Sample{{ClientNum: 0, Health: 100, Team: 1, Weapon: wpDynamite, EType: etPlayer}}}
		if i < 40 {
			f.Samples = append(f.Samples, Sample{ClientNum: 22, EType: etMissile, Weapon: wpDynamite, Origin: [3]float32{10, 0, 0}})
		}
		r.Frames = append(r.Frames, f)
	}
	r.Events = []Event{{Type: EventObjectiveDefuse, ServerTime: 2950, ActorClientNum: 1}}
	if e := BuildEntities(r, PathOptions{}); len(e.Explosions) != 0 || len(e.Projectiles) != 1 {
		t.Fatalf("defused dynamite: %+v / %+v", e.Explosions, e.Projectiles)
	}
	r.Events = nil
	if e := BuildEntities(r, PathOptions{}); len(e.Explosions) != 1 || e.Explosions[0].Radius != 600 {
		t.Fatalf("dynamite that went off: %+v", e.Explosions)
	}
}
