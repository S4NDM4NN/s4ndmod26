package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/digest"
	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/stats"
)

// The weekly summary email: who gets it and when.  The numbers come from stats.WeeklySummary, the mail from the digest package, the
// accounts and the sending from accounts.Service.  It is off unless DIGEST_ENABLED=1.
//
// Rolling schedule: an opted-in, confirmed account is due when a week has passed since its last summary (or since it opted in).  Then the
// window is the last 7 days (and the 7 before for the arrows); an account that did not play in it is looked at again a day later, so
// the summary goes out soon after the first week in which it played.  Sends happen only in an evening window (UTC), a few at a time.

// digestAccounts is the part of accounts.Service the sender uses.
type digestAccounts interface {
	DigestDue(ctx context.Context, limit int) ([]accounts.DigestTarget, error)
	MarkDigestSent(ctx context.Context, id uint64) error
	DeferDigest(ctx context.Context, id uint64, until time.Time) error
	DigestToken(ctx context.Context, id uint64) (string, error)
	SendRich(to, subject, html, text string, headers map[string]string) error
}

// digestStats is the part of the stats store the sender uses.
type digestStats interface {
	WeeklySummary(ctx context.Context, pids []string, from, to time.Time) (stats.WeekStats, error)
	PlayerGameNames(ctx context.Context, keys []string) (map[string][]string, error)
}

type digestSender struct {
	acc     digestAccounts
	st      digestStats
	keys    func(account uint64) []string // an account's linked player keys
	games   func() []byte                 // the games list (JSON), for the clips
	baseURL string                        // the site's address, no trailing slash
	now     func() time.Time
	pause   time.Duration // between two sends: the mail service takes a few requests a second
	fromHr  int           // sends only from this UTC hour ...
	toHr    int           // ... until this one (exclusive)
	batch   int           // accounts looked at per run
}

func (d *digestSender) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// inWindow says whether it is a good hour to send.
func (d *digestSender) inWindow(t time.Time) bool {
	h := t.UTC().Hour()
	if d.fromHr <= d.toHr {
		return h >= d.fromHr && h < d.toHr
	}
	return h >= d.fromHr || h < d.toHr // a window over midnight
}

// parseDigestHours reads "16-22" (UTC hours); anything else is the default evening window.
func parseDigestHours(s string) (from, to int) {
	from, to = 16, 22
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return
	}
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if e1 != nil || e2 != nil || x < 0 || x > 23 || y < 0 || y > 24 || x == y {
		return
	}
	return x, y
}

// build makes the email data for an account's last 7 days, and says whether there was anything to send (a game).
func (d *digestSender) build(ctx context.Context, id uint64, username, token string, now time.Time) (digest.Data, bool, error) {
	keys := d.keys(id)
	to := now.UTC()
	from := to.AddDate(0, 0, -7)
	data := digest.Data{Username: username, From: from, To: to, BaseURL: d.baseURL,
		UnsubURL: d.baseURL + "/account/unsubscribe.html?t=" + url.QueryEscape(token)}
	if len(keys) == 0 {
		return data, false, nil
	}
	this, err := d.st.WeeklySummary(ctx, keys, from, to)
	if err != nil {
		return data, false, err
	}
	data.This = this
	if this.Games == 0 {
		return data, false, nil
	}
	if data.Prev, err = d.st.WeeklySummary(ctx, keys, from.AddDate(0, 0, -7), from); err != nil {
		return data, false, err
	}
	// the week's clips, found the way the player page finds them
	if d.games != nil {
		if body := d.games(); body != nil {
			var list []replay.Summary
			if json.Unmarshal(body, &list) == nil {
				if names, err := d.st.PlayerGameNames(ctx, keys); err == nil {
					for _, c := range clipsOf(list, names) {
						if t, err := time.Parse(time.RFC3339, c.At); err == nil && !t.Before(from) && t.Before(to) {
							if c.Kind == "potg" {
								data.Plays++
							} else {
								data.Highlights++
							}
						}
					}
				}
			}
		}
	}
	return data, true, nil
}

