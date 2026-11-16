package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/stats"
)

type fakeDigestAccounts struct {
	due      []accounts.DigestTarget
	tokens   map[uint64]string
	sentTo   []string
	subjects []string
	headers  []map[string]string
	htmls    []string
	marked   []uint64
	deferred map[uint64]time.Time
	sendErr  error
}

func (f *fakeDigestAccounts) DigestDue(_ context.Context, _ int) ([]accounts.DigestTarget, error) {
	return f.due, nil
}
func (f *fakeDigestAccounts) MarkDigestSent(_ context.Context, id uint64) error {
	f.marked = append(f.marked, id)
	return nil
}
func (f *fakeDigestAccounts) DeferDigest(_ context.Context, id uint64, until time.Time) error {
	if f.deferred == nil {
		f.deferred = map[uint64]time.Time{}
	}
	f.deferred[id] = until
	return nil
}
func (f *fakeDigestAccounts) DigestToken(_ context.Context, id uint64) (string, error) {
	return f.tokens[id], nil
}
func (f *fakeDigestAccounts) SendRich(to, subject, html, text string, headers map[string]string) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sentTo = append(f.sentTo, to)
	f.subjects = append(f.subjects, subject)
	f.htmls = append(f.htmls, html)
	f.headers = append(f.headers, headers)
	return nil
}

// fakeDigestStats: key "kplay" played (3 games this week, 2 the week before); anyone else did not.
type fakeDigestStats struct{ err error }

func (f fakeDigestStats) WeeklySummary(_ context.Context, pids []string, from, to time.Time) (stats.WeekStats, error) {
	if f.err != nil {
		return stats.WeekStats{}, f.err
	}
	if len(pids) == 1 && pids[0] == "kplay" {
		// the first call is the week just gone (it ends now), the second the week before (it ended a week ago)
		if time.Since(to) < 24*time.Hour {
			return stats.WeekStats{Games: 3, Wins: 2, Losses: 1, Kills: 30, Deaths: 15, TopClass: 1, TopClassMs: 600000, TopWeapon: "MP40", TopWeaponKills: 12, TopWeaponHits: 20, TopWeaponAtts: 60}, nil
		}
		return stats.WeekStats{Games: 2, Wins: 1, Losses: 1, Kills: 10, Deaths: 15, TopClass: 1}, nil
	}
	return stats.WeekStats{TopClass: -1}, nil
}
func (f fakeDigestStats) PlayerGameNames(_ context.Context, _ []string) (map[string][]string, error) {
	return map[string][]string{"replays_1": {"Ann"}}, nil
}

func newTestSender(acc *fakeDigestAccounts, now time.Time) *digestSender {
	return &digestSender{acc: acc, st: fakeDigestStats{},
		keys: func(id uint64) []string {
			if id == 1 {
				return []string{"kplay"}
			}
			if id == 2 {
				return []string{"kidle"}
			}
			return nil // account 3 has no key
		},
		games:   func() []byte { return nil },
		baseURL: "https://example.test",
		now:     func() time.Time { return now },
		fromHr:  16, toHr: 22, batch: 10,
	}
}

func TestParseDigestHours(t *testing.T) {
	for in, want := range map[string][2]int{"": {16, 22}, "garbage": {16, 22}, "18-23": {18, 23}, "5-5": {16, 22}, "25-3": {16, 22}, "22-2": {22, 2}, " 8-12 ": {8, 12}} {
		f, to := parseDigestHours(in)
		if f != want[0] || to != want[1] {
			t.Errorf("%q = %d-%d, want %d-%d", in, f, to, want[0], want[1])
		}
	}
	d := &digestSender{fromHr: 22, toHr: 2}
	at := func(h int) time.Time { return time.Date(2026, 11, 16, h, 30, 0, 0, time.UTC) }
	if !d.inWindow(at(23)) || !d.inWindow(at(1)) || d.inWindow(at(12)) {
		t.Error("a window over midnight")
	}
	d = &digestSender{fromHr: 16, toHr: 22}
	if !d.inWindow(at(16)) || d.inWindow(at(22)) || d.inWindow(at(3)) {
		t.Error("an evening window")
	}
}

func TestDigestRunOnce(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour).Add(0) // the hour does not matter here: the window is moved to include it
	acc := &fakeDigestAccounts{
		due: []accounts.DigestTarget{
			{ID: 1, Username: "Ann <b>", Email: "ann@example.com"}, // played
			{ID: 2, Username: "Idle", Email: "idle@example.com"},   // did not
			{ID: 3, Username: "Keyless", Email: "k@example.com"},   // no player key linked
			{ID: 4, Username: "NoTok", Email: "n@example.com"},     // no unsubscribe token (should not happen)
		},
		tokens: map[uint64]string{1: "tok-one-000000000000000000000000", 2: "tok-two-000000000000000000000000", 3: "tok-three-0000000000000000000000"},
	}
	d := newTestSender(acc, now)
	d.fromHr, d.toHr = now.Hour(), now.Hour()+1
	if now.Hour() == 23 {
		d.fromHr, d.toHr = 23, 24
	}
	n, err := d.runOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("runOnce: %d %v", n, err)
	}
	if len(acc.sentTo) != 1 || acc.sentTo[0] != "ann@example.com" || len(acc.marked) != 1 || acc.marked[0] != 1 {
		t.Fatalf("sent %v marked %v", acc.sentTo, acc.marked)
	}
	if !strings.Contains(acc.subjects[0], "3 games") || strings.Contains(acc.htmls[0], "Ann <b>") || !strings.Contains(acc.htmls[0], "Ann &lt;b&gt;") {
		t.Errorf("the mail: %q", acc.subjects[0])
	}
	h := acc.headers[0]
	if h["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || h["List-Unsubscribe"] != "<https://example.test/api/account/digest/unsubscribe?t=tok-one-000000000000000000000000>" {
		t.Errorf("headers: %v", h)
	}
	if !strings.Contains(acc.htmls[0], "https://example.test/account/unsubscribe.html?t=tok-one-000000000000000000000000") {
		t.Error("the footer link goes to the unsubscribe page with the token")
	}
	// the ones that did not play, or could not be mailed, are looked at again tomorrow; none were marked sent
	for _, id := range []uint64{2, 3, 4} {
		until, ok := acc.deferred[id]
		if !ok || until.Sub(now) < 23*time.Hour || until.Sub(now) > 25*time.Hour {
			t.Errorf("account %d deferred to %v (ok %v)", id, until, ok)
		}
	}
	for _, id := range acc.marked {
		if id != 1 {
			t.Errorf("account %d marked as sent", id)
		}
	}
}

