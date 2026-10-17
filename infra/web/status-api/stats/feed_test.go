package stats

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"s4ndmod26/status-api/replay"
)

func feedAnalysis() *replay.Analysis {
	return &replay.Analysis{
		Meta: replay.AnalysisMeta{StartTimeMs: 1000},
		Players: map[string]*replay.PlayerInfo{
			"1": {DisplayName: "Ann", Team: 1},
			"2": {DisplayName: "Bob", Team: 2},
			// slot 3 was Cy, then somebody else took it at 5000
			"3": {DisplayName: "Dee", Team: 2, NameHistory: []replay.NameSpan{{FromMs: 1000, ToMs: 5000, Name: "Cy"}, {FromMs: 5000, ToMs: -1, Name: "Dee"}}},
		},
		Events: []replay.AnalysisEvent{
			{ServerTimeMs: 1000, Type: "PLAYER_JOIN", Actor: 1, Target: -1, Name: "Ann"}, // the starting roster: left out
			{ServerTimeMs: 2000, Type: "KILL", Actor: 1, Target: 2, MeansOfDeath: 17},
			{ServerTimeMs: 2000, Type: "HEADSHOT", Actor: 1, Target: 2, MeansOfDeath: 17},   // bonus event for the same kill
			{ServerTimeMs: 2500, Type: "KNIFE_KILL", Actor: 1, Target: 2, MeansOfDeath: 12}, // bonus event too
			{ServerTimeMs: 3000, Type: "TEAMKILL", Actor: 2, Target: 3, MeansOfDeath: 18},
			{ServerTimeMs: 3500, Type: "DAMAGE", Actor: 1, Target: 2, Score: 20}, // too noisy
			{ServerTimeMs: 4000, Type: "SUICIDE", Actor: 2, Target: 2, MeansOfDeath: 55},
			{ServerTimeMs: 4500, Type: "REVIVE", Actor: 2, Target: 3},
			{ServerTimeMs: 5000, Type: "OBJECTIVE_PLANT", Actor: 1, Target: -1},
			{ServerTimeMs: 5100, Type: "OBJECTIVE_RETURN", Actor: 2, Target: 1}, // a carrier kill: already the kill
			{ServerTimeMs: 5200, Type: "OBJECTIVE_RETURN", Actor: 2, Target: -1},
			{ServerTimeMs: 6000, Type: "PLAYER_LEAVE", Actor: 3, Target: -1},
			{ServerTimeMs: 7000, Type: "KILL", Actor: 3, Target: 1, MeansOfDeath: 15}, // slot 3 is Dee by now
			{ServerTimeMs: 9000, Type: "MATCH_END", Actor: -1, Target: -1},
		},
	}
}

func TestFeedFromAnalysis(t *testing.T) {
	feed := FeedFromAnalysis(feedAnalysis())
	var types []string
	for _, e := range feed {
		types = append(types, e.Type)
	}
	want := []string{"kill", "teamkill", "suicide", "revive", "plant", "return", "leave", "kill", "end"}
	if len(types) != len(want) {
		t.Fatalf("types = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("types = %v, want %v", types, want)
		}
	}
	k := feed[0]
	if k.Actor != "Ann" || k.ActorTeam != "axis" || k.Target != "Bob" || k.TargetTeam != "allies" || k.Weapon != "MP-40" || !k.Headshot || k.TMs != 1000 {
		t.Errorf("first kill: %+v", k)
	}
	if tk := feed[1]; tk.Actor != "Bob" || tk.Target != "Cy" || tk.Weapon != "Thompson" || tk.Headshot {
		t.Errorf("a slot's name is the one it had at the time (Cy, not Dee): %+v", tk)
	}
	if s := feed[2]; s.Actor != "Bob" || s.Weapon != "Falling" || s.Target != "" {
		t.Errorf("suicide: %+v", s)
	}
	if late := feed[7]; late.Actor != "Dee" {
		t.Errorf("after the swap slot 3 is Dee: %+v", late)
	}
	if feed[8].TMs != 8000 {
		t.Errorf("times are since the recording started: %d", feed[8].TMs)
	}
}

func TestIngestAnalysisAndFeed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	stats := t.TempDir()
	replays := t.TempDir()
	const match = "replays_20261017_012254"

	writeAnalysis := func(a *replay.Analysis) {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(replays, match+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeAnalysis(feedAnalysis())

	// no stats for the game yet: nothing to attach the feed to
	if n, err := s.IngestAnalysis(ctx, replays, match); err != nil || n != 0 {
		t.Fatalf("before the stats: stored %d, %v", n, err)
	}
	writeStats(t, stats, match+".jsonl", line("round_end", 1, 1, "Ann", "axis", 4, 2, 30, ``))
	s.IngestDir(ctx, stats)

	n, err := s.IngestAnalysis(ctx, replays, match)
	if err != nil || n != 9 {
		t.Fatalf("stored %d, %v", n, err)
	}
	feed, err := s.Feed(ctx, match)
	if err != nil || len(feed) != 9 || feed[0].Type != "kill" || feed[8].Type != "end" || feed[0].Weapon != "MP-40" || !feed[0].Headshot {
		t.Fatalf("feed: %v %+v", err, feed)
	}
	if n, err := s.IngestAnalysis(ctx, replays, match); err != nil || n != 0 {
		t.Errorf("unchanged analysis was read again: %d %v", n, err)
	}

	// the objective events of the feed count towards the player (Ann planted in the analysis)
	g, err := s.Game(ctx, match)
	if err != nil || len(g.Players) != 1 || g.Players[0].Objectives == nil || g.Players[0].Objectives.Plants != 1 || g.Players[0].Objectives.Total() != 1 {
		t.Errorf("Ann's objectives: %v %+v", err, g.Players)
	}
	found := false
	for _, a := range g.Awards {
		found = found || (a.Key == "objectives" && a.Player == "Ann")
	}
	if !found {
		t.Errorf("an objective hero award for Ann: %+v", g.Awards)
	}

	// the analysis is rewritten (a longer game): the feed is replaced, not doubled
	a := feedAnalysis()
	a.Events = append(a.Events, replay.AnalysisEvent{ServerTimeMs: 9500, Type: "KILL", Actor: 2, Target: 1, MeansOfDeath: 18})
	writeAnalysis(a)
	if n, err := s.IngestAnalysis(ctx, replays, match); err != nil || n != 10 {
		t.Fatalf("rewritten: stored %d, %v", n, err)
	}
	if feed, _ = s.Feed(ctx, match); len(feed) != 10 {
		t.Errorf("feed has %d events, want 10", len(feed))
	}

	// the replay (and its analysis) is pruned: the feed stays
	os.Remove(filepath.Join(replays, match+".json"))
	s.IngestFeeds(ctx, stats, replays)
	if feed, _ = s.Feed(ctx, match); len(feed) != 10 {
		t.Errorf("pruning the analysis must not drop the feed: %d", len(feed))
	}
	if _, err := s.Feed(ctx, "nope"); err != ErrNoGame {
		t.Errorf("unknown game: %v", err)
	}
}
