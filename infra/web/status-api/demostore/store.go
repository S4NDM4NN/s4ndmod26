package demostore

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Store is the Postgres side: matches, the demos in them, and who uploaded what.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres (lazily: use Migrate or Ping to find out if it is up).
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// NewStore wraps an existing pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies the schema files not yet applied.  It keeps its own record
// (demo_schema_migrations), apart from the game stats'.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7438202)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7438202)`)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS demo_schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %q: name must start with a number", name)
		}
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM demo_schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO demo_schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Printf("demos: applied migration %s", name)
	}
	return nil
}

// Demo is one stored demo file.
type Demo struct {
	ID           int64     `json:"id"`
	SHA256       string    `json:"sha256"`
	MatchID      string    `json:"match_id"`
	Protocol     int       `json:"protocol"`
	Size         int64     `json:"size"`
	Map          string    `json:"map"`
	ServerID     string    `json:"-"`
	Recorder     string    `json:"recorder"`
	RecorderSlot int       `json:"recorder_slot"`
	Spectator    bool      `json:"spectator"`
	StartMs      int       `json:"start_ms"` // in the demo's own clock
	EndMs        int       `json:"end_ms"`
	Frames       int       `json:"frames"`
	BaseOffsetMs int       `json:"base_offset_ms"` // put on the match's clock: own + this
	Gametype     int       `json:"gametype"`
	Rounds       int       `json:"rounds"`
	Players      []string  `json:"players"`
	Warning      string    `json:"warning,omitempty"`
	CreatedAt    time.Time `json:"added"`
}

// Span is how long the demo runs.
func (d Demo) Span() int { return d.EndMs - d.StartMs }

const demoCols = `id, sha256, match_id, protocol, size, map, server_id, recorder, recorder_slot, spectator, start_ms, end_ms, frames,
	base_offset_ms, gametype, rounds, players, warning, created_at`

func scanDemo(row pgx.Row) (Demo, error) {
	var d Demo
	err := row.Scan(&d.ID, &d.SHA256, &d.MatchID, &d.Protocol, &d.Size, &d.Map, &d.ServerID, &d.Recorder, &d.RecorderSlot, &d.Spectator,
		&d.StartMs, &d.EndMs, &d.Frames, &d.BaseOffsetMs, &d.Gametype, &d.Rounds, &d.Players, &d.Warning, &d.CreatedAt)
	if d.Players == nil {
		d.Players = []string{}
	}
	return d, err
}

// Match is one game and the demos of it.
type Match struct {
	ID        string    `json:"id"`
	Map       string    `json:"map"`
	Version   int       `json:"version"`
	MergedKey string    `json:"-"`
	CreatedAt time.Time `json:"created"`
	UpdatedAt time.Time `json:"updated"`
	Demos     []Demo    `json:"demos"`
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the system has no randomness
	}
	return hex.EncodeToString(b[:])
}

