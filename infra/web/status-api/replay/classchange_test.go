package replay

import "testing"

func TestClassChanges(t *testing.T) {
	ps := newPlayerState()
	alive := func(class int32) *Sample {
		return &Sample{Health: 100, PmType: pmNormal, PlayerClass: class, Weapon: 3}
	}
	dead := func(class int32) *Sample { return &Sample{Health: 0, PmType: pmDead, PlayerClass: class, Weapon: -1} }

	ps.update(alive(0), 1000) // starts as a soldier: not a change
	ps.update(alive(0), 2000)
	ps.update(dead(0), 3000)
	ps.update(dead(1), 4000)  // picked medic while dead: only counts once alive
	ps.update(alive(1), 5000) // respawned as a medic
	ps.update(alive(1), 6000)
	ps.update(dead(1), 7000)
	ps.update(alive(1), 8000) // same class again: no change
	ps.update(dead(1), 9000)
	ps.update(alive(3), 10000) // lieutenant

	if len(ps.classChanges) != 2 {
		t.Fatalf("want 2 class changes, got %+v", ps.classChanges)
	}
	if ps.classChanges[0] != (ClassChange{TimeMs: 5000, Class: 1}) || ps.classChanges[1] != (ClassChange{TimeMs: 10000, Class: 3}) {
		t.Fatalf("wrong changes: %+v", ps.classChanges)
	}
}
