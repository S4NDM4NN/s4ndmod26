package demostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Series is matches played together, in order.
type Series struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`
	Matches []MatchSummary `json:"matches"`
	Owner   string         `json:"-"`
}

var (
	// ErrSeriesNotFound: there is no such series.
	ErrSeriesNotFound = errors.New("demostore: no such series")
	// ErrNotYours: the key may not do that.
	ErrNotYours = errors.New("demostore: not yours")
	// ErrNoSuchMatch: a match named in a request does not exist.
	ErrNoSuchMatch = errors.New("demostore: no such match")
)

// hasUpload reports whether owner has uploaded a demo of the match.
func hasUpload(ctx context.Context, q pgx.Tx, owner, matchID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM demo_files f JOIN demo_uploads u ON u.file_id = f.id
		WHERE f.match_id = $1 AND u.owner = $2)`, matchID, owner).Scan(&ok)
	return ok, err
}

// canEdit reports whether owner may change a series: they made it, or they
// have uploaded a demo of one of the matches in it.
func canEdit(ctx context.Context, q pgx.Tx, owner, seriesID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM demo_series WHERE id = $1 AND owner = $2)
		OR EXISTS (SELECT 1 FROM demo_matches m JOIN demo_files f ON f.match_id = m.id JOIN demo_uploads u ON u.file_id = f.id
			WHERE m.series_id = $1 AND u.owner = $2)`, seriesID, owner).Scan(&ok)
	return ok, err
}

// CreateSeries makes a series of the matches, in that order.  owner must have
// uploaded a demo of each.
func (s *Store) CreateSeries(ctx context.Context, owner, name string, matches []string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	id := newID()
	if _, err := tx.Exec(ctx, `INSERT INTO demo_series (id, name, owner) VALUES ($1, $2, $3)`, id, name, owner); err != nil {
		return "", err
	}
	if err := setMembers(ctx, tx, owner, id, matches, nil); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// UpdateSeries renames a series and sets which matches it holds, in order.
// Any key with a demo in one of its matches may (see canEdit); a match added
// must be one that key has uploaded a demo of.
func (s *Store) UpdateSeries(ctx context.Context, owner, id, name string, matches []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockEditable(ctx, tx, owner, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE demo_series SET name = $2, updated_at = now() WHERE id = $1`, id, name); err != nil {
		return err
	}
	// the matches it holds now stay whoever edits; they are unlinked first so the new order can be written
	keep := map[string]bool{}
	rows, err := tx.Query(ctx, `SELECT id FROM demo_matches WHERE series_id = $1`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			rows.Close()
			return err
		}
		keep[m] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE demo_matches SET series_id = NULL, series_pos = 0 WHERE series_id = $1`, id); err != nil {
		return err
	}
	if err := setMembers(ctx, tx, owner, id, matches, keep); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lockEditable locks a series and checks the key may change it.
func lockEditable(ctx context.Context, tx pgx.Tx, owner, id string) error {
	var who string
	if err := tx.QueryRow(ctx, `SELECT owner FROM demo_series WHERE id = $1 FOR UPDATE`, id).Scan(&who); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSeriesNotFound
		}
		return err
	}
	ok, err := canEdit(ctx, tx, owner, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotYours
	}
	return nil
}

// setMembers puts the matches, in order, into the series.  A match already in
// another series moves over only if owner may edit that one.
func setMembers(ctx context.Context, tx pgx.Tx, owner, id string, matches []string, keep map[string]bool) error {
	for pos, m := range matches {
		var other *string
		if err := tx.QueryRow(ctx, `SELECT series_id FROM demo_matches WHERE id = $1 FOR UPDATE`, m).Scan(&other); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNoSuchMatch, m)
			}
			return err
		}
		// a match already in the series stays whoever edits; adding one takes a demo of it
		if !keep[m] && (other == nil || *other != id) {
			ok, err := hasUpload(ctx, tx, owner, m)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: you have no demo of match %s", ErrNotYours, m)
			}
		}
		if other != nil && *other != id {
			can, err := canEdit(ctx, tx, owner, *other)
			if err != nil {
				return err
			}
			if !can {
				return fmt.Errorf("%w: match %s is in another series", ErrNotYours, m)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE demo_matches SET series_id = $2, series_pos = $3 WHERE id = $1`, m, id, pos); err != nil {
			return err
		}
	}
	return dropEmptySeries(ctx, tx)
}