func (s *Store) demosOf(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, where string, args ...any) ([]Demo, error) {
	rows, err := q.Query(ctx, `SELECT `+demoCols+` FROM demo_files WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Demo
	for rows.Next() {
		d, err := scanDemo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DemoBySHA finds a stored demo by content; nil if there is none.
func (s *Store) DemoBySHA(ctx context.Context, sha string) (*Demo, error) {
	d, err := scanDemo(s.pool.QueryRow(ctx, `SELECT `+demoCols+` FROM demo_files WHERE sha256 = $1`, sha))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &d, err
}

// GetMatch loads a match with its demos; nil if there is none.
func (s *Store) GetMatch(ctx context.Context, id string) (*Match, error) {
	var m Match
	err := s.pool.QueryRow(ctx, `SELECT id, map, version, merged_key, created_at, updated_at FROM demo_matches WHERE id = $1`, id).
		Scan(&m.ID, &m.Map, &m.Version, &m.MergedKey, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if m.Demos, err = s.demosOf(ctx, s.pool, `match_id = $1`, id); err != nil {
		return nil, err
	}
	return &m, nil
}

// MatchesOfMap loads every match on a map, with its demos: the candidates a
// new demo of that map might belong to.
func (s *Store) MatchesOfMap(ctx context.Context, mapName string) ([]Match, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, map, version, merged_key, created_at, updated_at FROM demo_matches WHERE map = $1 ORDER BY created_at`, mapName)
	if err != nil {
		return nil, err
	}
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.ID, &m.Map, &m.Version, &m.MergedKey, &m.CreatedAt, &m.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Demos, err = s.demosOf(ctx, s.pool, `match_id = $1`, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// NewDemo is a demo to be stored.
type NewDemo struct {
	SHA256       string
	Protocol     int
	Size         int64
	Map          string
	ServerID     string
	Recorder     string
	RecorderSlot int
	Spectator    bool
	StartMs      int
	EndMs        int
	Frames       int
	BaseOffsetMs int // on the clock of the match it joins
	Gametype     int
	Rounds       int
	Players      []string
	Warning      string
}

// Attachment is what to do with a new demo: which match it joins ("" for a
// new one), and which other matches it turns out to bridge, with the shift
// that puts each one's demos on the joined match's clock.
type Attachment struct {
	MatchID string
	Merge   map[string]int // other match id -> shift onto MatchID's clock
}

// ErrDuplicate is returned by AddDemo when a demo with that content is already stored.
var ErrDuplicate = errors.New("demostore: that demo is already stored")

// AddDemo stores a demo and the claim of owner on it, in one transaction.
// Matches the demo bridges are folded into the one it joins: their demos move
// over (shifted onto its clock) and they are deleted.  Besides the demo, it
// returns the merged outputs that no longer describe a match, to be deleted
// from the blob store.
func (s *Store) AddDemo(ctx context.Context, nd NewDemo, owner, filename string, at Attachment) (Demo, []string, error) {
	if nd.Players == nil {
		nd.Players = []string{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Demo{}, nil, err
	}
	defer tx.Rollback(ctx)

	var stale []string
	matchID := at.MatchID
	if matchID == "" {
		matchID = newID()
		if _, err := tx.Exec(ctx, `INSERT INTO demo_matches (id, map) VALUES ($1, $2)`, matchID, nd.Map); err != nil {
			return Demo{}, nil, err
		}
	}
	// order matters: the other match's demos are moved out before it is deleted
	// (deleting a match deletes its demos)
	others := make([]string, 0, len(at.Merge))
	for other := range at.Merge {
		if other != matchID {
			others = append(others, other)
		}
	}
	sort.Strings(others)
	for _, other := range others {
		var key string
		if err := tx.QueryRow(ctx, `SELECT merged_key FROM demo_matches WHERE id = $1 FOR UPDATE`, other).Scan(&key); err != nil {
			return Demo{}, nil, fmt.Errorf("merge %s: %w", other, err)
		}
		if key != "" {
			stale = append(stale, key)
		}
		if _, err := tx.Exec(ctx, `UPDATE demo_files SET match_id = $1, base_offset_ms = base_offset_ms + $2 WHERE match_id = $3`, matchID, at.Merge[other], other); err != nil {
			return Demo{}, nil, err
		}
		// the match that is left keeps the series of one that is folded into it
		if _, err := tx.Exec(ctx, `UPDATE demo_matches t SET series_id = o.series_id, series_pos = o.series_pos
			FROM demo_matches o WHERE t.id = $1 AND o.id = $2 AND t.series_id IS NULL AND o.series_id IS NOT NULL`, matchID, other); err != nil {
			return Demo{}, nil, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM demo_matches WHERE id = $1`, other); err != nil {
			return Demo{}, nil, err
		}
	}
	if len(others) > 0 {
		if err := dropEmptySeries(ctx, tx); err != nil {
			return Demo{}, nil, err
		}
	}

	row := tx.QueryRow(ctx, `INSERT INTO demo_files (sha256, match_id, protocol, size, map, server_id, recorder, recorder_slot, spectator,
			start_ms, end_ms, frames, base_offset_ms, gametype, rounds, players, warning)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING `+demoCols,
		nd.SHA256, matchID, nd.Protocol, nd.Size, nd.Map, nd.ServerID, nd.Recorder, nd.RecorderSlot, nd.Spectator,
		nd.StartMs, nd.EndMs, nd.Frames, nd.BaseOffsetMs, nd.Gametype, nd.Rounds, nd.Players, nd.Warning)
	d, err := scanDemo(row)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return Demo{}, nil, ErrDuplicate
		}
		return Demo{}, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO demo_uploads (file_id, owner, filename) VALUES ($1, $2, $3)`, d.ID, owner, filename); err != nil {
		return Demo{}, nil, err
	}
	key, err := bumpMatch(ctx, tx, matchID)
	if err != nil {
		return Demo{}, nil, err
	}
	if key != "" {
		stale = append(stale, key)
	}
	if err := tx.Commit(ctx); err != nil {
		return Demo{}, nil, err
	}
	return d, stale, nil
}

// bumpMatch records that a match's demos changed: a new version, and no merged
// output (the old one's key is returned, to be deleted).
func bumpMatch(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var old string
	err := tx.QueryRow(ctx, `UPDATE demo_matches m SET version = m.version + 1, updated_at = now(), merged_key = ''
		FROM (SELECT merged_key FROM demo_matches WHERE id = $1 FOR UPDATE) o
		WHERE m.id = $1 RETURNING o.merged_key`, id).Scan(&old)
	return old, err
}

// AddUpload records that owner also uploaded a demo that is already stored.
// It reports whether that was news (the owner had not claimed it before).
func (s *Store) AddUpload(ctx context.Context, fileID int64, owner, filename string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO demo_uploads (file_id, owner, filename) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, fileID, owner, filename)
	return tag.RowsAffected() == 1, err
}

// Removal says what RemoveUpload did.
type Removal struct {
	SHA256    string // of the demo
	DemoGone  bool   // no one else had uploaded it: its row is gone, so is the file to delete
	MatchID   string
	MatchGone bool     // that was the match's last demo
	Stale     []string // merged outputs to delete
}

// RemoveUpload takes owner's claim on a demo away.  Nothing else changes if
// someone else uploaded the same file; once no one has, the demo goes, and a
// match with no demos goes with it.  ok is false if owner had no such upload.
func (s *Store) RemoveUpload(ctx context.Context, fileID int64, owner string) (r Removal, ok bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, false, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT sha256, match_id FROM demo_files WHERE id = $1 FOR UPDATE`, fileID).Scan(&r.SHA256, &r.MatchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r, false, nil
		}
		return r, false, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM demo_uploads WHERE file_id = $1 AND owner = $2`, fileID, owner)
	if err != nil {
		return r, false, err
	}
	if tag.RowsAffected() == 0 {
		return r, false, nil
	}
	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM demo_uploads WHERE file_id = $1`, fileID).Scan(&left); err != nil {
		return r, false, err
	}
	if left == 0 {
		r.DemoGone = true
		if _, err := tx.Exec(ctx, `DELETE FROM demo_files WHERE id = $1`, fileID); err != nil {
			return r, false, err
		}
		var demos int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM demo_files WHERE match_id = $1`, r.MatchID).Scan(&demos); err != nil {
			return r, false, err
		}
		if demos == 0 {
			r.MatchGone = true
			var key string
			if err := tx.QueryRow(ctx, `DELETE FROM demo_matches WHERE id = $1 RETURNING merged_key`, r.MatchID).Scan(&key); err != nil {
				return r, false, err
			}
			if key != "" {
				r.Stale = append(r.Stale, key)
			}
			if err := dropEmptySeries(ctx, tx); err != nil {
				return r, false, err
			}
		} else {
			key, err := bumpMatch(ctx, tx, r.MatchID)
			if err != nil {
				return r, false, err
			}
			if key != "" {
				r.Stale = append(r.Stale, key)
			}
		}
	}
	return r, true, tx.Commit(ctx)
}

