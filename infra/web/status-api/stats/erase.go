package stats

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DeletedName is what an erased player is called in every game they played.
const DeletedName = "Deleted Player"

// KeyHash is what is remembered of an erased key (see migration 014).
func KeyHash(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// EraseResult says what an erasure touched (or, for a preview, would touch).
type EraseResult struct {
	Games  int `json:"games"`  // games the key played
	Rows   int `json:"rows"`   // stats rows (one per round) made anonymous
	Events int `json:"events"` // kill-feed events whose name was replaced
	Index  int `json:"index"`  // replay index rows whose player names were replaced
}

type played struct {
	id        int64
	game      int64
	slot      int
	name, raw string
}

func keyRows(ctx context.Context, q pgx.Tx, key string) ([]played, error) {
	rows, err := q.Query(ctx, `SELECT id, game_id, slot, name, name_raw FROM game_players WHERE pid = $1 FOR UPDATE`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []played
	for rows.Next() {
		var p played
		if err := rows.Scan(&p.id, &p.game, &p.slot, &p.name, &p.raw); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PreviewErase counts what EraseKey would change, and changes nothing.
func (s *Store) PreviewErase(ctx context.Context, key string) (EraseResult, error) {
	var r EraseResult
	err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT game_id), count(*) FROM game_players WHERE pid = $1`, key).Scan(&r.Games, &r.Rows)
	if err != nil {
		return r, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM game_events e WHERE EXISTS (
			SELECT 1 FROM game_players p WHERE p.pid = $1 AND p.game_id = e.game_id
			  AND ((e.actor_slot = p.slot AND e.actor_name IN (p.name, p.name_raw)) OR (e.target_slot = p.slot AND e.target_name IN (p.name, p.name_raw))))`, key).Scan(&r.Events)
	return r, err
}

// EraseKey makes a player anonymous: every stats row of the key loses the key and gets DeletedName, and the same player's name
// is replaced where the games name people by name only (the kill feed, victim lists, a kill's victim, the replay index).  Other
// players' kills, deaths and results stay; the key's totals and cache go; a hash of the key is remembered so that reading the
// game servers' stats files again does not bring the player back (Ingest checks it).
func (s *Store) EraseKey(ctx context.Context, key string) (EraseResult, error) {
	var res EraseResult
	if !pidRe.MatchString(key) {
		return res, fmt.Errorf("that is not a player key")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	rows, err := keyRows(ctx, tx, key)
	if err != nil {
		return res, err
	}
	games := map[int64]bool{}
	names := map[int64]map[string]bool{} // per game: the names this player used in it
	for _, p := range rows {
		games[p.game] = true
		if names[p.game] == nil {
			names[p.game] = map[string]bool{}
		}
		for _, n := range []string{p.name, p.raw} {
			if n != "" && n != DeletedName {
				names[p.game][n] = true
			}
		}
		for _, col := range []string{"actor", "target"} {
			tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE game_events SET %[1]s_name = $4 WHERE game_id = $1 AND %[1]s_slot = $2 AND %[1]s_name IN ($3, $5)`, col),
				p.game, p.slot, p.name, DeletedName, p.raw)
			if err != nil {
				return res, err
			}
			res.Events += int(tag.RowsAffected())
		}
		// what other players' lines say about this one: victims by slot and name, and "longest kill" victims
		if _, err := tx.Exec(ctx, `UPDATE game_player_victims v SET victim_name = $4
			FROM game_players o WHERE o.id = v.player_id AND o.game_id = $1 AND v.victim_slot = $2 AND v.victim_name IN ($3, $5)`,
			p.game, p.slot, p.name, DeletedName, p.raw); err != nil {
			return res, err
		}
	}
	for g, set := range names {
		for n := range set {
			if _, err := tx.Exec(ctx, `UPDATE game_players SET longest_kill_victim = $3 WHERE game_id = $1 AND longest_kill_victim = $2`, g, n, DeletedName); err != nil {
				return res, err
			}
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE game_players SET pid = '', name = $2, name_raw = $2 WHERE pid = $1`, key, DeletedName)
	if err != nil {
		return res, err
	}
	res.Rows = int(tag.RowsAffected())
	res.Games = len(games)

	// the replay index keeps the names the replay used
	for g, set := range names {
		var match string
		if err := tx.QueryRow(ctx, `SELECT match FROM games WHERE id = $1`, g).Scan(&match); err != nil {
			return res, err
		}
		var raw []byte
		var sid, name string
		err := tx.QueryRow(ctx, `SELECT server_id, name, summary FROM replay_index WHERE match = $1 FOR UPDATE`, match).Scan(&sid, &name, &raw)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return res, err
		}
		if out, changed := redactSummary(raw, set); changed {
			if _, err := tx.Exec(ctx, `UPDATE replay_index SET summary = $3 WHERE server_id = $1 AND name = $2`, sid, name, out); err != nil {
				return res, err
			}
			res.Index++
		}
	}

	// the totals of everyone who shared a game with the player are recomputed; the player's own cache goes
	if _, err := tx.Exec(ctx, `INSERT INTO player_cache (pid, summary, stale)
		SELECT DISTINCT p.pid, '{}'::jsonb, true FROM game_players p WHERE p.game_id = ANY($1) AND p.pid <> ''
		ON CONFLICT (pid) DO UPDATE SET stale = true`, keysOf(games)); err != nil {
		return res, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM player_cache WHERE pid = $1`, key); err != nil {
		return res, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO erased_keys (key_hash) VALUES ($1) ON CONFLICT DO NOTHING`, KeyHash(key)); err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

func keysOf(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// redactSummary replaces the given names in a replay summary (the player list, the play of the game's player, the highlights).
func redactSummary(raw []byte, names map[string]bool) ([]byte, bool) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw, false
	}
	changed := false
	if list, ok := m["players"].([]any); ok {
		for i, v := range list {
			if s, ok := v.(string); ok && names[s] {
				list[i], changed = DeletedName, true
			}
		}
	}
	if s, ok := m["potg_player"].(string); ok && names[s] {
		m["potg_player"], changed = DeletedName, true
	}
	if hl, ok := m["highlights"].([]any); ok {
		for _, h := range hl {
			if hm, ok := h.(map[string]any); ok {
				if s, ok := hm["name"].(string); ok && names[s] {
					hm["name"], changed = DeletedName, true
				}
			}
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw, false
	}
	return out, true
}

// erasedKeys tells which of the keys were erased; used while ingesting.
type erasedSet struct {
	ctx  context.Context
	tx   pgx.Tx
	memo map[string]bool
}

func (e *erasedSet) has(key string) bool {
	if key == "" {
		return false
	}
	if v, ok := e.memo[key]; ok {
		return v
	}
	var one int
	err := e.tx.QueryRow(e.ctx, `SELECT 1 FROM erased_keys WHERE key_hash = $1`, KeyHash(key)).Scan(&one)
	e.memo[key] = err == nil
	return err == nil
}

// redactErasedSlots, run after a game's lines or events were stored, replaces the names of players already made anonymous (their
// rows say DeletedName) where the kill feed and the victim lists name people by slot.  A slot a different player used later in the
// same game is hidden too: a name too many is hidden rather than one too few.
func redactErasedSlots(ctx context.Context, tx pgx.Tx, game int64) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM game_players WHERE game_id = $1 AND name = $2 AND NOT bot`, game, DeletedName).Scan(&n); err != nil || n == 0 {
		return err
	}
	for _, col := range []string{"actor", "target"} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE game_events SET %[1]s_name = $2 WHERE game_id = $1 AND %[1]s_name <> $2
			AND %[1]s_slot IN (SELECT slot FROM game_players WHERE game_id = $1 AND name = $2 AND NOT bot)`, col), game, DeletedName); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE game_player_victims v SET victim_name = $2 FROM game_players o
		WHERE o.id = v.player_id AND o.game_id = $1 AND v.victim_name <> $2
		  AND v.victim_slot IN (SELECT slot FROM game_players WHERE game_id = $1 AND name = $2 AND NOT bot)`, game, DeletedName)
	return err
}
