package accounts

import (
	"context"
	"time"
)

// DigestInterval is the least time between two weekly summaries to one account.  It is counted from the last one sent, or from the moment
// the account opted in: a new account's first summary comes a week after it signed up, not at once.
const DigestInterval = 7 * 24 * time.Hour

// DigestTarget is an account the weekly summary may be due for.
type DigestTarget struct {
	ID       uint64
	Username string
	Email    string
}

// DigestDue lists up to limit opted-in accounts whose last summary (or opt-in) is a week old or more, oldest first.  The email must be
// confirmed, and a suspended account gets nothing.  An account that was looked at and had not played is skipped until DeferDigest's time.
// Whether the account played is the caller's question (the stats live elsewhere): it sends, or defers, or leaves it for the next run.
func (s *Service) DigestDue(ctx context.Context, limit int) ([]DigestTarget, error) {
	now := s.now()
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.username, a.email FROM accounts a
		WHERE a.digest_opt_in AND a.email_verified_at IS NOT NULL AND a.suspended_at IS NULL
		  AND GREATEST(COALESCE(a.digest_last_sent_at, '-infinity'), COALESCE(a.digest_opted_in_at, '-infinity')) <= $1
		  AND (a.digest_next_check_at IS NULL OR a.digest_next_check_at <= $2)
		ORDER BY GREATEST(COALESCE(a.digest_last_sent_at, '-infinity'), COALESCE(a.digest_opted_in_at, '-infinity')), a.id
		LIMIT $3`, now.Add(-DigestInterval), now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DigestTarget
	for rows.Next() {
		var t DigestTarget
		var id int64
		if err := rows.Scan(&id, &t.Username, &t.Email); err != nil {
			return nil, err
		}
		t.ID = uint64(id)
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkDigestSent records that a summary went out now: the next one is due in DigestInterval.
func (s *Service) MarkDigestSent(ctx context.Context, id uint64) error {
	_, err := s.pool.Exec(ctx, `UPDATE accounts SET digest_last_sent_at = $2, digest_next_check_at = NULL WHERE id = $1`, int64(id), s.now())
	return err
}

// DeferDigest leaves the account alone until the given time (it had not played, so there was nothing to send).
func (s *Service) DeferDigest(ctx context.Context, id uint64, until time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE accounts SET digest_next_check_at = $2 WHERE id = $1`, int64(id), until)
	return err
}

// RichMailer is a Mailer that can also send an HTML mail with its plain-text twin and extra headers (the unsubscribe headers).
type RichMailer interface {
	SendRich(to, subject, html, text string, headers map[string]string) error
}

// SendRich sends an HTML mail through the configured mailer; one that cannot (the log and test mailers) gets the plain text.
func (s *Service) SendRich(to, subject, html, text string, headers map[string]string) error {
	if m, ok := s.mailer().(RichMailer); ok {
		return m.SendRich(to, subject, html, text, headers)
	}
	return s.mailer().Send(to, subject, text)
}
