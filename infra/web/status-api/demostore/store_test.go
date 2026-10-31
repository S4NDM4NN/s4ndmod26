package demostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests need a real Postgres: set STATS_TEST_DSN (tools/local/demos_test.sh starts a throwaway one).  Each test
// gets its own schema.
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
	schema := fmt.Sprintf("d_%d", time.Now().UnixNano())
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
	s := NewStore(pool)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

var sha = func(c byte) string { return strings.Repeat(string(c), 64) }

func demo(c byte, recorder string, start, end int) NewDemo {
	return NewDemo{SHA256: sha(c), Protocol: 60, Size: 1000, Map: "mp_beach", ServerID: "1", Recorder: recorder,
		RecorderSlot: int(c), StartMs: start, EndMs: end, Frames: 100, Players: []string{recorder, "other"}}
}

func TestMigrateIsRepeatable(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestUploadsAreClaimedAndRemovedByTheirOwner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	d, stale, err := s.AddDemo(ctx, demo('a', "Ann", 1000, 9000), "travis", "ann.dm_60", Attachment{})
	if err != nil || len(stale) != 0 {
		t.Fatalf("AddDemo: %v %v", err, stale)
	}
	if got, _ := s.DemoBySHA(ctx, sha('a')); got == nil || got.ID != d.ID || got.MatchID != d.MatchID {
		t.Fatalf("DemoBySHA = %+v", got)
	}
	if got, _ := s.DemoBySHA(ctx, sha('z')); got != nil {
		t.Errorf("DemoBySHA found a demo that was never stored: %+v", got)
	}
	// the same file from someone else is the same demo with a second claim
	if news, err := s.AddUpload(ctx, d.ID, "sandy", "same.dm_60"); err != nil || !news {
		t.Fatalf("AddUpload by a second owner: %v %v", news, err)
	}
	if news, _ := s.AddUpload(ctx, d.ID, "sandy", "same.dm_60"); news {
		t.Error("the same claim twice was news")
	}
	if _, _, err := s.AddDemo(ctx, demo('a', "Ann", 1000, 9000), "sandy", "x", Attachment{}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("storing the same content again: %v, want ErrDuplicate", err)
	}
	if ups, _ := s.UploadsOf(ctx, "travis"); len(ups) != 1 || ups[0].FileID != d.ID || ups[0].Filename != "ann.dm_60" {
		t.Errorf("travis's uploads = %+v", ups)
	}
	if ups, _ := s.UploadsOf(ctx, "nobody"); len(ups) != 0 {
		t.Errorf("an owner with no uploads has %+v", ups)
	}

	// someone who did not upload it cannot remove it, and nothing changes
	if _, ok, err := s.RemoveUpload(ctx, d.ID, "mallory"); err != nil || ok {
		t.Fatalf("a stranger removed an upload: %v %v", ok, err)
	}
	if got, _ := s.DemoBySHA(ctx, sha('a')); got == nil {
		t.Fatal("the demo went when a stranger tried to remove it")
	}
	// travis removes his claim: sandy's remains, so the demo and its file stay
	r, ok, err := s.RemoveUpload(ctx, d.ID, "travis")
	if err != nil || !ok || r.DemoGone || r.MatchGone {
		t.Fatalf("first removal: %+v %v %v", r, ok, err)
	}
	if got, _ := s.DemoBySHA(ctx, sha('a')); got == nil {
		t.Fatal("the demo went while someone still had it")
	}
	if _, ok, _ := s.RemoveUpload(ctx, d.ID, "travis"); ok {
		t.Error("the same claim was removed twice")
	}
	// the last claim takes the demo and its (now empty) match with it
	if _, _, err := s.SetMerged(ctx, d.MatchID, 2, PathsKey(d.MatchID, 2)); err != nil {
		t.Fatal(err)
	}
	r, ok, err = s.RemoveUpload(ctx, d.ID, "sandy")
	if err != nil || !ok || !r.DemoGone || !r.MatchGone || r.SHA256 != sha('a') || r.MatchID != d.MatchID {
		t.Fatalf("last removal: %+v %v %v", r, ok, err)
	}
	if len(r.Stale) != 1 || r.Stale[0] != PathsKey(d.MatchID, 2) {
		t.Errorf("the match's merged output was not handed back for deletion: %v", r.Stale)
	}
	if m, _ := s.GetMatch(ctx, d.MatchID); m != nil {
		t.Errorf("the match is still there: %+v", m)
	}
}