func dropEmptySeries(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `DELETE FROM demo_series s WHERE NOT EXISTS (SELECT 1 FROM demo_matches m WHERE m.series_id = s.id)`)
	return err
}

// DeleteSeries dissolves a series (its matches stay).  Any key that may edit it may.
func (s *Store) DeleteSeries(ctx context.Context, owner, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockEditable(ctx, tx, owner, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM demo_series WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SeriesRef says which series a match is in and where.
type SeriesRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Pos   int    `json:"pos"`   // 1-based
	Total int    `json:"total"` // matches in the series
	Prev  string `json:"prev,omitempty"`
	Next  string `json:"next,omitempty"`
}

// SeriesOf says which series a match is in; nil if none.
func (s *Store) SeriesOf(ctx context.Context, matchID string) (*SeriesRef, error) {
	var sid *string
	if err := s.pool.QueryRow(ctx, `SELECT series_id FROM demo_matches WHERE id = $1`, matchID).Scan(&sid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if sid == nil {
		return nil, nil
	}
	ref := &SeriesRef{ID: *sid}
	if err := s.pool.QueryRow(ctx, `SELECT name FROM demo_series WHERE id = $1`, *sid).Scan(&ref.Name); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM demo_matches WHERE series_id = $1 ORDER BY series_pos, created_at, id`, *sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	for i, id := range ids {
		if id == matchID {
			ref.Pos = i + 1
			if i > 0 {
				ref.Prev = ids[i-1]
			}
			if i+1 < len(ids) {
				ref.Next = ids[i+1]
			}
		}
	}
	ref.Total = len(ids)
	return ref, rows.Err()
}

// SeriesCreator is who made a series; "" if there is none.
func (s *Store) SeriesCreator(ctx context.Context, id string) (string, error) {
	var who string
	err := s.pool.QueryRow(ctx, `SELECT owner FROM demo_series WHERE id = $1`, id).Scan(&who)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return who, err
}

// GetSeries loads a series with its matches in order; nil if there is none.
func (s *Store) GetSeries(ctx context.Context, id string) (*Series, error) {
	var out Series
	err := s.pool.QueryRow(ctx, `SELECT id, name, owner, created_at, updated_at FROM demo_series WHERE id = $1`, id).
		Scan(&out.ID, &out.Name, &out.Owner, &out.Created, &out.Updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.Matches, err = s.summaries(ctx, `series_id = $1`, `series_pos, created_at, id`, 0, 0, id)
	return &out, err
}

// ListSeries lists series, most recently changed first.
func (s *Store) ListSeries(ctx context.Context, limit, offset int) ([]Series, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM demo_series`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM demo_series ORDER BY updated_at DESC, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Series{}
	for _, id := range ids {
		sr, err := s.GetSeries(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		if sr != nil {
			out = append(out, *sr)
		}
	}
	return out, total, rows.Err()
}

// SeriesOfOwner lists the series a key may edit (id and name, newest first).
func (s *Store) SeriesOfOwner(ctx context.Context, owner string) ([]SeriesBrief, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM demo_series s WHERE owner = $1 OR EXISTS (
		SELECT 1 FROM demo_matches m JOIN demo_files f ON f.match_id = m.id JOIN demo_uploads u ON u.file_id = f.id
		WHERE m.series_id = s.id AND u.owner = $1) ORDER BY updated_at DESC, id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesBrief{}
	for rows.Next() {
		var b SeriesBrief
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SeriesBrief is a series' id and name.
type SeriesBrief struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
