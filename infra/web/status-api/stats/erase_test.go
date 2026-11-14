package stats

import (
	"context"
	"strings"
	"testing"

	"s4ndmod26/status-api/replay"
)

func TestEraseKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	m := "replays_20261101_100000"
	bobSaw := strings.Replace(pline(m, "round_end", 1, "Bob", pidBob, "allies", 2, 5, 20, 1, "axis"), `"weapons"`, `"victims":[{"slot":3,"name":"Ann","n":2}],"weapons"`, 1)
	bobSaw = strings.Replace(bobSaw, `"slot":3`, `"slot":5`, 1)
	writeStats(t, dir, m+".jsonl",
		pline(m, "round_end", 1, "Ann", pidAnn, "axis", 5, 2, 50, 4, "axis")+bobSaw+
			pline(m, "round_end", 2, "Ann", pidAnn, "allies", 3, 3, 30, 7, "axis"))
	if _, err := s.IngestFile(ctx, dir, m+".jsonl"); err != nil {
		t.Fatal(err)
	}
	// the kill feed names people (no keys), and the replay index lists the names
	evs := []FeedEvent{
		{TMs: 1000, Type: "kill", Actor: "Bob", ActorSlot: 5, Target: "Ann", TargetSlot: 3, Weapon: "MP40"},
		{TMs: 2000, Type: "kill", Actor: "Ann", ActorSlot: 3, Target: "Bob", TargetSlot: 5, Weapon: "Luger", Headshot: true, Pos: &[3]int32{1, 2, 3}},
		{TMs: 3000, Type: "join", Actor: "Cy", ActorSlot: 9, ActorTeam: "axis", TargetSlot: -1},
	}
	if _, err := s.IngestFeed(ctx, "", m, 10, func() ([]FeedEvent, error) { return evs, nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.putReplay(ctx, "", m, replay.Summary{Players: []string{"Ann", "Bob", "Cy"}, PotgPlayer: "Ann",
		Highlights: []replay.HighlightSummary{{Name: "Ann", Score: 3}, {Name: "Bob", Score: 2}}}, 1, true, "http://x"); err != nil {
		t.Fatal(err)
	}

	pv, err := s.PreviewErase(ctx, pidAnn)
	if err != nil || pv.Games != 1 || pv.Rows != 2 || pv.Events != 2 {
		t.Fatalf("preview %+v %v", pv, err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_players WHERE pid <> ''`); n != 3 {
		t.Fatalf("preview changed something: %d", n)
	}
	if _, err := s.EraseKey(ctx, "not a key"); err == nil {
		t.Error("a bad key was accepted")
	}
	res, err := s.EraseKey(ctx, pidAnn)
	if err != nil || res.Games != 1 || res.Rows != 2 || res.Events != 2 || res.Index != 1 {
		t.Fatalf("erase %+v %v", res, err)
	}
	// her rows are anonymous, Bob's stay
	if n := countRows(t, s, `SELECT count(*) FROM game_players WHERE pid = '`+pidAnn+`'`); n != 0 {
		t.Errorf("the key is still on %d rows", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_players WHERE name = 'Deleted Player' AND name_raw = 'Deleted Player' AND slot = 3`); n != 2 {
		t.Errorf("anonymous rows: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_players WHERE pid = '`+pidBob+`' AND name = 'Bob'`); n != 1 {
		t.Errorf("Bob was touched: %d", n)
	}
	// her name is gone from the feed, the victims and the index; Bob's and Cy's stay
	if n := countRows(t, s, `SELECT count(*) FROM game_events WHERE actor_name = 'Ann' OR target_name = 'Ann'`); n != 0 {
		t.Errorf("Ann is still in %d events", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_events WHERE (actor_name = 'Deleted Player' AND actor_slot = 3) OR (target_name = 'Deleted Player' AND target_slot = 3)`); n != 2 {
		t.Errorf("events with the new name: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_events WHERE actor_name IN ('Bob','Cy') OR target_name = 'Bob'`); n != 3 {
		t.Errorf("others' events changed: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_player_victims WHERE victim_name = 'Ann'`); n != 0 {
		t.Errorf("Ann is still a victim by name: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_player_victims WHERE victim_name = 'Deleted Player' AND kills = 2`); n != 1 {
		t.Errorf("the victim list lost the kills: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM replay_index WHERE summary::text LIKE '%Ann%'`); n != 0 {
		t.Errorf("Ann is still in the replay index")
	}
	if n := countRows(t, s, `SELECT count(*) FROM replay_index WHERE summary->>'potg_player' = 'Deleted Player' AND summary::text LIKE '%Bob%'`); n != 1 {
		t.Errorf("the index lost the others")
	}
	// her page and cache are gone, Bob's totals are recomputed
	if _, err := s.Player(ctx, pidAnn); err == nil {
		t.Error("an erased key still has a player page")
	}
	if n := countRows(t, s, `SELECT count(*) FROM player_cache WHERE pid = '`+pidAnn+`'`); n != 0 {
		t.Errorf("her cache stayed")
	}
	if n := countRows(t, s, `SELECT count(*) FROM erased_keys`); n != 1 {
		t.Errorf("tombstones: %d", n)
	}
	// a rebuild: the servers' files still hold her key, and reading them again must not bring her back
	if _, err := s.pool.Exec(ctx, `DELETE FROM games`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM ingest_files`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestFile(ctx, dir, m+".jsonl"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_players WHERE pid = '`+pidAnn+`' OR name = 'Ann'`); n != 0 {
		t.Errorf("a re-read brought her back: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_players WHERE name = 'Deleted Player' AND slot = 3`); n != 2 {
		t.Errorf("anonymous rows after a re-read: %d", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_player_victims WHERE victim_name = 'Ann'`); n != 0 {
		t.Errorf("a re-read put her back in the victim list")
	}
	if _, err := s.IngestFeed(ctx, "", m, 11, func() ([]FeedEvent, error) { return evs, nil }); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM game_events WHERE actor_name = 'Ann' OR target_name = 'Ann'`); n != 0 {
		t.Errorf("a re-read of the feed put her back: %d", n)
	}
	// an unknown key is fine: nothing to change, but it is remembered
	if res, err := s.EraseKey(ctx, "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"); err != nil || res.Rows != 0 {
		t.Errorf("unknown key: %+v %v", res, err)
	}
}

func countRows(t *testing.T, s *Store, q string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}
