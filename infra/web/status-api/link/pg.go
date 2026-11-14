package link

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// PG keeps codes, pending keys and links in Postgres next to the accounts they belong to (so deleting an account unlinks its keys).
type PG struct {
	pool *pgxpool.Pool
	Now  func() time.Time
}

// NewPG uses the accounts' pool; call Migrate before use.
func NewPG(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

func (p *PG) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func ctx5() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// Migrate applies the schema files not yet applied.  Safe to run from several processes.
func (p *PG) Migrate(ctx context.Context) error {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7438205)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7438205)`)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS link_schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
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
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM link_schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
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
		if _, err := tx.Exec(ctx, `INSERT INTO link_schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *PG) expire(ctx context.Context, now time.Time) {
	p.pool.Exec(ctx, `DELETE FROM link_codes WHERE expires_at < $1`, now)
	p.pool.Exec(ctx, `DELETE FROM link_pending WHERE asked_at < $1`, now.Add(-PendingTTL))
}

func (p *PG) NewCode(account uint64) (string, time.Time, error) {
	ctx, cancel := ctx5()
	defer cancel()
	now := p.now()
	p.expire(ctx, now)
	var n int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM link_codes WHERE account_id = $1 AND server_id = ''`, int64(account)).Scan(&n); err != nil {
		return "", time.Time{}, err
	}
	if n >= MaxCodes {
		return "", time.Time{}, ErrTooMany
	}
	return p.insertCode(ctx, account, "", now)
}

func (p *PG) NewDirectCode(account uint64) (string, time.Time, error) {
	ctx, cancel := ctx5()
	defer cancel()
	now := p.now()
	p.expire(ctx, now)
	p.pool.Exec(ctx, `DELETE FROM link_codes WHERE account_id = $1 AND server_id = $2`, int64(account), DirectScope)
	return p.insertCode(ctx, account, DirectScope, now)
}

func (p *PG) insertCode(ctx context.Context, account uint64, server string, now time.Time) (string, time.Time, error) {
	cs, err := randomCode()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := now.Add(CodeTTL)
	if _, err := p.pool.Exec(ctx, `INSERT INTO link_codes (hash, account_id, server_id, expires_at) VALUES ($1,$2,$3,$4)`, hashCode(cs), int64(account), server, exp); err != nil {
		return "", time.Time{}, err
	}
	return cs[:4] + "-" + cs[4:], exp, nil
}

// takeCode finds a live code and removes it (single use, even if the rest fails).  scope is "" for a code typed in game and DirectScope for
// one a signed-in page links a key with; a code of the other kind is not accepted (and is spent).
func (p *PG) takeCode(ctx context.Context, tx pgx.Tx, raw, scope string) (uint64, error) {
	c := NormalizeCode(raw)
	if c == "" {
		return 0, ErrBadCode
	}
	var acct int64
	var srv string
	err := tx.QueryRow(ctx, `DELETE FROM link_codes WHERE hash = $1 AND expires_at >= $2 RETURNING account_id, server_id`, hashCode(c), p.now()).Scan(&acct, &srv)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrBadCode
	}
	if err != nil {
		return 0, err
	}
	if srv != scope {
		return 0, ErrBadCode
	}
	return uint64(acct), nil
}

func (p *PG) Claim(rawCode, key, server string) error {
	if NormalizeCode(rawCode) == "" {
		return ErrBadCode
	}
	if !ValidKey(key) {
		return ErrBadKey
	}
	ctx, cancel := ctx5()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	acct, err := p.takeCode(ctx, tx, rawCode, "")
	if err != nil {
		if errors.Is(err, ErrBadCode) {
			tx.Commit(ctx) // a code that was found is spent even when it cannot be used
		}
		return err
	}
	var other int64
	if err := tx.QueryRow(ctx, `SELECT account_id FROM player_links WHERE key = $1`, key).Scan(&other); err == nil && uint64(other) != acct {
		tx.Commit(ctx)
		return ErrKeyLinked
	}
	if _, err := tx.Exec(ctx, `INSERT INTO link_pending (key, account_id, server_id, asked_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (key) DO UPDATE SET account_id = EXCLUDED.account_id, server_id = EXCLUDED.server_id, asked_at = EXCLUDED.asked_at`, key, int64(acct), server, p.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PG) ClaimDirect(account uint64, rawCode, key string) error {
	if NormalizeCode(rawCode) == "" {
		return ErrBadCode
	}
	if !ValidKey(key) {
		return ErrBadKey
	}
	ctx, cancel := ctx5()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	acct, err := p.takeCode(ctx, tx, rawCode, DirectScope)
	if err != nil {
		if errors.Is(err, ErrBadCode) {
			tx.Commit(ctx)
		}
		return err
	}
	if acct != account {
		tx.Commit(ctx) // somebody else's code: spent, and no link
		return ErrBadCode
	}
	var other int64
	err = tx.QueryRow(ctx, `SELECT account_id FROM player_links WHERE key = $1`, key).Scan(&other)
	known := err == nil
	if known && uint64(other) != acct {
		tx.Commit(ctx)
		return ErrKeyLinked
	}
	if !known {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM player_links WHERE account_id = $1`, int64(acct)).Scan(&n); err != nil {
			return err
		}
		if n >= MaxLinksPerAcc {
			tx.Commit(ctx)
			return ErrTooMany
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM link_pending WHERE key = $1`, key); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO player_links (key, account_id, linked_at) VALUES ($1,$2,$3) ON CONFLICT (key) DO NOTHING`, key, int64(acct), p.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PG) PendingFor(account uint64) []Pending {
	ctx, cancel := ctx5()
	defer cancel()
	p.expire(ctx, p.now())
	rows, err := p.pool.Query(ctx, `SELECT key, server_id, asked_at FROM link_pending WHERE account_id = $1 ORDER BY asked_at`, int64(account))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var x Pending
		if rows.Scan(&x.Key, &x.Server, &x.Asked) == nil {
			x.Fingerprint = Fingerprint(x.Key)
			out = append(out, x)
		}
	}
	return out
}

