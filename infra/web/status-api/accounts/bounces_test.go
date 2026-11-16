package accounts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func sign(secret, id string, ts time.Time, body string) (string, string) {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	t := fmt.Sprint(ts.Unix())
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + t + "." + body))
	return t, "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	now := time.Now()
	body := []byte(`{"type":"email.bounced"}`)
	ts, sig := sign(testSecret, "msg_1", now, string(body))
	if !verifySignature(testSecret, "msg_1", ts, sig, body, now) {
		t.Error("good signature refused")
	}
	if !verifySignature(testSecret, "msg_1", ts, "v1,AAAA "+sig, body, now) {
		t.Error("second of two signatures refused")
	}
	if verifySignature(testSecret, "msg_2", ts, sig, body, now) {
		t.Error("signature of another message accepted")
	}
	if verifySignature(testSecret, "msg_1", ts, sig, []byte(`{"type":"email.complained"}`), now) {
		t.Error("changed body accepted")
	}
	other := "whsec_" + base64.StdEncoding.EncodeToString([]byte("a different secret entirely!!!!!"))
	if verifySignature(other, "msg_1", ts, sig, body, now) {
		t.Error("other secret accepted")
	}
	if verifySignature(testSecret, "msg_1", ts, sig, body, now.Add(6*time.Minute)) {
		t.Error("old request accepted")
	}
	if verifySignature(testSecret, "msg_1", ts, "", body, now) || verifySignature("", "msg_1", ts, sig, body, now) || verifySignature("whsec_!!", "msg_1", ts, sig, body, now) {
		t.Error("empty or broken input accepted")
	}
}

func TestMailEvents(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mail := &Capture{}
	s.Mail, s.PublicURL = mail, "https://example.test"
	mux := http.NewServeMux()
	h := &Handler{S: s}
	h.Mount(mux)
	if w, _ := (&client{t: t, mux: mux}).do("POST", "/api/mail/events", nil); w.Code != 404 && w.Code != 405 {
		t.Errorf("webhook exists without a secret: %d", w.Code)
	}
	h.WebhookSecret = testSecret
	mux = http.NewServeMux()
	h.Mount(mux)
	c := &client{t: t, mux: mux}

	mk := func(name string) {
		c.do("POST", "/api/account/register", map[string]any{"username": name, "email": name + "@example.com", "password": name + " long password", "weekly_digest": true})
	}
	for _, n := range []string{"hana", "ivan", "june", "kai"} {
		mk(n)
	}
	post := func(id, body string, ok bool) int {
		ts, sig := sign(testSecret, id, time.Now(), body)
		if !ok {
			sig = "v1,AAAA"
		}
		r := httptest.NewRequest("POST", "/api/mail/events", bytes.NewReader([]byte(body)))
		r.Header.Set("svix-id", id)
		r.Header.Set("svix-timestamp", ts)
		r.Header.Set("svix-signature", sig)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	ev := func(typ, bounce, to string) string {
		b := ""
		if bounce != "" {
			b = `,"bounce":{"type":"` + bounce + `","subType":"x"}`
		}
		return `{"type":"` + typ + `","created_at":"2026-11-16T12:00:00Z","data":{"email_id":"e1","to":["` + to + `"]` + b + `}}`
	}
	state := func(name string) Account { a, _ := s.ByUsername(ctx, name); return a }

	if got := post("m0", ev("email.bounced", "Permanent", "hana@example.com"), false); got != 401 {
		t.Errorf("bad signature: %d", got)
	}
	if state("hana").MailBounced || !state("hana").WeeklyDigest {
		t.Error("an unsigned request changed an account")
	}

	// a permanent bounce (the address differs in case from what is stored)
	if got := post("m1", ev("email.bounced", "Permanent", "Hana@Example.com"), true); got != 204 {
		t.Fatalf("hard bounce: %d", got)
	}
	a := state("hana")
	if !a.MailBounced || a.WeeklyDigest || a.MailBounceWhy == "" {
		t.Errorf("after a hard bounce: %+v", a)
	}
	if err := s.SetDigest(ctx, a.ID, true); err != ErrBounced {
		t.Errorf("opted back in while bounced: %v", err)
	}
	if due, _ := s.DigestDue(ctx, 10); len(due) != 0 {
		t.Errorf("a bounced address is due a summary: %+v", due)
	}

	// a complaint
	post("m2", ev("email.complained", "", "ivan@example.com"), true)
	if a := state("ivan"); !a.MailBounced || a.WeeklyDigest {
		t.Errorf("after a complaint: %+v", a)
	}

	// temporary failures: two do nothing, a delivery starts over, then three in a row switch it off; a retried event counts once
	post("s1", ev("email.bounced", "Transient", "june@example.com"), true)
	post("s1", ev("email.bounced", "Transient", "june@example.com"), true)
	post("s2", ev("email.bounced", "Undetermined", "june@example.com"), true)
	if a := state("june"); a.MailBounced || !a.WeeklyDigest {
		t.Fatalf("two temporary failures: %+v", a)
	}
	post("d1", ev("email.delivered", "", "june@example.com"), true)
	post("s3", ev("email.bounced", "Transient", "june@example.com"), true)
	post("s4", ev("email.bounced", "Transient", "june@example.com"), true)
	if a := state("june"); a.MailBounced {
		t.Fatalf("the count did not start over after a delivery: %+v", a)
	}
	post("s5", ev("email.bounced", "Transient", "june@example.com"), true)
	if a := state("june"); !a.MailBounced || a.WeeklyDigest {
		t.Errorf("three temporary failures in a row: %+v", a)
	}

	// things that must not matter
	post("x1", ev("email.sent", "", "kai@example.com"), true)
	post("x2", ev("email.delivery_delayed", "", "kai@example.com"), true)
	post("x3", ev("email.bounced", "Permanent", "nobody@example.com"), true)
	if a := state("kai"); a.MailBounced || !a.WeeklyDigest {
		t.Errorf("an unrelated event changed an account: %+v", a)
	}
	if got := post("x4", "not json", true); got != 400 {
		t.Errorf("not json: %d", got)
	}

	// changing the address clears it, and the summary can be switched on again
	hana := state("hana")
	tok, _, err := s.Login(ctx, "hana", "hana long password", "7.7.7.7")
	if err != nil {
		t.Fatal(err)
	}
	c.tok = tok
	if w, _ := c.do("PUT", "/api/account/digest", map[string]bool{"on": true}); w.Code != 409 {
		t.Errorf("switching on while bounced over http: %d", w.Code)
	}
	c.do("POST", "/api/account/email", map[string]string{"password": "hana long password", "email": "hana2@example.com"})
	c.tok = ""
	if w, _ := c.do("POST", "/api/account/email/confirm", map[string]string{"token": mail.Token()}); w.Code != 204 {
		t.Fatalf("confirm: %d", w.Code)
	}
	if a := state("hana"); a.MailBounced || a.MailBounceWhy != "" {
		t.Errorf("still marked after the address changed: %+v", a)
	}
	if err := s.SetDigest(ctx, hana.ID, true); err != nil {
		t.Errorf("could not opt in after fixing the address: %v", err)
	}
}