// Upload is one of an uploader's uploads.
type Upload struct {
	FileID     int64     `json:"id"`
	MatchID    string    `json:"match_id"`
	Map        string    `json:"map"`
	Recorder   string    `json:"recorder"`
	Filename   string    `json:"filename"`
	UploadedAt time.Time `json:"uploaded"`
}

// UploadsOf lists what owner uploaded, newest first.
func (s *Store) UploadsOf(ctx context.Context, owner string) ([]Upload, error) {
	rows, err := s.pool.Query(ctx, `SELECT f.id, f.match_id, f.map, f.recorder, u.filename, u.uploaded_at
		FROM demo_uploads u JOIN demo_files f ON f.id = u.file_id WHERE u.owner = $1 ORDER BY u.uploaded_at DESC, f.id DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		var u Upload
		if err := rows.Scan(&u.FileID, &u.MatchID, &u.Map, &u.Recorder, &u.Filename, &u.UploadedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MatchSummary is a match as the list shows it.
type MatchSummary struct {
	ID         string    `json:"id"`
	Map        string    `json:"map"`
	Demos      int       `json:"demos"`
	Recorders  []string  `json:"recorders"`  // who made the demos (not spectators)
	Spectators int       `json:"spectators"` // demos made by someone watching
	Players    []string  `json:"players"`    // everyone named in the demos' rosters
	DurationMs int       `json:"duration_ms"`
	Rounds     int       `json:"rounds"`
	Gametype   int       `json:"gametype"`
	SeriesID   string    `json:"series_id,omitempty"`
	SeriesName string    `json:"series_name,omitempty"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

// ListMatches lists matches, most recently changed first, and how many there are in all.
func (s *Store) ListMatches(ctx context.Context, limit, offset int) ([]MatchSummary, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM demo_matches`).Scan(&total); err != nil {
		return nil, 0, err
	}
	out, err := s.summaries(ctx, `true`, `updated_at DESC, id`, limit, offset)
	return out, total, err
}

// summaries lists the matches satisfying where, with their demos summed up.
// A limit of 0 means no limit.
func (s *Store) summaries(ctx context.Context, where, order string, limit, offset int, args ...any) ([]MatchSummary, error) {
	q := `SELECT m.id, m.map, m.created_at, m.updated_at, COALESCE(m.series_id, ''), COALESCE(r.name, '')
		FROM demo_matches m LEFT JOIN demo_series r ON r.id = m.series_id WHERE ` + where + ` ORDER BY ` + order
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d OFFSET %d`, limit, offset)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []MatchSummary
	for rows.Next() {
		var m MatchSummary
		if err := rows.Scan(&m.ID, &m.Map, &m.Created, &m.Updated, &m.SeriesID, &m.SeriesName); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ds, err := s.demosOf(ctx, s.pool, `match_id = $1`, out[i].ID)
		if err != nil {
			return nil, err
		}
		summarize(&out[i], ds)
	}
	return out, nil
}

func summarize(m *MatchSummary, ds []Demo) {
	m.Demos = len(ds)
	m.Recorders, m.Players = []string{}, []string{}
	seen := map[string]bool{}
	first, last := 1<<30, -1<<30
	for _, d := range ds {
		if d.Spectator {
			m.Spectators++
		} else if d.Recorder != "" {
			m.Recorders = append(m.Recorders, d.Recorder)
		}
		for _, p := range d.Players {
			if p != "" && !seen[p] {
				seen[p] = true
				m.Players = append(m.Players, p)
			}
		}
		if v := d.StartMs + d.BaseOffsetMs; v < first {
			first = v
		}
		if v := d.EndMs + d.BaseOffsetMs; v > last {
			last = v
		}
		if d.Rounds > m.Rounds {
			m.Rounds = d.Rounds
		}
		if d.Gametype != 0 {
			m.Gametype = d.Gametype
		}
	}
	if last >= first {
		m.DurationMs = last - first
	}
	sort.Strings(m.Players)
}

// SetMerged records where the merged output of a match, built for version, is
// kept.  If the match has changed since (or is gone) nothing is recorded and
// ok is false: the output is already out of date.  oldKey is the output it
// replaces, to be deleted.
func (s *Store) SetMerged(ctx context.Context, matchID string, version int, key string) (oldKey string, ok bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var v int
	if err := tx.QueryRow(ctx, `SELECT version, merged_key FROM demo_matches WHERE id = $1 FOR UPDATE`, matchID).Scan(&v, &oldKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	if v != version {
		return "", false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE demo_matches SET merged_key = $2 WHERE id = $1`, matchID, key); err != nil {
		return "", false, err
	}
	return oldKey, true, tx.Commit(ctx)
}
