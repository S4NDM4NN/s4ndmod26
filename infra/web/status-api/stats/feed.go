package stats

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"s4ndmod26/status-api/replay"
)

// FeedEvent is one line of a game's event feed.
type FeedEvent struct {
	TMs        int    `json:"t_ms"` // since the recording started
	Type       string `json:"type"` // kill teamkill suicide revive plant defuse capture steal return denial join leave end
	Actor      string `json:"actor,omitempty"`
	ActorTeam  string `json:"actor_team,omitempty"` // axis | allies
	ActorSlot  int    `json:"-"`
	Target     string `json:"target,omitempty"`
	TargetTeam string `json:"target_team,omitempty"`
	TargetSlot int    `json:"-"`
	Weapon     string `json:"weapon,omitempty"`
	Headshot   bool   `json:"headshot,omitempty"`
}

func teamName(t int32) string {
	switch t {
	case 1:
		return "axis"
	case 2:
		return "allies"
	}
	return ""
}

// feedTypes maps the analysis' event types to the feed's.  Anything not listed is left out: damage and pack events would bury the
// rest, and HEADSHOT / EXPLOSIVE_KILL / KNIFE_KILL / MULTIKILL are bonus events for the same kill as a KILL.
var feedTypes = map[string]string{
	"KILL":              "kill",
	"TEAMKILL":          "teamkill",
	"SUICIDE":           "suicide",
	"REVIVE":            "revive",
	"OBJECTIVE_PLANT":   "plant",
	"OBJECTIVE_DEFUSE":  "defuse",
	"OBJECTIVE_CAPTURE": "capture",
	"OBJECTIVE_STEAL":   "steal",
	"OBJECTIVE_RETURN":  "return",
	"OBJECTIVE_DENIAL":  "denial",
	"PLAYER_JOIN":       "join",
	"PLAYER_LEAVE":      "leave",
	"MATCH_END":         "end",
}

// FeedFromAnalysis builds the event feed of a game from its replay analysis: who killed whom with what, revives, objective
// events, and players joining or leaving after the start.  Names are the ones the slot's player had at the time.
func FeedFromAnalysis(a *replay.Analysis) []FeedEvent {
	nameAt := func(slot int32, t int32) string {
		p := a.Players[strconv.Itoa(int(slot))]
		if p == nil {
			return ""
		}
		for _, sp := range p.NameHistory {
			if t >= sp.FromMs && (sp.ToMs < 0 || t < sp.ToMs) && sp.Name != "" {
				return sp.Name
			}
		}
		return p.DisplayName
	}
	teamOf := func(slot int32) string {
		if p := a.Players[strconv.Itoa(int(slot))]; p != nil {
			return teamName(p.Team)
		}
		return ""
	}

	type kill struct{ t, actor, target int32 }
	headshots := map[kill]bool{}
	for _, ev := range a.Events {
		if ev.Type == "HEADSHOT" {
			headshots[kill{ev.ServerTimeMs, ev.Actor, ev.Target}] = true
		}
	}

	start := a.Meta.StartTimeMs
	var out []FeedEvent
	for _, ev := range a.Events {
		typ, ok := feedTypes[ev.Type]
		if !ok {
			continue
		}
		// the roster at the start is not news
		if (typ == "join" || typ == "leave") && ev.ServerTimeMs <= start+1000 {
			continue
		}
		// an objective returned because its carrier was killed is already the kill
		if typ == "return" && ev.Target >= 0 {
			continue
		}
		fe := FeedEvent{TMs: int(ev.ServerTimeMs - start), Type: typ, ActorSlot: int(ev.Actor), TargetSlot: -1}
		if fe.TMs < 0 {
			fe.TMs = 0
		}
		if ev.Actor >= 0 {
			fe.Actor, fe.ActorTeam = nameAt(ev.Actor, ev.ServerTimeMs), teamOf(ev.Actor)
		}
		switch typ {
		case "kill", "teamkill", "revive":
			fe.TargetSlot = int(ev.Target)
			if ev.Target >= 0 {
				fe.Target, fe.TargetTeam = nameAt(ev.Target, ev.ServerTimeMs), teamOf(ev.Target)
			}
			if typ != "revive" {
				fe.Weapon = ModName(ev.MeansOfDeath)
				fe.Headshot = headshots[kill{ev.ServerTimeMs, ev.Actor, ev.Target}]
			}
		case "suicide":
			fe.Weapon = ModName(ev.MeansOfDeath)
		}
		out = append(out, fe)
	}
	return out
}

