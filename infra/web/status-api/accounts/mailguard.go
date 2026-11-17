package accounts

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
	"time"
)

// The limits on the mail an account action can send.  They are about the domain's standing with mail providers (a burst of mail to
// strangers or to dead addresses gets it judged as spam) and about the daily allowance at the mail service: a bot must not be able to
// use that up and leave real people without their confirmation mail.
const (
	mailPerAddressDay  = 3                   // sign-up, resend, change-email and reset mails to one address, per 24 hours
	defaultMailPerHour = 30                  // account mails from the whole site, per hour (Service.MailPerHour)
	suppressedFor      = 30 * 24 * time.Hour // how long a bounced address is refused
	mailWarnFraction   = 0.8                 // log a warning when this much of the hourly allowance is used
)

var (
	ErrMailLimit  = errors.New("too many mails were sent to that address today; try again tomorrow")
	ErrMailBusy   = errors.New("the site is sending a lot of mail right now; try again in a little while")
	ErrSuppressed = errors.New("mail to that address could not be delivered recently; use a different address")
)

func (s *Service) mailPerHour() int {
	if s.MailPerHour > 0 {
		return s.MailPerHour
	}
	return defaultMailPerHour
}

// reserveMail takes one mail from the allowances before it is sent: the site-wide hourly one, and, when to is not empty, the
// per-address daily one.  Digest mails are not counted; they have their own pacing, and neither are password resets (see RequestReset).
func (s *Service) reserveMail(to string) error {
	now := s.now()
	if to != "" && !s.rl.allow("to:"+strings.ToLower(strings.TrimSpace(to)), mailPerAddressDay, 24*time.Hour, now) {
		return ErrMailLimit
	}
	max := s.mailPerHour()
	if !s.rl.allow("mail:all", max, time.Hour, now) {
		log.Printf("accounts: the hourly limit of %d account mails is used up: sign-up and reset mails are refused until it clears", max)
		return ErrMailBusy
	}
	if n := s.rl.count("mail:all", time.Hour, now); n == int(math.Ceil(float64(max)*mailWarnFraction)) {
		log.Printf("accounts: %d of the %d account mails allowed this hour are used", n, max)
	}
	return nil
}

// Suppressed says whether the address bounced or complained within the last 30 days.
func (s *Service) Suppressed(ctx context.Context, email string) bool {
	var n int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM mail_suppressed WHERE email_key = $1 AND at > $2`,
		strings.ToLower(strings.TrimSpace(email)), s.now().Add(-suppressedFor)).Scan(&n)
	return n > 0
}

func (s *Service) suppress(ctx context.Context, key, why string) {
	s.pool.Exec(ctx, `DELETE FROM mail_suppressed WHERE at < $1`, s.now().Add(-suppressedFor))
	s.pool.Exec(ctx, `INSERT INTO mail_suppressed (email_key, reason, at) VALUES ($1, $2, $3)
		ON CONFLICT (email_key) DO UPDATE SET reason = EXCLUDED.reason, at = EXCLUDED.at`, key, why, s.now())
}
