package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeGame makes the four files of an analyzed game started at t, with the summary's bot flag.
func writeGame(t *testing.T, dir string, at time.Time, humans bool) string {
	t.Helper()
	base := at.Format("replays_20060102_150405")
	for _, ext := range []string{".rpl", ".txt", ".json"} {
		if err := os.WriteFile(filepath.Join(dir, base+ext), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := json.Marshal(map[string]any{"name": base, "player_count": 2, "players": []string{"a", "b"}, "has_humans": humans})
	if err := os.WriteFile(filepath.Join(dir, base+".summary.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return base
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestPruneBotOnlyGames(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	oldBots := writeGame(t, dir, now.Add(-30*time.Hour), false)
	oldHumans := writeGame(t, dir, now.Add(-30*time.Hour+time.Minute), true)
	newBots := writeGame(t, dir, now.Add(-2*time.Hour), false)
	veryOld := writeGame(t, dir, now.Add(-11*24*time.Hour), true)

	pruneOld(dir, RetentionConfig{MaxAgeDays: 10, RplMaxAgeDays: -1, BotOnlyMaxAgeDays: 1})

	for _, ext := range []string{".rpl", ".txt", ".json", ".summary.json"} {
		if exists(dir, oldBots+ext) {
			t.Errorf("bot-only game a day old kept its %s", ext)
		}
		if exists(dir, veryOld+ext) {
			t.Errorf("game past the age limit kept its %s", ext)
		}
		if !exists(dir, oldHumans+ext) {
			t.Errorf("game with a human lost its %s", ext)
		}
		if !exists(dir, newBots+ext) {
			t.Errorf("bot-only game under a day old lost its %s", ext)
		}
	}

	// off (-1): bot-only games are kept like any other
	dir = t.TempDir()
	oldBots = writeGame(t, dir, now.Add(-30*time.Hour), false)
	pruneOld(dir, RetentionConfig{MaxAgeDays: 10, RplMaxAgeDays: -1, BotOnlyMaxAgeDays: -1})
	if !exists(dir, oldBots+".rpl") {
		t.Error("bot-only game removed with the setting off")
	}
}