func TestRemovingOneDemoKeepsTheMatchAndInvalidatesItsOutput(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	d1, _, _ := s.AddDemo(ctx, demo('a', "Ann", 1000, 9000), "travis", "a", Attachment{})
	d2, _, err := s.AddDemo(ctx, demo('b', "Bob", 1000, 9000), "travis", "b", Attachment{MatchID: d1.MatchID})
	if err != nil || d2.MatchID != d1.MatchID {
		t.Fatalf("second demo: %+v %v", d2, err)
	}
	m, _ := s.GetMatch(ctx, d1.MatchID)
	if len(m.Demos) != 2 || m.Version != 3 { // created at 1, one bump per demo added
		t.Fatalf("match = %d demos, version %d", len(m.Demos), m.Version)
	}
	key := PathsKey(m.ID, m.Version)
	if _, ok, _ := s.SetMerged(ctx, m.ID, m.Version, key); !ok {
		t.Fatal("SetMerged for the current version was refused")
	}
	r, ok, err := s.RemoveUpload(ctx, d2.ID, "travis")
	if err != nil || !ok || !r.DemoGone || r.MatchGone {
		t.Fatalf("removal: %+v %v %v", r, ok, err)
	}
	if len(r.Stale) != 1 || r.Stale[0] != key {
		t.Errorf("stale = %v, want the merged output built before the removal (%s)", r.Stale, key)
	}
	after, _ := s.GetMatch(ctx, m.ID)
	if len(after.Demos) != 1 || after.Version != m.Version+1 || after.MergedKey != "" {
		t.Errorf("match after removal: %d demos, version %d, merged %q", len(after.Demos), after.Version, after.MergedKey)
	}
	// an output built for the old version must not be recorded now
	if _, ok, _ := s.SetMerged(ctx, m.ID, m.Version, key); ok {
		t.Error("an out-of-date merged output was recorded")
	}
}

func TestADemoThatBridgesTwoMatchesJoinsThem(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	a, _, _ := s.AddDemo(ctx, demo('a', "Ann", 1000, 5000), "travis", "a", Attachment{})
	b, _, _ := s.AddDemo(ctx, demo('b', "Bob", 6000, 9000), "travis", "b", Attachment{})
	if a.MatchID == b.MatchID {
		t.Fatal("two unrelated demos share a match")
	}
	mb, _ := s.GetMatch(ctx, b.MatchID)
	s.SetMerged(ctx, b.MatchID, mb.Version, PathsKey(b.MatchID, mb.Version))
	// a third demo overlaps both: it joins a's match, and b's demos move over, 250 ms later on the joined clock
	bridge := demo('c', "Cy", 4000, 7000)
	d, stale, err := s.AddDemo(ctx, bridge, "sandy", "c", Attachment{MatchID: a.MatchID, Merge: map[string]int{b.MatchID: 250}})
	if err != nil {
		t.Fatal(err)
	}
	if d.MatchID != a.MatchID {
		t.Fatalf("the bridging demo is in %s, want %s", d.MatchID, a.MatchID)
	}
	if len(stale) != 1 || stale[0] != PathsKey(b.MatchID, mb.Version) {
		t.Errorf("stale = %v, want the folded match's output", stale)
	}
	if m, _ := s.GetMatch(ctx, b.MatchID); m != nil {
		t.Errorf("the folded match still exists: %+v", m)
	}
	m, _ := s.GetMatch(ctx, a.MatchID)
	if len(m.Demos) != 3 {
		t.Fatalf("the joined match has %d demos, want 3", len(m.Demos))
	}
	for _, md := range m.Demos {
		if md.SHA256 == sha('b') && md.BaseOffsetMs != 250 {
			t.Errorf("b's offset = %d, want 250", md.BaseOffsetMs)
		}
		if md.SHA256 == sha('a') && md.BaseOffsetMs != 0 {
			t.Errorf("a's offset = %d, want 0", md.BaseOffsetMs)
		}
	}
	// the claims came along: bob's demo is still travis's to remove
	if ups, _ := s.UploadsOf(ctx, "travis"); len(ups) != 2 {
		t.Errorf("travis's uploads after the merge: %+v", ups)
	}
}

