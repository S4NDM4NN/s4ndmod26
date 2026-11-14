package stats

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"s4ndmod26/status-api/replay"
)

// maxSummaryBytes bounds one summary read from another server.
const maxSummaryBytes = 256 << 10

type indexState struct {
	SumSize int64
	HasRPL  bool
	BaseURL string
}

// replayIndexState is what is stored for a server, by replay name.
func (s *Store) replayIndexState(ctx context.Context, serverID string) (map[string]indexState, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, sum_size, has_rpl, base_url FROM replay_index WHERE server_id = $1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]indexState{}
	for rows.Next() {
		var n string
		var st indexState
		if err := rows.Scan(&n, &st.SumSize, &st.HasRPL, &st.BaseURL); err != nil {
			return nil, err
		}
		out[n] = st
	}
	return out, rows.Err()
}

// putReplay stores (or replaces) a replay's index row.  The summary is the server's own, so only what the list shows is kept, and what
// it says about itself (its name, whether it has stats) is overwritten with what the site knows.
func (s *Store) putReplay(ctx context.Context, serverID, name string, sum replay.Summary, sumSize int64, hasRPL bool, baseURL string) error {
	match := GameName(serverID, name)
	sum.Name = match
	sum.ServerID = serverID
	sum.HasStats, sum.Has3D, sum.HasRPL = false, false, false // set at list time
	var started *time.Time
	if t, ok := MatchStart(name); ok {
		started = &t
	}
	b, err := json.Marshal(sum)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO replay_index (server_id, name, match, summary, sum_size, has_rpl, base_url, started_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (server_id, name) DO UPDATE SET summary = EXCLUDED.summary, sum_size = EXCLUDED.sum_size, has_rpl = EXCLUDED.has_rpl,
			base_url = EXCLUDED.base_url, started_at = EXCLUDED.started_at, updated_at = now()`,
		serverID, name, match, b, sumSize, hasRPL, baseURL, started)
	return err
}

// IndexedReplays lists the replays other servers have, newest first, as the games list shows them.  Pool says which pool each
// belongs to (its game's pool when the game has stats, otherwise the server's).
func (s *Store) IndexedReplays(ctx context.Context) ([]replay.Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.summary, r.has_rpl, EXISTS (SELECT 1 FROM games g WHERE g.match = r.match)
		FROM replay_index r ORDER BY r.started_at DESC NULLS LAST, r.match DESC LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []replay.Summary
	for rows.Next() {
		var raw []byte
		var hasRPL, hasStats bool
		if err := rows.Scan(&raw, &hasRPL, &hasStats); err != nil {
			return nil, err
		}
		var sm replay.Summary
		if json.Unmarshal(raw, &sm) != nil {
			continue
		}
		sm.GametypeName = replay.GametypeName(sm.Gametype)
		sm.Has3D = hasRPL
		sm.HasRPL = hasRPL && sm.HasPOTG
		sm.HasStats = hasStats
		out = append(out, sm)
	}
	return out, rows.Err()
}

// ReplaySource says where a replay's files are: the base address (the server's /v1/replays) and the replay's name on that server.
// ok is false for a name the index does not know (the viewer then uses the local folder).
func (s *Store) ReplaySource(ctx context.Context, match string) (baseURL, name, serverID string, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT base_url, name, server_id FROM replay_index WHERE match = $1`, match).Scan(&baseURL, &name, &serverID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	return baseURL, name, serverID, err == nil, err
}
