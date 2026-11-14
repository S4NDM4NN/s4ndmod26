package stats

import (
	"context"
	"fmt"
	"strings"
	"testing"
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

	games, total, err := s.PlayerHistory(ctx, pidAnn, "", 2, 0)
	if err != nil || total != 3 || len(games) != 2 || games[0].Pool != PoolCommunity || games[0].ServerID != "cccccccccccccccc" {
		t.Fatalf("history: %d %+v %v", total, games, err)
	}
	if g, total, _ := s.PlayerHistory(ctx, pidAnn, PoolOfficial, 10, 0); total != 2 || g[0].Match != "replays_20261102_100000" || g[0].Result != "win" || g[1].Result != "loss" {
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
	// a line with a malformed key, or from a bot, is stored with no key
	if l, err := ParseLine([]byte(pline("m", "round_end", 1, "X", "not-a-key", "axis", 1, 1, 1, 1, ""))); err != nil || l.PlayerID != "" {
		t.Errorf("a bad key was kept: %q %v", l.PlayerID, err)
	}
}
