package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// softBounceLimit is how many temporary failures in a row (a full mailbox, a server that was down) turn the weekly summary off.
const softBounceLimit = 3

// RecordMailEvent applies one report from the mail service: kind is Resend's event type ("email.bounced", "email.complained",
// "email.delivered"), bounce its bounce type ("Permanent", "Transient", "Undetermined"), to the addresses the mail went to.
//
// A permanent bounce or a spam complaint marks the account's address at once: the weekly summary is switched off and cannot be
// switched on again until the address changes.  Temporary failures are counted, and the third in a row does the same; a delivery
// starts the count over.  eventID makes a retried delivery of the same report count once.  It returns how many accounts changed.
func (s *Service) RecordMailEvent(ctx context.Context, eventID, kind, bounce string, to []string) (int, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO mail_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, nil // already handled
	}
	s.pool.Exec(ctx, `DELETE FROM mail_events WHERE received_at < $1`, s.now().Add(-30*24*time.Hour))
	changed := 0
	for _, addr := range to {
		key := strings.ToLower(strings.TrimSpace(addr))
		var id int64
		if kind == "email.complained" {
			s.suppress(ctx, key, "reported as spam")
		} else if kind == "email.bounced" && bounce == "Permanent" {
			s.suppress(ctx, key, "address does not exist or refuses mail")
		}
		switch {
		case kind == "email.delivered":
			s.pool.Exec(ctx, `UPDATE accounts SET mail_soft_bounces = 0 WHERE email_key = $1 AND mail_soft_bounces > 0`, key)
			continue
		case kind == "email.complained":
			err = s.pool.QueryRow(ctx, mark, key, "reported as spam", s.now()).Scan(&id)
		case kind == "email.bounced" && bounce == "Permanent":
			err = s.pool.QueryRow(ctx, mark, key, "address does not exist or refuses mail", s.now()).Scan(&id)
		case kind == "email.bounced":
			var n int
			err = s.pool.QueryRow(ctx, `UPDATE accounts SET mail_soft_bounces = mail_soft_bounces + 1 WHERE email_key = $1 AND mail_bounced_at IS NULL
				RETURNING id, mail_soft_bounces`, key).Scan(&id, &n)
			if err == nil && n >= softBounceLimit {
				err = s.pool.QueryRow(ctx, mark, key, "mail kept failing to arrive", s.now()).Scan(&id)
			} else if err == nil {
				continue
			}
		default:
			continue
		}
		if err != nil {
			continue // no account with that address (or it is marked already)
		}
		changed++
		log.Printf("accounts: mail to account %d cannot be delivered (%s %s): the weekly summary is off", id, kind, bounce)
	}
	return changed, nil
}

// mark switches the summary off and records why; it only touches an account that is not marked yet.
const mark = `UPDATE accounts SET mail_bounced_at = $3, mail_bounce_why = $2, digest_opt_in = false
	WHERE email_key = $1 AND mail_bounced_at IS NULL RETURNING id`

// verifySignature checks a Resend (Svix) webhook: the signature is a base64 HMAC-SHA256, keyed with the secret's decoded part (after "whsec_"),
// of "<id>.<timestamp>.<body>".  The header may hold several space-separated "v1,<signature>" values.  Timestamps more than five
// minutes off are refused, so a captured request cannot be replayed later.
func verifySignature(secret, id, ts, sigs string, body []byte, now time.Time) bool {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 || id == "" {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, part := range strings.Fields(sigs) {
		v, ok := strings.CutPrefix(part, "v1,")
		if !ok {
			continue
		}
		if got, err := base64.StdEncoding.DecodeString(v); err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}

// mailEvents is the webhook Resend calls (POST /api/mail/events).  Everything it does is switching a summary off, and only
// for a request with a valid signature.
func (h *Handler) mailEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !verifySignature(h.WebhookSecret, r.Header.Get("svix-id"), r.Header.Get("svix-timestamp"), r.Header.Get("svix-signature"), body, h.S.now()) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			To     []string `json:"to"`
			Bounce struct {
				Type string `json:"type"`
			} `json:"bounce"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &ev) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, err := h.S.RecordMailEvent(r.Context(), r.Header.Get("svix-id"), ev.Type, ev.Data.Bounce.Type, ev.Data.To); err != nil {
		log.Printf("accounts: mail event: %v", err)
		http.Error(w, "try again", http.StatusInternalServerError) // Resend retries a failed delivery
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
