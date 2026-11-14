package stats

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// srcMatchOK is what a match name from another server may look like (it ends up in URLs and in other names).
var srcMatchOK = regexp.MustCompile(`^[A-Za-z0-9_-]{1,60}$`)

// Source is one stats file: how big it is now and a way to read it from a position to its end.  The local folder and a remote
// agent both provide one.
type Source struct {
	Size int64
	Read func(pos int64) ([]byte, error)
}

// GameName is the match name a game is stored under: the server's own name for a local game, "<server_id>-<name>" for one pulled
// from a server, so names from different servers never meet.
func GameName(serverID, match string) string {
	if serverID == "" {
		return match
	}
	return serverID + "-" + match
}

// IngestFile adds the lines of <dir>/<name> that have not been read yet.  The game server only ever appends whole lines,
// so the position reached is remembered per file and a half-written last line waits for the next pass.  Safe to run
// repeatedly and from several processes: each line is stored at most once (by its offset in the file).
// Returns how many lines were stored.
func (s *Store) IngestFile(ctx context.Context, dir, name string) (int, error) {
	path := filepath.Join(dir, name)
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return s.Ingest(ctx, "", PoolOfficial, name, Source{Size: st.Size(), Read: func(pos int64) ([]byte, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		return io.ReadAll(f)
	}})
}

// Pools keep stats apart (see migration 009).  The local folder is our own server, so it is official.
const (
	PoolOfficial  = "official"
	PoolCommunity = "community"
)

// Ingest is IngestFile for any Source.  serverID is the registry id of the server the file came from ("" for the local folder); pool
// is the pool a game of it counts in (only used when the game is first stored).
func (s *Store) Ingest(ctx context.Context, serverID, pool, name string, src Source) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `INSERT INTO ingest_files (server_id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, serverID, name); err != nil {
		return 0, err
	}
	var pos int64
	if err := tx.QueryRow(ctx, `SELECT byte_pos FROM ingest_files WHERE server_id = $1 AND name = $2 FOR UPDATE`, serverID, name).Scan(&pos); err != nil {
		return 0, err
	}
	if src.Size < pos {
		log.Printf("stats: %s is shorter than what was read (%d < %d); reading it again from the start", name, src.Size, pos)
		pos = 0
	}
	if src.Size == pos {
		return 0, nil
	}
	data, err := src.Read(pos)
	if err != nil {
		return 0, err
	}
	end := bytes.LastIndexByte(data, '\n') + 1 // only whole lines
	if end == 0 {
		return 0, nil
	}

	stem := strings.TrimSuffix(name, ".jsonl")
	games := map[string]int64{}
	pids := map[string]bool{}
	stored := 0
	for off := 0; off < end; {
		nl := bytes.IndexByte(data[off:end], '\n')
		raw := bytes.TrimSpace(data[off : off+nl])
		lineOff := pos + int64(off)
		off += nl + 1
		if len(raw) == 0 {
			continue
		}
		l, err := ParseLine(raw)
		if err != nil {
			log.Printf("stats: %s at byte %d: skipping a line: %v", name, lineOff, err)
			continue
		}
		if l.Match == "" {
			l.Match = stem
		}
		if serverID != "" && !srcMatchOK.MatchString(l.Match) {
			log.Printf("stats: %s at byte %d: skipping a line with an odd match name", name, lineOff)
			continue // another server's data: only plain names
		}
		gid, ok := games[l.Match]
		if !ok {
			var started *time.Time
			if t, ok := MatchStart(l.Match); ok {
				started = &t
			}
			err := tx.QueryRow(ctx, `
				INSERT INTO games (match, map, started_at, server_id, src_match, pool) VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (match) DO UPDATE SET map = CASE WHEN games.map = '' THEN EXCLUDED.map ELSE games.map END
				RETURNING id`, GameName(serverID, l.Match), l.Map, started, serverID, l.Match, pool).Scan(&gid)
			if err != nil {
				return stored, err
			}
			games[l.Match] = gid
		}
		var lk LongestKill
		if l.LongestKill != nil {
			lk = *l.LongestKill
		}
		var pid int64
		err = tx.QueryRow(ctx, `
			INSERT INTO game_players (game_id, src_offset, event, round, slot, name, bot, team, class, kills, deaths, suicides,
				teamkills, gibs, revives, damage_given, damage_received, team_damage, score,
				team_gibs, goombas, poisons, knife_kills, kill_streak, death_streak, double_kills, triple_kills, quad_kills,
				first_blood, first_death, play_ms, name_raw, longest_kill, longest_kill_weapon, longest_kill_victim, pid)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,
				$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36)
			ON CONFLICT (game_id, src_offset) DO NOTHING
			RETURNING id`,
			gid, lineOff, l.Event, l.Round, l.Slot, l.Name, l.Bot, l.Team, l.Class, l.Kills, l.Deaths, l.Suicides,
			l.TeamKills, l.Gibs, l.Revives, l.DamageGiven, l.DamageReceived, l.TeamDamage, l.Score,
			l.TeamGibs, l.Goombas, l.Poisons, l.KnifeKills, l.KillStreak, l.DeathStreak, l.DoubleKills, l.TripleKills, l.QuadKills,
			l.FirstBlood, l.FirstDeath, l.PlayMs, l.NameRaw, lk.Dist, lk.Weapon, lk.Victim, l.PlayerID).Scan(&pid)
		if err == pgx.ErrNoRows {
			continue // already stored
		}
		if err != nil {
			return stored, err
		}
		if l.Event == "round_end" && l.Winner != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO game_rounds (game_id, round, winner) VALUES ($1,$2,$3)
				ON CONFLICT (game_id, round) DO UPDATE SET winner = EXCLUDED.winner`, gid, l.Round, l.Winner); err != nil {
				return stored, err
			}
		}
		batch := &pgx.Batch{}
		for _, w := range WeaponOrder {
			v, ok := l.Weapons[w]
			if !ok {
				continue
			}
			batch.Queue(`INSERT INTO game_player_weapons (player_id, weapon, hits, atts, kills, deaths, headshots) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				pid, w, v[0], v[1], v[2], v[3], v[4])
		}
		for _, v := range l.Victims {
			if v.N > 0 {
				batch.Queue(`INSERT INTO game_player_victims (player_id, victim_slot, victim_name, kills) VALUES ($1,$2,$3,$4)
					ON CONFLICT (player_id, victim_slot) DO NOTHING`, pid, v.Slot, v.Name, v.N)
			}
		}
		br := tx.SendBatch(ctx, batch)
		for i := 0; i < batch.Len(); i++ {
			if _, err := br.Exec(); err != nil {
				br.Close()
				return stored, err
			}
		}
		if err := br.Close(); err != nil {
			return stored, err
		}
		if l.PlayerID != "" {
			pids[l.PlayerID] = true
		}
		stored++
	}
	if err := markStale(ctx, tx, pids); err != nil {
		return stored, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ingest_files SET byte_pos = $3, updated_at = now() WHERE server_id = $1 AND name = $2`, serverID, name, pos+int64(end)); err != nil {
		return stored, err
	}
	return stored, tx.Commit(ctx)
}

