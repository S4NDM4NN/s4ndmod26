package replay

import "testing"

func dmg(t, from, to, score, mod int32) Event {
	return Event{Type: EventDamage, ServerTime: t, ActorClientNum: from, TargetClientNum: to, Score: score, MeansOfDeath: mod}
}

func kill(t, from, to int32) Event {
	return Event{Type: EventKill, ServerTime: t, ActorClientNum: from, TargetClientNum: to, MeansOfDeath: 17}
}

func engHealth(clients ...int32) map[int32][]hSample {
	m := map[int32][]hSample{}
	for _, c := range clients {
		m[c] = []hSample{{0, 100}}
	}
	return m
}

var teams = map[int32]int32{1: 1, 2: 1, 3: 2, 4: 2, 5: 1, 6: 2}

func TestEngagementsOnlyEnemyHandFiredHits(t *testing.T) {
	ev := []Event{
		dmg(1000, 1, 3, 20, 17),  // axis hits allies: counts
		dmg(1200, 3, 1, 20, 17),  // and back
		dmg(1300, 2, 1, 50, 17),  // team hit: ignored, so 2 is not in the fight
		dmg(1400, 4, 1, 100, 46), // airstrike: ignored, so 4 is not in the fight
		dmg(1500, 1, 1, 10, 17),  // self damage: ignored
		dmg(1600, 1, 3, 15, 17),
	}
	got := computeEngagements(ev, 8, teams, engHealth(1, 2, 3, 4))
	if len(got) != 1 {
		t.Fatalf("want one fight, got %+v", got)
	}
	e := got[0]
	if len(e.Participants) != 2 || e.Participants[0].ClientNum != 1 || e.Participants[1].ClientNum != 3 {
		t.Errorf("participants: %+v", e.Participants)
	}
	if e.HitCount != 3 || e.TotalDamage != 55 || len(e.Hits) != 3 {
		t.Errorf("hits %d damage %d %+v", e.HitCount, e.TotalDamage, e.Hits)
	}
	if e.Participants[0].DamageDealt != 35 || e.Participants[0].DamageTaken != 20 {
		t.Errorf("player 1: %+v", e.Participants[0])
	}
	if e.Classification != "1v1" {
		t.Errorf("class %s", e.Classification)
	}
}

func TestEngagementsSeparateFightsStaySeparate(t *testing.T) {
	// 1 v 3 on one side of the map and 2 v 4 elsewhere at the same time never share a player, so they are two fights
	ev := []Event{
		dmg(1000, 1, 3, 30, 17), dmg(1100, 3, 1, 30, 17),
		dmg(1050, 5, 6, 30, 18), dmg(1150, 6, 5, 30, 18),
	}
	got := computeEngagements(ev, 8, teams, engHealth(1, 3, 5, 6))
	if len(got) != 2 {
		t.Fatalf("want two fights, got %d: %+v", len(got), got)
	}
	// a later hit between 1 and 6 connects them
	ev = append(ev, dmg(1200, 1, 6, 30, 17))
	if got = computeEngagements(ev, 8, teams, engHealth(1, 3, 5, 6)); len(got) != 1 || len(got[0].Participants) != 4 {
		t.Fatalf("want one merged fight of four: %+v", got)
	}
}

func TestEngagementsStaleMemberDoesNotLink(t *testing.T) {
	// player 1 fought 3 early on; ten seconds later 1 is in a new fight with 6.  Not one chained fight.
	ev := []Event{
		dmg(1000, 1, 3, 30, 17), dmg(1100, 3, 1, 30, 17),
		dmg(12000, 1, 6, 30, 17), dmg(12100, 6, 1, 30, 17),
	}
	got := computeEngagements(ev, 8, teams, engHealth(1, 3, 6))
	if len(got) != 2 {
		t.Fatalf("want two fights, got %+v", got)
	}
}

func TestEngagementsIgnoreCorpseHits(t *testing.T) {
	hs := engHealth(1, 3)
	hs[3] = []hSample{{0, 100}, {1150, 0}} // dies at 1150
	ev := []Event{dmg(1000, 1, 3, 60, 17), dmg(1100, 3, 1, 10, 17), dmg(1140, 1, 3, 40, 17), dmg(1300, 1, 3, 50, 34)}
	got := computeEngagements(ev, 8, teams, hs)
	if len(got) != 1 || got[0].HitCount != 3 || got[0].TotalDamage != 110 {
		t.Fatalf("the hit after death is not part of it: %+v", got)
	}
}

func TestAttachEngagementKills(t *testing.T) {
	ev := []Event{dmg(1000, 1, 3, 20, 17), dmg(1100, 3, 1, 20, 17), kill(1200, 1, 3), {Type: EventHeadshot, ServerTime: 1200, ActorClientNum: 1, TargetClientNum: 3}, kill(1210, 1, 6)}
	engs := attachEngagementKills(computeEngagements(ev, 8, teams, engHealth(1, 3)), ev)
	if len(engs) != 1 || len(engs[0].Kills) != 1 || engs[0].Kills[0].Killer != 1 || engs[0].Kills[0].Victim != 3 {
		t.Fatalf("one kill (the headshot bonus is the same death, 6 is not in the fight): %+v", engs)
	}
	// a short scuffle with no kill and little damage is not an engagement
	ev = []Event{dmg(1000, 1, 3, 5, 17), dmg(1100, 3, 1, 5, 17)}
	if engs = attachEngagementKills(computeEngagements(ev, 8, teams, engHealth(1, 3)), ev); len(engs) != 0 {
		t.Errorf("a scratch is not a fight: %+v", engs)
	}
}

func TestEngagementStartHealthIsWhenTheyJoined(t *testing.T) {
	hs := engHealth(1, 3, 4)
	hs[4] = []hSample{{0, 100}, {1000, 30}, {2000, 100}} // 4 is hurt early, healed to full before joining at 2500
	ev := []Event{dmg(1000, 1, 3, 20, 17), dmg(1100, 3, 1, 20, 17), dmg(2500, 4, 1, 20, 17)}
	got := computeEngagements(ev, 8, teams, hs)
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	for _, p := range got[0].Participants {
		if p.ClientNum == 4 && p.StartHealth != 100 {
			t.Errorf("4 joined at full health, not at the fight's start: %+v", p)
		}
	}
}
