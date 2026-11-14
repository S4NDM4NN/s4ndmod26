package stats

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"s4ndmod26/status-api/feed"
	"s4ndmod26/status-api/replay"
)

// FeedEvent is one line of a game's event feed (see package feed).
type FeedEvent = feed.Event

// FeedFromAnalysis builds a game's event feed from its replay analysis.
func FeedFromAnalysis(a *replay.Analysis) []FeedEvent { return feed.FromAnalysis(a) }

// ModName is the weapon / cause of death for a meansOfDeath value ("" when unknown).
func ModName(mod int32) string { return feed.ModName(mod) }

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
	return s.IngestFeed(ctx, "", match, st.Size(), func() ([]FeedEvent, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var a replay.Analysis
		if err := json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		return FeedFromAnalysis(&a), nil
	})
}

// FeedDone says whether the event feed of a game (named as the server calls it) has been dealt with.
func (s *Store) FeedDone(ctx context.Context, serverID, match string) (bool, error) {
	var done bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ingest_files WHERE server_id = $1 AND name = $2)`, serverID, match+".analysis").Scan(&done)
	return done, err
}

// HasGame says whether a game (named as the server calls it) has stats.
func (s *Store) HasGame(ctx context.Context, serverID, match string) (bool, error) {
	var has bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM games WHERE match = $1)`, GameName(serverID, match)).Scan(&has)
	return has, err
}

// IngestFeed stores the event feed of a game that has stats.  size identifies the version of the analysis it comes from: load is called
// only when that is not what was stored last.  A load that fails is not retried until size changes (failed is recorded as size -1).
func (s *Store) IngestFeed(ctx context.Context, serverID, match string, size int64, load func() ([]FeedEvent, error)) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var gid int64
	if err := tx.QueryRow(ctx, `SELECT id FROM games WHERE match = $1`, GameName(serverID, match)).Scan(&gid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil // the game's stats have not been ingested (yet)
		}
		return 0, err
	}
	name := match + ".analysis"
	if _, err := tx.Exec(ctx, `INSERT INTO ingest_files (server_id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, serverID, name); err != nil {
		return 0, err
	}
	var done int64
	if err := tx.QueryRow(ctx, `SELECT byte_pos FROM ingest_files WHERE server_id = $1 AND name = $2 FOR UPDATE`, serverID, name).Scan(&done); err != nil {
		return 0, err
	}
	if done == size {
		return 0, nil
	}

	events, err := load()
	if err != nil {
		if serverID == "" {
			return 0, err
		}
		// another server's replay that cannot be read: note it and leave it (the error is only logged)
		log.Printf("stats: event feed of %s from %s: %v", match, serverID, err)
		if _, err := tx.Exec(ctx, `UPDATE ingest_files SET byte_pos = -1, updated_at = now() WHERE server_id = $1 AND name = $2`, serverID, name); err != nil {
			return 0, err
		}
		return 0, tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM game_events WHERE game_id = $1`, gid); err != nil {
		return 0, err
	}
	batch := &pgx.Batch{}
	for i, e := range events {
		var px, py, pz *int32
		if e.Pos != nil {
			px, py, pz = &e.Pos[0], &e.Pos[1], &e.Pos[2]
		}
		batch.Queue(`INSERT INTO game_events (game_id, seq, t_ms, type, actor_slot, actor_name, actor_team, target_slot, target_name, target_team, weapon, headshot, pos_x, pos_y, pos_z)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			gid, i, e.TMs, e.Type, e.ActorSlot, e.Actor, e.ActorTeam, e.TargetSlot, e.Target, e.TargetTeam, e.Weapon, e.Headshot, px, py, pz)
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
	if _, err := tx.Exec(ctx, `UPDATE ingest_files SET byte_pos = $3, updated_at = now() WHERE server_id = $1 AND name = $2`, serverID, name, size); err != nil {
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
		SELECT t_ms, type, actor_slot, actor_name, actor_team, target_slot, target_name, target_team, weapon, headshot, pos_x, pos_y, pos_z
		FROM game_events WHERE game_id = $1 ORDER BY seq`, gid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeedEvent{}
	for rows.Next() {
		var e FeedEvent
		var px, py, pz *int32
		if err := rows.Scan(&e.TMs, &e.Type, &e.ActorSlot, &e.Actor, &e.ActorTeam, &e.TargetSlot, &e.Target, &e.TargetTeam, &e.Weapon, &e.Headshot, &px, &py, &pz); err != nil {
			return nil, err
		}
		if px != nil && py != nil && pz != nil {
			e.Pos = &[3]int32{*px, *py, *pz}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
