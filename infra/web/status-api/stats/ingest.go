package stats

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// IngestFile adds the lines of <dir>/<name> that have not been read yet.  The game server only ever appends whole lines,
// so the position reached is remembered per file and a half-written last line waits for the next pass.  Safe to run
// repeatedly and from several processes: each line is stored at most once (by its offset in the file).
// Returns how many lines were stored.
func (s *Store) IngestFile(ctx context.Context, dir, name string) (int, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `INSERT INTO ingest_files (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
		return 0, err
	}
	var pos int64
	if err := tx.QueryRow(ctx, `SELECT byte_pos FROM ingest_files WHERE name = $1 FOR UPDATE`, name).Scan(&pos); err != nil {
		return 0, err
	}
	if st.Size() < pos {
		log.Printf("stats: %s is shorter than what was read (%d < %d); reading it again from the start", name, st.Size(), pos)
		pos = 0
	}
	if st.Size() == pos {
		return 0, nil
	}
	if _, err := f.Seek(pos, io.SeekStart); err != nil {
		return 0, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return 0, err
	}
	end := bytes.LastIndexByte(data, '\n') + 1 // only whole lines
	if end == 0 {
		return 0, nil
	}

	stem := strings.TrimSuffix(name, ".jsonl")
	games := map[string]int64{}
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
		gid, ok := games[l.Match]
		if !ok {
			var started *time.Time
			if t, ok := MatchStart(l.Match); ok {
				started = &t
			}
			err := tx.QueryRow(ctx, `
				INSERT INTO games (match, map, started_at) VALUES ($1, $2, $3)
				ON CONFLICT (match) DO UPDATE SET map = CASE WHEN games.map = '' THEN EXCLUDED.map ELSE games.map END
				RETURNING id`, l.Match, l.Map, started).Scan(&gid)
			if err != nil {
				return stored, err
			}
			games[l.Match] = gid
		}
		var pid int64
		err = tx.QueryRow(ctx, `
			INSERT INTO game_players (game_id, src_offset, event, round, slot, name, bot, team, class, kills, deaths, suicides,
				teamkills, gibs, revives, damage_given, damage_received, team_damage, score,
				team_gibs, goombas, poisons, knife_kills, kill_streak, death_streak, double_kills, triple_kills, quad_kills,
				first_blood, first_death, play_ms, name_raw)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,
				$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32)
			ON CONFLICT (game_id, src_offset) DO NOTHING
			RETURNING id`,
			gid, lineOff, l.Event, l.Round, l.Slot, l.Name, l.Bot, l.Team, l.Class, l.Kills, l.Deaths, l.Suicides,
			l.TeamKills, l.Gibs, l.Revives, l.DamageGiven, l.DamageReceived, l.TeamDamage, l.Score,
			l.TeamGibs, l.Goombas, l.Poisons, l.KnifeKills, l.KillStreak, l.DeathStreak, l.DoubleKills, l.TripleKills, l.QuadKills,
			l.FirstBlood, l.FirstDeath, l.PlayMs, l.NameRaw).Scan(&pid)
		if err == pgx.ErrNoRows {
			continue // already stored
		}
		if err != nil {
			return stored, err
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
		stored++
	}
	if _, err := tx.Exec(ctx, `UPDATE ingest_files SET byte_pos = $2, updated_at = now() WHERE name = $1`, name, pos+int64(end)); err != nil {
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
