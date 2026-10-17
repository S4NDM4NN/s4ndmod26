package stats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a real Postgres: set STATS_TEST_DSN (tools/local/stats_test.sh starts a throwaway one).  Each test gets
// its own schema so they do not see each other's rows.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("STATS_TEST_DSN")
	if dsn == "" {
		t.Skip("STATS_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("t_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := &Store{pool: pool}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func line(event string, round, slot int, name, team string, kills, deaths, score int, weapons string) string {
	return fmt.Sprintf(`{"v":1,"event":%q,"match":"replays_20261017_012254","map":"mp_beach","round":%d,"slot":%d,"name":%q,"bot":false,"team":%q,"class":0,"kills":%d,"deaths":%d,"suicides":0,"teamkills":0,"gibs":0,"revives":0,"damage_given":%d,"damage_received":0,"team_damage":0,"score":%d,"weapons":{%s}}`+"\n",
		event, round, slot, name, team, kills, deaths, kills*100, score, weapons)
}

func writeStats(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendStats(t *testing.T, dir, name, content string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateTwice(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestIngestAndQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	const name = "replays_20261017_012254.jsonl"

	// stopwatch: round 1 then round 2 (numbers are per round); Ann played both, Bob left in round 1 and is not in round 2
	writeStats(t, dir, name,
		line("round_end", 1, 3, "Ann", "axis", 4, 2, 30, `"Luger":[5,10,1,0,0],"MP-40":[20,50,3,1,2]`)+
			line("disconnect", 1, 4, "Bob", "allies", 1, 3, 5, `"Luger":[1,4,1,2,0]`)+
			line("round_end", 2, 3, "Ann", "allies", 6, 1, 55, `"MP-40":[30,70,5,0,1],"Grenade":[4,2,2,0,0]`))
	n, err := s.IngestFile(ctx, dir, name)
	if err != nil || n != 3 {
		t.Fatalf("ingest: stored %d, %v", n, err)
	}

	g, err := s.Game(ctx, "replays_20261017_012254")
	if err != nil {
		t.Fatal(err)
	}
	if g.Map != "mp_beach" || g.Rounds != 2 || len(g.Players) != 2 {
		t.Fatalf("game: %+v", g)
	}
	if g.StartedAt == nil || !g.StartedAt.Equal(time.Date(2026, 10, 17, 1, 22, 54, 0, time.UTC)) {
		t.Errorf("started_at = %v", g.StartedAt)
	}
	var ann, bob PlayerStats
	for _, p := range g.Players {
		if p.Name == "Ann" {
			ann = p
		} else {
			bob = p
		}
	}
	if ann.Kills != 10 || ann.Deaths != 3 || ann.Score != 85 || ann.Rounds != 2 || ann.Efficiency != 76 {
		t.Errorf("Ann should be the sum of both rounds: %+v", ann)
	}
	if ann.Team != "allies" {
		t.Errorf("team is the last one played on (stopwatch swaps sides), got %s", ann.Team)
	}
	if len(ann.Weapons) != 3 || ann.Weapons[0].Name != "Luger" || ann.Weapons[1].Name != "MP-40" || ann.Weapons[2].Name != "Grenade" {
		t.Fatalf("weapons in the game's order: %+v", ann.Weapons)
	}
	if mp := ann.Weapons[1]; mp.Hits != 50 || mp.Atts != 120 || mp.Kills != 8 || mp.Deaths != 1 || mp.Headshots != 3 {
		t.Errorf("MP-40 summed over the rounds: %+v", mp)
	}
	if bob.Kills != 1 || bob.Rounds != 1 {
		t.Errorf("Bob: %+v", bob)
	}
	if g.Players[0].Team != "allies" && g.Players[0].Name == "Bob" {
		t.Errorf("order: axis first, then by score")
	}

	// ingesting again changes nothing
	if n, err := s.IngestFile(ctx, dir, name); err != nil || n != 0 {
		t.Errorf("second pass stored %d, %v", n, err)
	}
	// a line added later is picked up on its own
	appendStats(t, dir, name, line("map_end", 2, 9, "Cy", "axis", 0, 1, 0, ``))
	if n, err := s.IngestFile(ctx, dir, name); err != nil || n != 1 {
		t.Errorf("appended line: stored %d, %v", n, err)
	}
	if g, _ = s.Game(ctx, "replays_20261017_012254"); len(g.Players) != 3 {
		t.Errorf("Cy should be in the game now: %d players", len(g.Players))
	}
}

func TestIngestWaitsForTheWholeLine(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	const name = "replays_20261017_012254.jsonl"
	full := line("round_end", 1, 3, "Ann", "axis", 4, 2, 30, `"Luger":[5,10,1,0,0]`)

	writeStats(t, dir, name, full[:len(full)/2]) // the server is mid-write
	if n, err := s.IngestFile(ctx, dir, name); err != nil || n != 0 {
		t.Fatalf("half a line: stored %d, %v", n, err)
	}
	appendStats(t, dir, name, full[len(full)/2:])
	if n, err := s.IngestFile(ctx, dir, name); err != nil || n != 1 {
		t.Fatalf("finished line: stored %d, %v", n, err)
	}
}

func TestIngestSkipsBadLinesAndKeepsGoing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	const name = "replays_20261017_012254.jsonl"
	writeStats(t, dir, name, "not json at all\n"+line("round_end", 1, 3, "Ann", "axis", 4, 2, 30, ``)+"\n"+`{"v":9}`+"\n")
	n, err := s.IngestFile(ctx, dir, name)
	if err != nil || n != 1 {
		t.Fatalf("stored %d, %v", n, err)
	}
	if n, _ := s.IngestFile(ctx, dir, name); n != 0 {
		t.Errorf("bad lines are not retried forever, stored %d", n)
	}
}

func TestGameNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Game(context.Background(), "nope"); err != ErrNoGame {
		t.Errorf("got %v, want ErrNoGame", err)
	}
}

func TestIngestDir(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	writeStats(t, dir, "replays_20261017_012254.jsonl", line("round_end", 1, 3, "Ann", "axis", 4, 2, 30, ``))
	writeStats(t, dir, "ignored.txt", "x")
	s.IngestDir(ctx, dir)
	have, err := s.GamesWithStats(ctx)
	if err != nil || !have["replays_20261017_012254"] || len(have) != 1 {
		t.Errorf("games with stats: %v %v", have, err)
	}
	s.IngestDir(ctx, filepath.Join(dir, "missing")) // a missing directory is not an error
}

// a line with the extra fields the game added later (streaks, multi kills, who killed whom, ...)
func lineExtra(event string, round, slot int, name, team, extra string) string {
	l := line(event, round, slot, name, team, 1, 1, 10, ``)
	return l[:len(l)-len("}\n")] + "," + extra + "}\n" // add the fields before the closing brace
}

func TestExtrasAndRivals(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	const name = "replays_20261017_012254.jsonl"
	writeStats(t, dir, name,
		lineExtra("round_end", 1, 3, "Ann", "axis", `"kill_streak":4,"death_streak":1,"double_kills":1,"first_blood":1,"play_ms":600000,"goombas":1,"victims":[{"slot":4,"name":"Bob","n":3},{"slot":5,"name":"Cy","n":1}]`)+
			lineExtra("round_end", 1, 4, "Bob", "allies", `"kill_streak":1,"death_streak":3,"victims":[{"slot":3,"name":"Ann","n":1}]`)+
			lineExtra("round_end", 1, 5, "Cy", "allies", `"kill_streak":2,"first_death":1,"victims":[{"slot":3,"name":"Ann","n":2}]`)+
			lineExtra("round_end", 2, 3, "Ann", "allies", `"kill_streak":2,"death_streak":5,"double_kills":1,"triple_kills":1,"play_ms":500000,"victims":[{"slot":4,"name":"Bob","n":2}]`))
	if n, err := s.IngestFile(ctx, dir, name); err != nil || n != 4 {
		t.Fatalf("ingest: %d %v", n, err)
	}
	g, err := s.Game(ctx, "replays_20261017_012254")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]PlayerStats{}
	for _, p := range g.Players {
		by[p.Name] = p
	}
	ann := by["Ann"]
	if ann.KillStreak != 4 || ann.DeathStreak != 5 {
		t.Errorf("streaks are the best/worst over the rounds, not sums: %d / %d", ann.KillStreak, ann.DeathStreak)
	}
	if ann.DoubleKills != 2 || ann.TripleKills != 1 || ann.FirstBlood != 1 || ann.PlayMs != 1100000 || ann.Goombas != 1 {
		t.Errorf("Ann's counts add up: %+v", ann)
	}
	if ann.FavouriteVictim == nil || ann.FavouriteVictim.Name != "Bob" || ann.FavouriteVictim.N != 5 {
		t.Errorf("Ann's favourite victim is Bob (3+2): %+v", ann.FavouriteVictim)
	}
	if ann.Nemesis == nil || ann.Nemesis.Name != "Cy" || ann.Nemesis.N != 2 {
		t.Errorf("Ann's nemesis is Cy (2, Bob had 1): %+v", ann.Nemesis)
	}
	if bob := by["Bob"]; bob.Nemesis == nil || bob.Nemesis.Name != "Ann" || bob.Nemesis.N != 5 || bob.FavouriteVictim.Name != "Ann" {
		t.Errorf("Bob: %+v %+v", bob.Nemesis, bob.FavouriteVictim)
	}
	if cy := by["Cy"]; cy.FirstDeath != 1 || cy.Nemesis == nil || cy.Nemesis.Name != "Ann" || cy.Nemesis.N != 1 {
		t.Errorf("Cy: %+v", cy)
	}
	// the awards come with the game
	found := map[string]string{}
	for _, a := range g.Awards {
		found[a.Key] = a.Player
	}
	if found["kill_streak"] != "Ann" || found["first_blood"] != "Ann" {
		t.Errorf("awards: %+v", g.Awards)
	}
}

func TestOldLinesStillIngest(t *testing.T) {
	// lines written before the extra fields existed have none of them: zeros, no rivals, no error
	s := newTestStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	const name = "replays_20261017_012254.jsonl"
	writeStats(t, dir, name, line("round_end", 1, 3, "Ann", "axis", 4, 2, 30, `"Luger":[5,10,1,0,0]`))
	if n, err := s.IngestFile(ctx, dir, name); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	g, err := s.Game(ctx, "replays_20261017_012254")
	if err != nil || len(g.Players) != 1 {
		t.Fatalf("%v %+v", err, g)
	}
	if p := g.Players[0]; p.KillStreak != 0 || p.Nemesis != nil || p.FavouriteVictim != nil || p.PlayMs != 0 {
		t.Errorf("%+v", p)
	}
}