func TestListMatches(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	d1, _, _ := s.AddDemo(ctx, demo('a', "Ann", 1000, 9000), "travis", "a", Attachment{})
	spec := demo('s', "", 500, 9500)
	spec.Spectator, spec.Rounds, spec.Gametype = true, 2, 6
	s.AddDemo(ctx, spec, "travis", "s", Attachment{MatchID: d1.MatchID})
	other := demo('o', "Zed", 1, 100)
	other.Map = "mp_sub"
	s.AddDemo(ctx, other, "sandy", "o", Attachment{})

	got, total, err := s.ListMatches(ctx, 10, 0)
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("ListMatches: %d of %d, %v", len(got), total, err)
	}
	if got[0].Map != "mp_sub" { // the match changed last comes first
		t.Errorf("order: %s first", got[0].Map)
	}
	var beach MatchSummary
	for _, m := range got {
		if m.Map == "mp_beach" {
			beach = m
		}
	}
	if beach.Demos != 2 || len(beach.Recorders) != 1 || beach.Recorders[0] != "Ann" || beach.Spectators != 1 ||
		beach.Rounds != 2 || beach.Gametype != 6 || beach.DurationMs != 9000 {
		t.Errorf("summary = %+v", beach)
	}
	if len(beach.Players) != 2 || beach.Players[0] != "Ann" || beach.Players[1] != "other" {
		t.Errorf("players = %v (the roster union, sorted)", beach.Players)
	}
	if page, total, _ := s.ListMatches(ctx, 1, 1); len(page) != 1 || total != 2 {
		t.Errorf("paging: %d of %d", len(page), total)
	}
}

// A demo of several games is one stored file and one row per game, each in its own match.
func TestADemoOfSeveralGamesIsOneFileAndARowPerGame(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	g := func(n int) NewDemo {
		d := demo('s', "", 1000*n, 1000*n+900)
		d.Spectator, d.Segment, d.WindowFrom, d.WindowTo = true, n, 1000*n, 1000*n+900
		return d
	}
	d1, _, err := s.AddDemo(ctx, g(1), "travis", "long", Attachment{})
	if err != nil {
		t.Fatal(err)
	}
	d2, _, err := s.AddDemo(ctx, g(2), "travis", "long", Attachment{})
	if err != nil {
		t.Fatalf("second game of the same file: %v", err)
	}
	if d1.MatchID == d2.MatchID || d1.Segment != 1 || d2.Segment != 2 || d2.WindowFrom != 2000 {
		t.Fatalf("rows: %+v %+v", d1, d2)
	}
	if _, _, err := s.AddDemo(ctx, g(2), "travis", "long", Attachment{}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("the same game twice: %v", err)
	}
	if all, _ := s.DemosBySHA(ctx, sha('s')); len(all) != 2 {
		t.Errorf("DemosBySHA = %d rows", len(all))
	}
	r, ok, err := s.RemoveUpload(ctx, d1.ID, "travis")
	if err != nil || !ok || !r.DemoGone || !r.MatchGone || r.FileGone {
		t.Fatalf("removing game 1: %+v %v %v (the file is still used by game 2)", r, ok, err)
	}
	r, ok, err = s.RemoveUpload(ctx, d2.ID, "travis")
	if err != nil || !ok || !r.FileGone {
		t.Fatalf("removing game 2: %+v %v %v", r, ok, err)
	}
}
