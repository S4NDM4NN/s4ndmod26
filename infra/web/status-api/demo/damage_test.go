package demo

import (
	"testing"

	"s4ndmod26/status-api/replay"
)

// Health drops seen in a merged replay, compared with the scoreboard's
// damage-received column.  The scoreboard counts enemy damage only, the drops
// also include team damage and falls, so they are expected to be about equal
// or a little higher where health is measured exactly.
func TestDamageReceivedVsScoreboard(t *testing.T) {
	root := sampleRoot(t)
	groups := []struct {
		name               string
		subs               []string
		minRatio, maxRatio float64
	}{
		{"dm_60 round 1, six POVs", []string{"2003-08-15_145952", "2003-08-15_145959", "2003-08-15_150025",
			"2003-08-15_150048", "2003-08-15_150102", "2003-08-15_150139"}, 0.80, 1.10},
		// every player recorded, so health is exact throughout
		{"dm_59 set 1, twelve POVs and the overview", []string{"-191902-", "-191904-", "-191906-",
			"-191911-", "-191924-", "wsw_mtw"}, 0.90, 1.05},
	}
	for _, g := range groups {
		ds := loadGroup(t, root, g.subs)
		_, mr, err := MergeDemos(ds)
		if err != nil {
			t.Fatal(err)
		}
		var b *board
		var start int
		for _, d := range ds {
			ph := phases(d)
			if ph[0].State == gsPlaying || ph[0].State == gsUnknown {
				continue
			}
			for _, bd := range scoreboards(d) {
				if s := mapStart(ph, bd.time); s >= 0 && bd.time-s >= minRoundMs && b == nil {
					bd := bd
					b, start = &bd, s
				}
			}
		}
		if b == nil {
			t.Fatalf("%s: no scoreboard", g.name)
		}
		est := map[int]int{}
		exact := map[int]int{}
		for _, dr := range mr.Health.Drops {
			if int(dr.Time) > start && int(dr.Time) <= b.time {
				est[int(dr.Victim)] += int(dr.Amount)
				if dr.Exact {
					exact[int(dr.Victim)] += int(dr.Amount)
				}
			}
		}
		t.Logf("== %s: health known=%d guessed=%d, board@%d", g.name, mr.Health.KnownSamples, mr.Health.GuessedSamples, b.time)
		info := Describe(ds[0])
		var gtTot, estTot int
		for n := 0; n < 64; n++ {
			w, ok := b.damage[n]
			if !ok {
				continue
			}
			t.Logf("   %-18s DR(game)=%5d  drops=%5d (exact part %5d)", info.Players[n].Name, w.received, est[n], exact[n])
			gtTot += w.received
			estTot += est[n]
		}
		ratio := float64(estTot) / float64(gtTot)
		t.Logf("   total DR(game)=%d drops=%d ratio=%.2f", gtTot, estTot, ratio)
		if ratio < g.minRatio || ratio > g.maxRatio {
			t.Errorf("%s: health drops are %.2f of the scoreboard's damage received, want %.2f-%.2f", g.name, ratio, g.minRatio, g.maxRatio)
		}
		ds = nil
		releaseMemory()
	}
}

// Attributed damage: enemy damage given per player against the scoreboard's
// DG column, and team damage against TD.
//
// Bullets are attributed through the flesh-hit events (they name the shooter)
// and grenades/rockets through the missile's thrower.  Dynamite, airstrikes,
// artillery, fire and melee are not, so players who mostly use those fall
// short of DG.  The test guards two things: nothing is attributed that the
// game did not count, and coverage does not regress.
func TestDamageGivenVsScoreboard(t *testing.T) {
	root := sampleRoot(t)
	groups := []struct {
		name               string
		subs               []string
		minRatio, maxRatio float64
	}{
		{"dm_60 round 1, six POVs", []string{"2003-08-15_145952", "2003-08-15_145959", "2003-08-15_150025",
			"2003-08-15_150048", "2003-08-15_150102", "2003-08-15_150139"}, 0.45, 1.10},
		{"dm_59 set 1, twelve POVs and the overview", []string{"-191902-", "-191904-", "-191906-",
			"-191911-", "-191924-", "wsw_mtw"}, 0.62, 1.10},
	}
	for _, g := range groups {
		ds := loadGroup(t, root, g.subs)
		rep, mr, err := MergeDemos(ds)
		if err != nil {
			t.Fatal(err)
		}
		teams := buildTeamTimeline(ds[0])
		var b *board
		var start int
		for _, d := range ds {
			ph := phases(d)
			if ph[0].State == gsPlaying || ph[0].State == gsUnknown {
				continue
			}
			for _, bd := range scoreboards(d) {
				if s := mapStart(ph, bd.time); s >= 0 && bd.time-s >= minRoundMs && b == nil {
					bd := bd
					b, start = &bd, s
				}
			}
		}
		if b == nil {
			t.Fatalf("%s: no scoreboard", g.name)
		}
		enemy, team := map[int]int{}, map[int]int{}
		for _, e := range rep.Events {
			if e.Type != replayEventDamage || int(e.ServerTime) <= start || int(e.ServerTime) > b.time {
				continue
			}
			a, v := int(e.ActorClientNum), int(e.TargetClientNum)
			if ta := teams.at(a, int(e.ServerTime)); ta != 0 && ta == teams.at(v, int(e.ServerTime)) {
				team[a] += int(e.Score)
			} else {
				enemy[a] += int(e.Score)
			}
		}
		total := mr.Damage.Attributed + mr.Damage.Unattributed
		t.Logf("== %s: of %d hp lost, bullets explain %d, explosions %d, unexplained %d (%.0f%% attributed)", g.name,
			total+mr.Damage.Explosive, mr.Damage.Attributed, mr.Damage.Explosive, mr.Damage.Unattributed,
			100*float64(mr.Damage.Attributed+mr.Damage.Explosive)/float64(total+mr.Damage.Explosive))
		info := Describe(ds[0])
		var gtTot, estTot, gtTeam, estTeam int
		for n := 0; n < 64; n++ {
			w, ok := b.damage[n]
			if !ok {
				continue
			}
			t.Logf("   %-18s DG(game)=%5d est=%5d   TD(game)=%4d est=%4d", info.Players[n].Name, w.given, enemy[n], w.team, team[n])
			if enemy[n] > w.given+w.given/7+50 {
				t.Errorf("%s: %s is credited %d damage, the game counted %d", g.name, info.Players[n].Name, enemy[n], w.given)
			}
			gtTot += w.given
			estTot += enemy[n]
			gtTeam += w.team
			estTeam += team[n]
		}
		ratio := float64(estTot) / float64(gtTot)
		t.Logf("   total DG(game)=%d est=%d ratio=%.2f;  TD(game)=%d est=%d", gtTot, estTot, ratio, gtTeam, estTeam)
		if ratio < g.minRatio || ratio > g.maxRatio {
			t.Errorf("%s: attributed enemy damage is %.2f of the scoreboard's damage given, want %.2f-%.2f", g.name, ratio, g.minRatio, g.maxRatio)
		}
		ds = nil
		releaseMemory()
	}
}

const replayEventDamage = replay.EventDamage
