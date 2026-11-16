package stats

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	pidAnn = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	pidBob = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

// pline is a stats line for one match with a player key.
func pline(match, event string, round int, name, pid, team string, kills, deaths, score, streak int, winner string) string {
	w := ""
	if winner != "" {
		w = fmt.Sprintf(`,"winner":%q`, winner)
	}
	return fmt.Sprintf(`{"v":1,"event":%q,"match":%q,"map":"mp_beach","round":%d,"slot":3,"name":%q,"pid":%q,"bot":false,"team":%q,"class":0,"kills":%d,"deaths":%d,"suicides":0,"teamkills":0,"gibs":0,"revives":0,"damage_given":%d,"damage_received":0,"team_damage":0,"score":%d,"kill_streak":%d,"play_ms":60000,"weapons":{"Luger":[5,10,%d,0,1]}%s}`+"\n",
		event, match, round, name, pid, team, kills, deaths, kills*100, score, streak, kills, w)
}

func TestPlayerTotalsPoolsAndHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()

	// an official game: Ann (axis) wins round 1 then plays round 2 on the other side and loses it; the second round decides
	writeStats(t, dir, "replays_20261101_100000.jsonl",
		pline("replays_20261101_100000", "round_end", 1, "Ann", pidAnn, "axis", 5, 2, 50, 4, "axis")+
			pline("replays_20261101_100000", "round_end", 1, "Bob", pidBob, "allies", 2, 5, 20, 1, "axis")+
			pline("replays_20261101_100000", "round_end", 2, "Ann", pidAnn, "allies", 3, 3, 30, 7, "axis")+
			pline("replays_20261101_100000", "round_end", 2, "Bob", pidBob, "axis", 4, 3, 40, 2, "axis"))
	// a later official game she wins (and Bob left before the last round)
	writeStats(t, dir, "replays_20261102_100000.jsonl",
		pline("replays_20261102_100000", "round_end", 1, "Ann", pidAnn, "allies", 8, 1, 80, 6, "allies")+
			pline("replays_20261102_100000", "disconnect", 1, "Bob", pidBob, "axis", 1, 8, 10, 1, ""))
	if _, err := s.IngestFile(ctx, dir, "replays_20261101_100000.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestFile(ctx, dir, "replays_20261102_100000.jsonl"); err != nil {
		t.Fatal(err)
	}
	// a community game from another server, in which Ann has a longer streak (this must not touch her official records)
	c := strings.ReplaceAll(pline("replays_20261103_100000", "round_end", 1, "Annie", pidAnn, "axis", 20, 0, 200, 20, "axis"), `"slot":3`, `"slot":1`)
	if _, err := s.Ingest(ctx, "cccccccccccccccc", PoolCommunity, "replays_20261103_100000.jsonl", Source{Size: int64(len(c)), Read: func(int64) ([]byte, error) { return []byte(c), nil }}); err != nil {
		t.Fatal(err)
	}

	// when and where each key last played: Ann's newest game is the community one (3 Nov), Bob's the 2nd (official)
	seen, err := s.LastSeen(ctx, []string{pidAnn, pidBob, "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"})
	if err != nil {
		t.Fatal(err)
	}
	if a := seen[pidAnn]; a.Pool != PoolCommunity || a.At.UTC().Format("2006-01-02") != "2026-11-03" {
		t.Errorf("Ann last seen %+v", a)
	}
	if b := seen[pidBob]; b.Pool != PoolOfficial || b.At.UTC().Format("2006-01-02") != "2026-11-02" {
		t.Errorf("Bob last seen %+v", b)
	}
	if len(seen) != 2 {
		t.Errorf("a key that never played is listed: %v", seen)
	}
	if m, err := s.LastSeen(ctx, nil); err != nil || len(m) != 0 {
		t.Errorf("no keys: %v %v", m, err)
	}

	// the names a player used in each game, for finding their clips; Bob's games are not Ann's
	gn, err := s.PlayerGameNames(ctx, []string{pidAnn})
	if err != nil {
		t.Fatal(err)
	}
	if len(gn) != 3 || len(gn["replays_20261101_100000"]) != 1 || gn["replays_20261101_100000"][0] != "Ann" {
		t.Errorf("game names = %v", gn)
	}
	if m, err := s.PlayerGameNames(ctx, nil); err != nil || len(m) != 0 {
		t.Errorf("no keys: %v %v", m, err)
	}

	// the recent players: Ann's newest game is the community one (3 Nov, as "Annie"), Bob's the 2nd; newest first
	rp, err := s.RecentPlayers(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rp) != 2 || rp[0].PID != pidAnn || rp[0].Name != "Annie" || rp[0].Pool != PoolCommunity || rp[1].PID != pidBob || rp[1].Map != "mp_beach" {
		t.Errorf("recent players = %+v", rp)
	}
	if rp, err := s.RecentPlayers(ctx, 1); err != nil || len(rp) != 1 || rp[0].PID != pidAnn {
		t.Errorf("only the newest game: %+v %v", rp, err)
	}

	// the weekly summary: all three of Ann's games (1-3 Nov) fall in a window over those days, none in the week before
	dd := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	ws, err := s.WeeklySummary(ctx, []string{pidAnn}, dd(1), dd(4))
	if err != nil {
		t.Fatal(err)
	}
	if ws.Games != 3 || ws.Kills <= 0 || ws.TopWeapon != "Luger" || ws.TopWeaponKills <= 0 || ws.TopClass < 0 || ws.Wins+ws.Losses+ws.Draws > 3 {
		t.Errorf("weekly summary = %+v", ws)
	}
	// packs handed out come from the kill feed: Ann (slot 3) gave 2 medpacks and 1 ammo pack in the first game; Bob's event is not hers
	var gid1 int64
	s.pool.QueryRow(ctx, `SELECT id FROM games WHERE match = 'replays_20261101_100000'`).Scan(&gid1)
	for i, ev := range []struct {
		typ, name string
		slot      int
	}{{"medpack", "Ann", 3}, {"medpack", "Ann", 3}, {"ammo", "Ann", 3}, {"medpack", "Bob", 4}, {"kill", "Ann", 3},
		{"steal", "Ann", 3}, {"steal", "Ann", 3}, {"return", "Ann", 3}, {"capture", "Ann", 3}, {"plant", "Ann", 3}, {"defuse", "Ann", 3}, {"steal", "Bob", 4}} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO game_events (game_id, seq, t_ms, type, actor_slot, actor_name) VALUES ($1, $2, 1000, $3, $4, $5)`, gid1, i, ev.typ, ev.slot, ev.name); err != nil {
			t.Fatal(err)
		}
	}
	if pk, err := s.WeeklySummary(ctx, []string{pidAnn}, dd(1), dd(4)); err != nil || pk.Medpacks != 2 || pk.AmmoPacks != 1 || pk.FeedGames != 1 || pk.ObjTaken != 2 || pk.ObjReturned != 1 || pk.ObjCompleted != 1 || pk.Planted != 1 || pk.Defused != 1 {
		t.Errorf("packs from the feed: %+v %v (want 2 medpacks, 1 ammo pack, 1 game with a feed, objectives 2/1/1/1/1; Bob's events are not hers)", pk, err)
	}
	if acc, ok := ws.TopWeaponAccuracy(); !ok || acc < 49.9 || acc > 50.1 {
		t.Errorf("accuracy of the top weapon = %.1f %v (5 hits in 10 shots in every row: 50%%)", acc, ok)
	}
	if one, err := s.WeeklySummary(ctx, []string{pidAnn}, dd(3), dd(4)); err != nil || one.Games != 1 || one.Kills != 20 {
		t.Errorf("only the 3rd (the community game, 20 kills): %+v %v", one, err)
	}
	if none, err := s.WeeklySummary(ctx, []string{pidAnn}, dd(20), dd(27)); err != nil || none.Games != 0 || none.TopClass != -1 {
		t.Errorf("an empty week: %+v %v", none, err)
	}
	if no, err := s.WeeklySummary(ctx, nil, dd(1), dd(4)); err != nil || no.Games != 0 {
		t.Errorf("no keys: %+v %v", no, err)
	}

	sum, err := s.Player(ctx, pidAnn)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Name != "Annie" || len(sum.Pools) != 2 || sum.Pools[0].Pool != PoolOfficial || sum.Pools[1].Pool != PoolCommunity {
		t.Fatalf("summary: %+v", sum)
	}
	// she has been Ann and Annie: the newest name leads, the other is listed
	if len(sum.Aliases) != 1 || sum.Aliases[0].Name != "Ann" || sum.Aliases[0].Games != 2 {
		t.Errorf("aliases: %+v", sum.Aliases)
	}
	o := sum.Pools[0]
	if o.Games != 2 || o.Wins != 1 || o.Losses != 1 || o.Kills != 16 || o.Deaths != 6 || o.Score != 160 {
		t.Errorf("official totals: %+v", o)
	}
	if o.Records.KillStreak != 7 || o.Records.MostKills != 8 || o.Records.BestScore != 80 {
		t.Errorf("official records: %+v", o.Records)
	}
	if o.Efficiency != 72 || o.Headshots != 3 || len(o.Weapons) != 1 || o.Weapons[0].Kills != 16 {
		t.Errorf("official weapons/eff: %+v", o)
	}
	cm := sum.Pools[1]
	if cm.Games != 1 || cm.Kills != 20 || cm.Records.KillStreak != 20 {
		t.Errorf("community totals: %+v", cm)
	}

	// the second read comes from the cache; new lines make it stale and the next read sees them
	var stale bool
	s.pool.QueryRow(ctx, `SELECT stale FROM player_cache WHERE pid = $1`, pidAnn).Scan(&stale)
	if stale {
		t.Errorf("the cache should be fresh after a read")
	}
	appendStats(t, dir, "replays_20261102_100000.jsonl", pline("replays_20261102_100000", "round_end", 2, "Ann", pidAnn, "allies", 1, 0, 10, 1, "allies"))
	if _, err := s.IngestFile(ctx, dir, "replays_20261102_100000.jsonl"); err != nil {
		t.Fatal(err)
	}
	s.pool.QueryRow(ctx, `SELECT stale FROM player_cache WHERE pid = $1`, pidAnn).Scan(&stale)
	if !stale {
		t.Errorf("ingesting lines should mark the player stale")
	}
	if again, _ := s.Player(ctx, pidAnn); again.Pools[0].Kills != 17 {
		t.Errorf("a stale cache was served: %+v", again.Pools[0])
	}
	// put it back for the checks below
	s.pool.Exec(ctx, `DELETE FROM game_players WHERE pid = $1 AND kills = 1 AND score = 10`, pidAnn)
	s.pool.Exec(ctx, `UPDATE player_cache SET stale = true`)

	bob, err := s.Player(ctx, pidBob)
	if err != nil {
		t.Fatal(err)
	}
	if b := bob.Pools[0]; b.Games != 2 || b.Wins != 1 || b.Losses != 0 || b.Draws != 0 {
		// game 1: Bob was axis in the last round, axis won: a win; game 2: he left before the last round: no result
		t.Errorf("Bob: %+v", b)
	}

	games, total, err := s.PlayerHistory(ctx, []string{pidAnn}, "", 2, 0)
	if err != nil || total != 3 || len(games) != 2 || games[0].Pool != PoolCommunity || games[0].ServerID != "cccccccccccccccc" {
		t.Fatalf("history: %d %+v %v", total, games, err)
	}
	if g, total, _ := s.PlayerHistory(ctx, []string{pidAnn}, PoolOfficial, 10, 0); total != 2 || g[0].Match != "replays_20261102_100000" || g[0].Result != "win" || g[1].Result != "loss" {
		t.Errorf("official history: %d %+v", total, g)
	}
	if _, err := s.Player(ctx, "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"); err != ErrNoPlayer {
		t.Errorf("an unknown player: %v", err)
	}
	// purging the community server removes everything of it from the totals too (nothing is cached)
	if n, err := s.PurgeServer(ctx, "cccccccccccccccc"); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if after, _ := s.Player(ctx, pidAnn); len(after.Pools) != 1 || after.Pools[0].Pool != PoolOfficial || after.Name != "Ann" {
		t.Errorf("after the purge: %+v", after)
	}
	// a default name is neither the headline nor an alias
	d := pline("replays_20261104_100000", "round_end", 1, "WolfPlayer", pidAnn, "axis", 1, 1, 10, 1, "axis")
	if _, err := s.Ingest(ctx, "", PoolOfficial, "replays_20261104_100000.jsonl", Source{Size: int64(len(d)), Read: func(int64) ([]byte, error) { return []byte(d), nil }}); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.Player(ctx, pidAnn); def.Name != "Ann" || def.AliasTotal != 0 || len(def.Aliases) != 0 {
		t.Errorf("a default name showed: %q %+v", def.Name, def.Aliases)
	}
	// the daily file an older game build wrote final lines to (no replay open) is not a game: it makes none and counts for nothing
	day := pline("stats_20261104", "map_end", 1, "Ann", pidAnn, "axis", 1, 0, 5, 1, "")
	before, _ := s.Player(ctx, pidAnn)
	if n, err := s.Ingest(ctx, "", PoolOfficial, "stats_20261104.jsonl", Source{Size: int64(len(day)), Read: func(int64) ([]byte, error) { return []byte(day), nil }}); err != nil || n != 0 {
		t.Errorf("a daily line was stored: %d %v", n, err)
	}
	if after, _ := s.Player(ctx, pidAnn); after.Pools[0].Games != before.Pools[0].Games {
		t.Errorf("a daily line made a game: %d -> %d", before.Pools[0].Games, after.Pools[0].Games)
	}
	// a line with a malformed key, or from a bot, is stored with no key
	if l, err := ParseLine([]byte(pline("m", "round_end", 1, "X", "not-a-key", "axis", 1, 1, 1, 1, ""))); err != nil || l.PlayerID != "" {
		t.Errorf("a bad key was kept: %q %v", l.PlayerID, err)
	}
}

func TestMergePlayers(t *testing.T) {
	when := func(day int) *time.Time { d := time.Date(2026, 11, day, 10, 0, 0, 0, time.UTC); return &d }
	a := &PlayerSummary{Name: "Ann", Aliases: []Alias{{Name: "Annie", Games: 2, LastSeen: when(1)}}, Pools: []PlayerTotals{{
		Pool: PoolOfficial, Games: 3, Wins: 2, Losses: 1, Kills: 30, Deaths: 10, DamageGiven: 3000, PlayMs: 600000, Headshots: 5,
		Records: PlayerRecords{KillStreak: 6, BestScore: 70, MostKills: 11, LongestKill: &LongestKill{Dist: 3000, Weapon: "Sniper"}},
		Weapons: []WeaponStats{{Name: "Luger", Hits: 10, Atts: 20, Kills: 12, Headshots: 2}, {Name: "MP-40", Hits: 5, Atts: 9, Kills: 18}}}}}
	b := &PlayerSummary{Name: "AnnOnLauncher", Aliases: []Alias{{Name: "Annie", Games: 1, LastSeen: when(5)}, {Name: "WolfPlayer", Games: 4, LastSeen: when(6)}}, Pools: []PlayerTotals{
		{Pool: PoolOfficial, Games: 2, Wins: 1, Draws: 1, Kills: 20, Deaths: 10, DamageGiven: 1500, PlayMs: 300000, Headshots: 3,
			Records: PlayerRecords{KillStreak: 9, BestScore: 50, MostKills: 12, LongestKill: &LongestKill{Dist: 2000, Weapon: "Luger"}},
			Weapons: []WeaponStats{{Name: "Luger", Hits: 4, Atts: 8, Kills: 6, Headshots: 1}}},
		{Pool: PoolCommunity, Games: 1, Kills: 7, Deaths: 1}}}
	m := MergePlayers([]*PlayerSummary{a, b})
	o := m.Pools[0]
	if m.Keys != 2 || len(m.Pools) != 2 || o.Pool != PoolOfficial || m.Pools[1].Pool != PoolCommunity {
		t.Fatalf("pools: %+v", m)
	}
	if o.Games != 5 || o.Wins != 3 || o.Losses != 1 || o.Draws != 1 || o.Kills != 50 || o.Deaths != 20 || o.Efficiency != 71 || o.DamageGiven != 4500 || o.PlayMs != 900000 || o.Headshots != 8 {
		t.Errorf("totals: %+v", o)
	}
	if o.Records.KillStreak != 9 || o.Records.BestScore != 70 || o.Records.MostKills != 12 || o.Records.LongestKill.Dist != 3000 {
		t.Errorf("records are the best of the keys: %+v", o.Records)
	}
	if len(o.Weapons) != 2 || o.Weapons[0].Name != "Luger" || o.Weapons[0].Kills != 18 || o.Weapons[0].Hits != 14 || o.Weapons[0].Headshots != 3 {
		t.Errorf("weapons: %+v", o.Weapons)
	}
	// the key played most recently gives the name; the others are aliases, pooled, with default names left out
	if m.Name != "AnnOnLauncher" || m.AliasTotal != 2 || m.Aliases[0].Name != "Annie" || m.Aliases[0].Games != 3 || m.Aliases[1].Name != "Ann" {
		t.Errorf("names: %q %+v", m.Name, m.Aliases)
	}
	if MergePlayers(nil) != nil {
		t.Errorf("no summaries")
	}
}