// headers are the unsubscribe headers mail clients turn into their own button (RFC 8058 one-click, which POSTs to the address).
func (d *digestSender) headers(token string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + d.baseURL + "/api/account/digest/unsubscribe?t=" + url.QueryEscape(token) + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

// runOnce looks at the accounts that are due and sends to those that played.  It returns how many it sent.
func (d *digestSender) runOnce(ctx context.Context) (int, error) {
	now := d.clock()
	if !d.inWindow(now) {
		return 0, nil
	}
	batch := d.batch
	if batch <= 0 {
		batch = 40
	}
	due, err := d.acc.DigestDue(ctx, batch)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, t := range due {
		tok, err := d.acc.DigestToken(ctx, t.ID)
		if err != nil || tok == "" {
			log.Printf("accounts: weekly summary: no unsubscribe token for account %d; skipped", t.ID)
			d.acc.DeferDigest(ctx, t.ID, now.Add(24*time.Hour))
			continue
		}
		data, ok, err := d.build(ctx, t.ID, t.Username, tok, now)
		if err != nil {
			return sent, err
		}
		if !ok {
			d.acc.DeferDigest(ctx, t.ID, now.Add(24*time.Hour)) // nothing played: look again tomorrow
			continue
		}
		subject, html, text := digest.Render(data)
		if html == "" {
			log.Printf("accounts: weekly summary for account %d did not render", t.ID)
			d.acc.DeferDigest(ctx, t.ID, now.Add(24*time.Hour))
			continue
		}
		if err := d.acc.SendRich(t.Email, subject, html, text, d.headers(tok)); err != nil {
			log.Printf("accounts: weekly summary for account %d not sent: %v", t.ID, err)
			d.acc.DeferDigest(ctx, t.ID, now.Add(time.Hour)) // try again in an hour, not on every run
			continue
		}
		if err := d.acc.MarkDigestSent(ctx, t.ID); err != nil {
			return sent, err
		}
		sent++
		if d.pause > 0 {
			time.Sleep(d.pause)
		}
	}
	return sent, nil
}

// run is the loop: every 15 minutes, look.
func (d *digestSender) run() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if n, err := d.runOnce(ctx); err != nil {
			log.Printf("accounts: weekly summary run: %v", err)
		} else if n > 0 {
			log.Printf("accounts: sent %d weekly summaries", n)
		}
		cancel()
		time.Sleep(15 * time.Minute)
	}
}

// newDigestSender builds the sender from the site's pieces.  DIGEST_HOURS ("16-22", UTC) is the sending window.
func newDigestSender(acct *accounts.Service, st *stats.Store, dir playerDirectory, games func() []byte) *digestSender {
	from, to := parseDigestHours(os.Getenv("DIGEST_HOURS"))
	acct.Digest = accounts.DigestWindow{On: os.Getenv("DIGEST_ENABLED") == "1", From: from, To: to} // the account page says when it comes
	keys := func(id uint64) []string {
		if dir == nil {
			return nil
		}
		return dir.KeysOfAccount(id)
	}
	return &digestSender{acc: acct, st: st, keys: keys, games: games, baseURL: strings.TrimRight(acct.PublicURL, "/"),
		pause: 600 * time.Millisecond, fromHr: from, toHr: to, batch: 40}
}

// startDigest runs the sender when DIGEST_ENABLED=1; otherwise nothing is ever sent by the schedule (the admin test mail still works).
func startDigest(d *digestSender) {
	if os.Getenv("DIGEST_ENABLED") != "1" {
		return
	}
	log.Printf("accounts: weekly summary emails on, sent between %02d:00 and %02d:00 UTC", d.fromHr, d.toHr)
	go d.run()
}

// digestTestHandler is POST /api/admin/digest/test (admin): mails the signed-in admin a test of the weekly summary, to their own address
// only.  It uses their real last 7 days when they played, and made-up numbers otherwise (the mail says so in its subject).
func digestTestHandler(acct *accounts.Service, d *digestSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		a, ok := acct.FromRequest(r)
		if !ok || !a.Has(accounts.PermAdmin) {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "admin only"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		tok, err := d.acc.DigestToken(ctx, a.ID)
		if err != nil || tok == "" {
			tok = "test-no-token-because-not-opted-in"
		}
		data, real, err := d.build(ctx, a.ID, a.Username, tok, d.clock())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "could not read your stats"})
			return
		}
		subject := ""
		if !real {
			base, unsub := data.BaseURL, data.UnsubURL
			data = digest.Sample(a.Username, "", d.clock().UTC())
			data.BaseURL, data.UnsubURL = base, unsub
			subject = "[TEST, made-up numbers] "
		} else {
			subject = "[TEST] "
		}
		subj, html, text := digest.Render(data)
		if err := d.acc.SendRich(a.Email, subject+subj, html, text, d.headers(tok)); err != nil {
			log.Printf("accounts: test weekly summary to account %d: %v", a.ID, err)
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": "the mail service refused it"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sent_to_you": true, "real_numbers": real})
	}
}