// IngestAnalysis stores the event feed of <replayDir>/<match>.json for a game that has stats.  The analysis is read once (again if
// it was rewritten with a different size).  Returns how many events were stored; 0 with no error when there is nothing to do.
func (s *Store) IngestAnalysis(ctx context.Context, replayDir, match string) (int, error) {
	path := filepath.Join(replayDir, match+".json")
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var gid int64
	if err := tx.QueryRow(ctx, `SELECT id FROM games WHERE match = $1`, match).Scan(&gid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil // the game's stats have not been ingested (yet)
		}
		return 0, err
	}
	name := match + ".analysis"
	if _, err := tx.Exec(ctx, `INSERT INTO ingest_files (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
		return 0, err
	}
	var done int64
	if err := tx.QueryRow(ctx, `SELECT byte_pos FROM ingest_files WHERE name = $1 FOR UPDATE`, name).Scan(&done); err != nil {
		return 0, err
	}
	if done == st.Size() {
		return 0, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var a replay.Analysis
	if err := json.Unmarshal(data, &a); err != nil {
		return 0, err
	}
	events := FeedFromAnalysis(&a)

	if _, err := tx.Exec(ctx, `DELETE FROM game_events WHERE game_id = $1`, gid); err != nil {
		return 0, err
	}
	batch := &pgx.Batch{}
	for i, e := range events {
		batch.Queue(`INSERT INTO game_events (game_id, seq, t_ms, type, actor_slot, actor_name, actor_team, target_slot, target_name, target_team, weapon, headshot)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			gid, i, e.TMs, e.Type, e.ActorSlot, e.Actor, e.ActorTeam, e.TargetSlot, e.Target, e.TargetTeam, e.Weapon, e.Headshot)
	}
	br := tx.SendBatch(ctx, batch)
	for range events {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return 0, err
		}
	}
	if err := br.Close(); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ingest_files SET byte_pos = $2, updated_at = now() WHERE name = $1`, name, st.Size()); err != nil {
		return 0, err
	}
	return len(events), tx.Commit(ctx)
}

// IngestFeeds ingests the event feed of every game that has a stats file in statsDir.
func (s *Store) IngestFeeds(ctx context.Context, statsDir, replayDir string) {
	entries, err := os.ReadDir(statsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		match := strings.TrimSuffix(e.Name(), ".jsonl")
		n, err := s.IngestAnalysis(ctx, replayDir, match)
		if err != nil {
			log.Printf("stats: event feed of %s: %v", match, err)
			continue
		}
		if n > 0 {
			log.Printf("stats: %s: stored %d feed event(s)", match, n)
		}
	}
}

// Feed loads a game's event feed in order.
func (s *Store) Feed(ctx context.Context, match string) ([]FeedEvent, error) {
	var gid int64
	if err := s.pool.QueryRow(ctx, `SELECT id FROM games WHERE match = $1`, match).Scan(&gid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoGame
		}
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t_ms, type, actor_name, actor_team, target_name, target_team, weapon, headshot
		FROM game_events WHERE game_id = $1 ORDER BY seq`, gid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeedEvent{}
	for rows.Next() {
		var e FeedEvent
		if err := rows.Scan(&e.TMs, &e.Type, &e.Actor, &e.ActorTeam, &e.Target, &e.TargetTeam, &e.Weapon, &e.Headshot); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