func TestDigestRunOnceFailuresAndWindow(t *testing.T) {
	now := time.Now().UTC()
	acc := &fakeDigestAccounts{due: []accounts.DigestTarget{{ID: 1, Username: "Ann", Email: "ann@example.com"}}, tokens: map[uint64]string{1: "tok-one-000000000000000000000000"}, sendErr: errors.New("service down")}
	d := newTestSender(acc, now)
	d.fromHr, d.toHr = now.Hour(), now.Hour()+1
	if now.Hour() == 23 {
		d.fromHr, d.toHr = 23, 24
	}
	if n, err := d.runOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("a refused send: %d %v", n, err)
	}
	if len(acc.marked) != 0 {
		t.Error("marked sent although the mail was refused")
	}
	if until := acc.deferred[1]; until.Sub(now) < 59*time.Minute || until.Sub(now) > 61*time.Minute {
		t.Errorf("a failed send should be retried in an hour, not on the next run: %v", until)
	}
	// outside the window: nothing is looked at
	acc2 := &fakeDigestAccounts{due: acc.due, tokens: acc.tokens}
	d2 := newTestSender(acc2, time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC))
	if n, err := d2.runOnce(context.Background()); err != nil || n != 0 || len(acc2.sentTo) != 0 || len(acc2.deferred) != 0 {
		t.Errorf("outside the window: %d %v %v", n, err, acc2.deferred)
	}
	// a stats failure stops the run without marking anything
	acc3 := &fakeDigestAccounts{due: acc.due, tokens: acc.tokens}
	d3 := newTestSender(acc3, now)
	d3.fromHr, d3.toHr = d.fromHr, d.toHr
	d3.st = fakeDigestStats{err: errors.New("db down")}
	if _, err := d3.runOnce(context.Background()); err == nil || len(acc3.marked) != 0 {
		t.Errorf("stats failure: %v marked %v", err, acc3.marked)
	}
}

func TestDigestClipCounts(t *testing.T) {
	now := time.Now().UTC()
	in := now.Add(-48 * time.Hour).Format(time.RFC3339)
	old := now.Add(-20 * 24 * time.Hour).Format(time.RFC3339)
	list := []replay.Summary{
		{Name: "replays_1", Map: "mp_beach", GeneratedAt: in, HasRPL: true, HasPOTG: true, PotgPlayer: "Ann", Highlights: []replay.HighlightSummary{{Player: 2, Name: "Ann", Score: 5}}},
		{Name: "replays_2", Map: "mp_base", GeneratedAt: old, HasRPL: true, HasPOTG: true, PotgPlayer: "Ann"},
	}
	body, _ := json.Marshal(list)
	acc := &fakeDigestAccounts{tokens: map[uint64]string{1: "tok-one-000000000000000000000000"}}
	d := newTestSender(acc, now)
	d.games = func() []byte { return body }
	d.st = namesStats{fakeDigestStats{}}
	data, ok, err := d.build(context.Background(), 1, "Ann", "tok-one-000000000000000000000000", now)
	if err != nil || !ok {
		t.Fatalf("build: %v %v", ok, err)
	}
	if data.Plays != 1 || data.Highlights != 1 {
		t.Errorf("clips this week: %d plays, %d highlights (the 20-day-old play of the game is not this week's)", data.Plays, data.Highlights)
	}
}

// namesStats also knows the names Ann used in replays_1 and replays_2.
type namesStats struct{ fakeDigestStats }

func (namesStats) PlayerGameNames(_ context.Context, _ []string) (map[string][]string, error) {
	return map[string][]string{"replays_1": {"Ann"}, "replays_2": {"Ann"}}, nil
}

func TestDigestTestHandlerIsAdminOnly(t *testing.T) {
	// no signed-in account at all: refused (the account service is nil-safe through FromRequest on a service with no sessions is not available
	// here, so this checks the method route and the refusal path with a request that carries no session)
	acc := &fakeDigestAccounts{}
	d := newTestSender(acc, time.Now())
	_ = d
	req := httptest.NewRequest(http.MethodPost, "/api/admin/digest/test", nil)
	rec := httptest.NewRecorder()
	// a Service with no database cannot find a session; FromRequest then says "not signed in"
	svc := &accounts.Service{}
	digestTestHandler(svc, d)(rec, req)
	if rec.Code != http.StatusForbidden || len(acc.sentTo) != 0 {
		t.Errorf("anonymous: %d, mails %v", rec.Code, acc.sentTo)
	}
}