// IngestDir ingests every *.jsonl in dir; a file that fails does not stop the others.
func (s *Store) IngestDir(ctx context.Context, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("stats: reading %s: %v", dir, err)
		}
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		n, err := s.IngestFile(ctx, dir, name)
		if err != nil {
			log.Printf("stats: ingesting %s: %v", name, err)
			continue
		}
		if n > 0 {
			log.Printf("stats: %s: stored %d line(s)", name, n)
		}
	}
}

// Run keeps ingesting dir (and the event feeds from replayDir) until ctx ends: it waits for the database, applies the schema, then
// rescans every interval.
func (s *Store) Run(ctx context.Context, dir, replayDir string, interval time.Duration) {
	for {
		if err := s.Migrate(ctx); err == nil {
			break
		} else {
			log.Printf("stats: database not ready: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	for {
		s.IngestDir(ctx, dir)
		s.IngestFeeds(ctx, dir, replayDir)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// PurgeServer deletes everything pulled from a server: its games (players, weapons, rounds and the event feeds go with them) and
// where it was read up to.  Returns how many games went.  The local folder (server_id "") cannot be purged this way.
func (s *Store) PurgeServer(ctx context.Context, serverID string) (int64, error) {
	if serverID == "" {
		return 0, errors.New("stats: a server id is needed")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// whose totals change: everyone who played there
	if _, err := tx.Exec(ctx, `INSERT INTO player_cache (pid, summary, stale)
		SELECT DISTINCT p.pid, '{}'::jsonb, true FROM game_players p JOIN games g ON g.id = p.game_id WHERE g.server_id = $1 AND p.pid <> ''
		ON CONFLICT (pid) DO UPDATE SET stale = true`, serverID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM replay_index WHERE server_id = $1`, serverID); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM games WHERE server_id = $1`, serverID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ingest_files WHERE server_id = $1`, serverID); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// CountGames is how many games came from a server.
func (s *Store) CountGames(ctx context.Context, serverID string) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM games WHERE server_id = $1`, serverID).Scan(&n)
	return n, err
}

// markStale notes that these players' cached totals are out of date.
func markStale(ctx context.Context, tx pgx.Tx, pids map[string]bool) error {
	for pid := range pids {
		if _, err := tx.Exec(ctx, `INSERT INTO player_cache (pid, summary, stale) VALUES ($1, '{}', true) ON CONFLICT (pid) DO UPDATE SET stale = true`, pid); err != nil {
			return err
		}
	}
	return nil
}
