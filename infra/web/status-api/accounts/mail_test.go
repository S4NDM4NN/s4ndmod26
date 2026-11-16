package accounts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendMailer(t *testing.T) {
	var got struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		ReplyTo string   `json:"reply_to"`
	}
	var auth, ctype, method, raw string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ctype, method = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Method
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		json.Unmarshal(b, &got)
		w.WriteHeader(status)
		if status != 200 {
			io.WriteString(w, `{"name":"validation_error","message":"The from address is not verified"}`)
			return
		}
		io.WriteString(w, `{"id":"abc"}`)
	}))
	defer srv.Close()

	m := &ResendMailer{APIKey: "re_secret_key", From: "S4NDMoD26 <accounts@example.com>", ReplyTo: "help@example.com", URL: srv.URL}
	if err := m.Send("ann@example.org", "Confirm your email", "link: https://x/#verify=TOKEN"); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || auth != "Bearer re_secret_key" || ctype != "application/json" {
		t.Errorf("request: %s %q %q", method, auth, ctype)
	}
	if got.From != "S4NDMoD26 <accounts@example.com>" || len(got.To) != 1 || got.To[0] != "ann@example.org" ||
		got.Subject != "Confirm your email" || !strings.Contains(got.Text, "TOKEN") || got.ReplyTo != "help@example.com" {
		t.Errorf("body = %+v", got)
	}

	// a refusal is an error that says why, and never contains the key
	status = 422
	err := m.Send("ann@example.org", "x", "y")
	if err == nil || !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "not verified") || strings.Contains(err.Error(), "re_secret_key") {
		t.Errorf("refusal: %v", err)
	}

	// no reply-to: the field is left out
	status = 200
	m2 := &ResendMailer{APIKey: "k", From: "a@example.com", URL: srv.URL}
	if err := m2.Send("b@example.org", "s", "b"); err != nil || strings.Contains(raw, "reply_to") {
		t.Errorf("no reply-to: %v %s", err, raw)
	}

	// an unreachable service is an error too, not a hang
	srv.Close()
	if err := m.Send("ann@example.org", "x", "y"); err == nil {
		t.Error("a dead service gave no error")
	}
}