func (p *PG) Confirm(account uint64, key string) error {
	ctx, cancel := ctx5()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found int64
	if err := tx.QueryRow(ctx, `SELECT account_id FROM link_pending WHERE key = $1 FOR UPDATE`, key).Scan(&found); err != nil || uint64(found) != account {
		return ErrNotFound
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM player_links WHERE account_id = $1`, int64(account)).Scan(&n); err != nil {
		return err
	}
	if n >= MaxLinksPerAcc {
		return ErrTooMany
	}
	if _, err := tx.Exec(ctx, `DELETE FROM link_pending WHERE key = $1`, key); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO player_links (key, account_id, linked_at) VALUES ($1,$2,$3) ON CONFLICT (key) DO UPDATE SET account_id = EXCLUDED.account_id`, key, int64(account), p.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PG) Reject(account uint64, key string) error {
	ctx, cancel := ctx5()
	defer cancel()
	a, err := p.pool.Exec(ctx, `DELETE FROM link_pending WHERE key = $1 AND account_id = $2`, key, int64(account))
	if err != nil {
		return err
	}
	b, err := p.pool.Exec(ctx, `DELETE FROM player_links WHERE key = $1 AND account_id = $2`, key, int64(account))
	if err != nil {
		return err
	}
	if a.RowsAffected()+b.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) LinksFor(account uint64) []Link {
	ctx, cancel := ctx5()
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT key, linked_at FROM player_links WHERE account_id = $1 ORDER BY linked_at`, int64(account))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		if rows.Scan(&l.Key, &l.Linked) == nil {
			l.Fingerprint = Fingerprint(l.Key)
			out = append(out, l)
		}
	}
	return out
}

func (p *PG) AccountOf(key string) (uint64, bool) {
	ctx, cancel := ctx5()
	defer cancel()
	var a int64
	if err := p.pool.QueryRow(ctx, `SELECT account_id FROM player_links WHERE key = $1`, key).Scan(&a); err != nil {
		return 0, false
	}
	return uint64(a), true
}

var _ Backend = (*PG)(nil)
var _ Backend = (*Store)(nil)
