package replay

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
)

// HighlightSummary is one player's highlight as the games list shows it.
type HighlightSummary struct {
	Player int    `json:"player"`
	Name   string `json:"name,omitempty"`
	Score  int32  `json:"score"`
}

// Summary is one game's row in the games list.  It is derived from the (large) analysis JSON once,
// when the scanner writes it, and kept in a small "<base>.summary.json" sidecar so listing games
// never has to re-parse the analyses.
type Summary struct {
	Name         string             `json:"name"`
	Map          string             `json:"map"`
	Gametype     int                `json:"gametype"`
	GametypeName string             `json:"gametype_name"`
	DurationMs   int32              `json:"duration_ms"`
	KillCount    int                `json:"kill_count"`
	PlayerCount  int                `json:"player_count"`
	HasHumans    bool               `json:"has_humans"`            // at least one player is not a bot; the games list can hide bot-only games
	HasPOTG      bool               `json:"has_potg"`
	PotgPlayer   string             `json:"potg_player,omitempty"` // who the play of the game belongs to
	Highlights   []HighlightSummary `json:"highlights,omitempty"`  // other real players' own best plays
	Players      []string           `json:"players,omitempty"`     // every name a player used in the game, for searching the list
	HasStats     bool               `json:"has_stats"`             // the game server wrote a stats file for it; set at list time, not stored
	Has3D        bool               `json:"has_3d"`                // the raw .rpl still exists, so the game can be watched in 3D; set at list time, not stored
	HasRPL       bool               `json:"has_rpl"`               // the raw .rpl (and sidecar .txt) still exist; set at list time, not stored
	GeneratedAt  string             `json:"generated_at"`
	MatchStartAt string             `json:"match_start_at,omitempty"`
}

// Summarize builds the list row for an analysis.
func Summarize(base string, a *Analysis) Summary {
	kills := 0
	for _, ev := range a.Events {
		if ev.Type == "KILL" {
			kills++
		}
	}
	potgPlayer := ""
	if p := a.Meta.POTG; p != nil {
		if pi := a.Players[strconv.Itoa(p.Actor)]; pi != nil {
			potgPlayer = pi.DisplayName
		}
	}
	var highlights []HighlightSummary
	for _, h := range a.Meta.Highlights {
		hs := HighlightSummary{Player: h.Player, Score: h.Score}
		if pi := a.Players[strconv.Itoa(h.Player)]; pi != nil {
			hs.Name = pi.DisplayName
		}
		highlights = append(highlights, hs)
	}
	return Summary{
		Players:      playerNames(a),
		Name:         base,
		Map:          a.Meta.Map,
		Gametype:     a.Meta.Gametype,
		GametypeName: a.Meta.GametypeName,
		DurationMs:   a.Meta.DurationMs,
		KillCount:    kills,
		PlayerCount:  len(a.Players),
		HasHumans:    hasHumans(a),
		HasPOTG:      a.Meta.POTG != nil,
		PotgPlayer:   potgPlayer,
		Highlights:   highlights,
		GeneratedAt:  a.Meta.GeneratedAt,
		MatchStartAt: a.Meta.MatchStartAt,
	}
}

// hasHumans reports whether any player in the game is not a bot.  A slot's bot flag rides on its
// PLAYER_JOIN / PLAYER_RENAME events (the latest wins); a slot with none is unknown and counts as human so a
// game is never hidden on a guess.
func hasHumans(a *Analysis) bool {
	bot := map[string]bool{}
	for _, ev := range a.Events {
		if ev.Type == "PLAYER_JOIN" || ev.Type == "PLAYER_RENAME" {
			bot[strconv.Itoa(int(ev.Actor))] = ev.Extra != 0
		}
	}
	for key := range a.Players {
		if !bot[key] {
			return true
		}
	}
	return false
}

// playerNames lists the names players went by in the game (a rename mid-game counts), plain text, each once, so the games
// list can be searched by player.  Slots the recording had no name for ("Player 3") are left out.
func playerNames(a *Analysis) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		n = strings.TrimSpace(n)
		key := strings.ToLower(n)
		if n == "" || seen[key] || strings.HasPrefix(n, "Player ") {
			return
		}
		seen[key] = true
		out = append(out, n)
	}
	for _, pi := range a.Players {
		add(pi.DisplayName)
		for _, h := range pi.NameHistory {
			add(h.Name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// writeFileAtomic writes via a temp file so readers never see a partial file.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeSummary(path string, s Summary) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// LoadSummary returns the game's list row, reading the small sidecar when there is one and
// otherwise (games analyzed before sidecars existed) parsing the full analysis JSON once and
// writing the sidecar for next time.
func LoadSummary(dir, base string) (Summary, bool) {
	sumPath := dir + "/" + base + ".summary.json"
	if data, err := os.ReadFile(sumPath); err == nil {
		var s Summary
		// A sidecar from before player names or the bot flag were kept is rebuilt from the analysis (once; the rebuilt one has them).
		if json.Unmarshal(data, &s) == nil && s.Name == base && (s.Players != nil || s.PlayerCount == 0) &&
			bytes.Contains(data, []byte(`"has_humans"`)) {
			return s, true
		}
	}
	data, err := os.ReadFile(dir + "/" + base + ".json")
	if err != nil {
		return Summary{}, false
	}
	var a Analysis
	if err := json.Unmarshal(data, &a); err != nil {
		return Summary{}, false
	}
	s := Summarize(base, &a)
	_ = writeSummary(sumPath, s) // best effort; it is rebuilt next time if this fails
	return s, true
}
