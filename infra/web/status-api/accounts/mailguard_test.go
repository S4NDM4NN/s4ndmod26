package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func regBody(name, email string) map[string]any {
	return map[string]any{"username": name, "email": email, "password": name + " long password"}
}

func TestMailPerAddress(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mail := &Capture{}
	s.Mail, s.PublicURL = mail, "https://example.test"
	s.MailPerHour = 1000

	// a sign-up that fails (the username is taken) must not use up a stranger's mail allowance
	if _, err := s.Register(ctx, "owner", "owner@example.com", "owner long password"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := s.Register(ctx, "owner", "victim@example.com", "some long password"); !errors.Is(err, ErrTaken) {
			t.Fatalf("taken username: %v", err)
		}
	}
	// register, delete, register ... to one address: three mails a day, then it stops, and the refused account is not left behind
	for i := 0; i < 3; i++ {
		a, err := s.Register(ctx, "bot"+string(rune('a'+i)), "victim@example.com", "some long password")
		if err != nil {
			t.Fatalf("sign-up %d for the victim's address: %v", i+1, err)
		}
		s.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, int64(a.ID))
	}
	before := len(mail.Mail)
	if _, err := s.Register(ctx, "botz", "Victim@Example.com", "some long password"); !errors.Is(err, ErrMailLimit) {
		t.Fatalf("fourth mail to one address in a day: %v", err)
	}
	if _, err := s.ByUsername(ctx, "botz"); err == nil {
		t.Error("the refused sign-up left an account behind")
	}
	if len(mail.Mail) != before {
		t.Error("mail was sent after the limit")
	}
	// a day later it works again
	s.Now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if _, err := s.Register(ctx, "botz", "victim@example.com", "some long password"); err != nil {
		t.Errorf("after a day: %v", err)
	}
}

func TestMailSiteLimit(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mail := &Capture{}
	s.Mail, s.PublicURL = mail, "https://example.test"
	s.MailPerHour = 3
	mux := http.NewServeMux()
	(&Handler{S: s}).Mount(mux)
	c := &client{t: t, mux: mux}

	for i, n := range []string{"usr1", "usr2", "usr3"} {
		if w, _ := c.do("POST", "/api/account/register", regBody(n, n+"@example.com")); w.Code != 201 {
			t.Fatalf("sign-up %d: %d", i+1, w.Code)
		}
	}
	if w, _ := c.do("POST", "/api/account/register", regBody("usr4", "usr4@example.com")); w.Code != 429 {
		t.Errorf("sign-up past the hourly limit: %d", w.Code)
	}
	if _, err := s.ByUsername(ctx, "usr4"); err == nil {
		t.Error("the refused sign-up left an account behind")
	}
	// a reset says the same thing, but no mail goes out
	n := len(mail.Mail)
	if w, _ := c.do("POST", "/api/account/reset", map[string]string{"email": "usr1@example.com"}); w.Code != 204 {
		t.Errorf("reset: %d", w.Code)
	}
	if len(mail.Mail) != n {
		t.Error("a reset mail was sent past the hourly limit")
	}
	s.Now = func() time.Time { return time.Now().Add(61 * time.Minute) }
	if w, _ := c.do("POST", "/api/account/register", regBody("usr4", "usr4@example.com")); w.Code != 201 {
		t.Errorf("an hour later: %d", w.Code)
	}
}

// Nobody can use up a person's way back into their account by asking for mail to their address.
func TestResetIgnoresAddressLimit(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mail := &Capture{}
	s.Mail, s.PublicURL = mail, "https://example.test"
	if _, err := s.Register(ctx, "rita", "rita@example.com", "ritas long password"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		s.rl.allow("to:rita@example.com", mailPerAddressDay, 24*time.Hour, s.now())
	}
	n := len(mail.Mail)
	if err := s.RequestReset(ctx, "rita@example.com", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if len(mail.Mail) != n+1 {
		t.Error("the reset mail was held back by the per-address limit")
	}
}

func TestSuppressedAddresses(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mail := &Capture{}
	s.Mail, s.PublicURL = mail, "https://example.test"
	mux := http.NewServeMux()
	(&Handler{S: s}).Mount(mux)
	c := &client{t: t, mux: mux}

	// reports for addresses that have no account (any more) still count
	s.RecordMailEvent(ctx, "e1", "email.bounced", "Permanent", []string{"Gone@Example.com"})
	s.RecordMailEvent(ctx, "e2", "email.complained", "", []string{"angry@example.com"})
	s.RecordMailEvent(ctx, "e3", "email.bounced", "Transient", []string{"full@example.com"})
	for _, addr := range []string{"gone@example.com", "angry@example.com"} {
		if w, _ := c.do("POST", "/api/account/register", regBody("x"+addr[:3], addr)); w.Code != 400 {
			t.Errorf("sign-up with %s: %d", addr, w.Code)
		}
	}
	if w, _ := c.do("POST", "/api/account/register", regBody("fulluser", "full@example.com")); w.Code != 201 {
		t.Errorf("a temporary failure refused a sign-up: %d", w.Code)
	}

	// an account may not move to such an address either
	c.do("POST", "/api/account/register", regBody("sam", "sam@example.com"))
	tok, _, err := s.Login(ctx, "sam", "sam long password", "2.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	c.tok = tok
	if w, _ := c.do("POST", "/api/account/email", map[string]string{"password": "sam long password", "email": "gone@example.com"}); w.Code != 400 {
		t.Errorf("change to a bounced address: %d", w.Code)
	}

	// and a reset for an account whose address went bad sends nothing
	s.RecordMailEvent(ctx, "e4", "email.bounced", "Permanent", []string{"sam@example.com"})
	n := len(mail.Mail)
	s.RequestReset(ctx, "sam@example.com", "3.3.3.3")
	if len(mail.Mail) != n {
		t.Error("a reset mail went to an address that bounced")
	}

	// after 30 days the address is allowed again
	s.Now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	c.tok = ""
	if w, _ := c.do("POST", "/api/account/register", regBody("gone", "gone@example.com")); w.Code != 201 {
		t.Errorf("after 30 days: %d", w.Code)
	}
}
