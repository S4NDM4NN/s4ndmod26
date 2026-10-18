package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testAnalysis() *Analysis {
	return &Analysis{
		Meta: AnalysisMeta{
			Map: "mp_beach", Gametype: 5, GametypeName: "Objective", DurationMs: 600000,
			POTG:         &POTGInfo{Actor: 3, Score: 90},
			Highlights:   []HighlightInfo{{Player: 4, Score: 40}, {Player: 9, Score: 10}},
			GeneratedAt:  "2026-10-17T00:00:00Z",
			MatchStartAt: "2026-10-16T23:50:00Z",
		},
		Players: map[string]*PlayerInfo{"3": {DisplayName: "Bob"}, "4": {DisplayName: "Ann"}},
		Events:  []AnalysisEvent{{Type: "KILL"}, {Type: "SPAWN"}, {Type: "KILL"}},
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize("game1", testAnalysis())
	if s.Name != "game1" || s.Map != "mp_beach" || s.GametypeName != "Objective" || s.DurationMs != 600000 {
		t.Errorf("basic fields wrong: %+v", s)
	}
	if s.KillCount != 2 || s.PlayerCount != 2 {
		t.Errorf("kills=%d players=%d, want 2 and 2", s.KillCount, s.PlayerCount)
	}
	if !s.HasPOTG || s.PotgPlayer != "Bob" {
		t.Errorf("potg = %v %q, want true Bob", s.HasPOTG, s.PotgPlayer)
	}
	if len(s.Highlights) != 2 || s.Highlights[0].Name != "Ann" || s.Highlights[1].Name != "" {
		t.Errorf("highlights = %+v (a player missing from the table keeps no name)", s.Highlights)
	}
	if s.HasRPL {
		t.Error("HasRPL is decided at list time and must not be stored")
	}
}

func writeJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSummaryUsesSidecar(t *testing.T) {
	dir := t.TempDir()
	// no analysis file at all: the sidecar alone is enough, which is the point of it
	writeJSON(t, filepath.Join(dir, "g1.summary.json"), Summary{Name: "g1", Map: "from_sidecar"})
	s, ok := LoadSummary(dir, "g1")
	if !ok || s.Map != "from_sidecar" {
		t.Fatalf("got %+v ok=%v", s, ok)
	}
}

func TestLoadSummaryBackfillsSidecar(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "g2.json"), testAnalysis())
	s, ok := LoadSummary(dir, "g2")
	if !ok || s.Map != "mp_beach" || s.Name != "g2" {
		t.Fatalf("got %+v ok=%v", s, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "g2.summary.json")); err != nil {
		t.Errorf("the sidecar was not written for next time: %v", err)
	}
	// and a second load now works from the sidecar alone
	os.Remove(filepath.Join(dir, "g2.json"))
	if s2, ok := LoadSummary(dir, "g2"); !ok || s2.Map != "mp_beach" {
		t.Errorf("second load: %+v ok=%v", s2, ok)
	}
}

func TestLoadSummaryBadFiles(t *testing.T) {
	dir := t.TempDir()
	if _, ok := LoadSummary(dir, "missing"); ok {
		t.Error("a game with no files must not be listed")
	}
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644)
	if _, ok := LoadSummary(dir, "bad"); ok {
		t.Error("an unreadable analysis must not be listed")
	}
	// a sidecar that belongs to a different game (copied or renamed) is ignored in favour of the analysis
	writeJSON(t, filepath.Join(dir, "g3.summary.json"), Summary{Name: "other", Map: "wrong"})
	writeJSON(t, filepath.Join(dir, "g3.json"), testAnalysis())
	if s, ok := LoadSummary(dir, "g3"); !ok || s.Map != "mp_beach" || s.Name != "g3" {
		t.Errorf("got %+v ok=%v", s, ok)
	}
	// a corrupt sidecar is rebuilt
	os.WriteFile(filepath.Join(dir, "g4.summary.json"), []byte("garbage"), 0o644)
	writeJSON(t, filepath.Join(dir, "g4.json"), testAnalysis())
	if s, ok := LoadSummary(dir, "g4"); !ok || s.Name != "g4" {
		t.Errorf("got %+v ok=%v", s, ok)
	}
}

func TestSummaryPlayerNames(t *testing.T) {
	a := testAnalysis()
	a.Players["4"].NameHistory = []NameSpan{{Name: "Ann"}, {Name: "annie"}}
	a.Players["7"] = &PlayerInfo{DisplayName: "Player 7"} // no real name recorded
	a.Players["8"] = &PlayerInfo{DisplayName: "ann"}      // same name, different case
	got := Summarize("g", a).Players
	want := []string{"Ann", "annie", "Bob"}
	if len(got) != len(want) {
		t.Fatalf("players = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("players = %q, want %q", got, want)
		}
	}
}

func TestLoadSummaryRebuildsOldSidecar(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "g.json"), testAnalysis())
	old := Summarize("g", testAnalysis())
	old.Players = nil // a sidecar written before player names were kept
	writeJSON(t, filepath.Join(dir, "g.summary.json"), old)
	s, ok := LoadSummary(dir, "g")
	if !ok || len(s.Players) != 2 {
		t.Fatalf("LoadSummary = %+v, %v; want the players filled in from the analysis", s, ok)
	}
}
