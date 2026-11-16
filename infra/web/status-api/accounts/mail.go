package accounts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Mailer sends the account emails.  LogMailer prints them (and OutboxMailer writes
// them to a folder), which is enough for development and for tests; ResendMailer is the real sender.
type Mailer interface {
	Send(to, subject, body string) error
}

// ResendMailer sends mail through Resend's HTTP API (https://resend.com/docs/api-reference/emails/send-email).  From must be an address on a
// domain verified in Resend (SPF and DKIM records), like "S4NDMoD26 <accounts@s4ndmod.com>".  Plain text only: the mails carry one link.
// It never logs the body, which holds a one-time token, nor the key.
type ResendMailer struct {
	APIKey  string
	From    string
	ReplyTo string       // optional
	URL     string       // https://api.resend.com/emails when empty (tests point it at a fake)
	Client  *http.Client // a 15 s timeout when nil
}

func (m *ResendMailer) Send(to, subject, body string) error {
	url := m.URL
	if url == "" {
		url = "https://api.resend.com/emails"
	}
	c := m.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	msg := map[string]any{"from": m.From, "to": []string{to}, "subject": subject, "text": body}
	if m.ReplyTo != "" {
		msg["reply_to"] = m.ReplyTo
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("resend: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	log.Printf("accounts: MAIL sent: %s", subject)
	return nil
}

// LogMailer writes each mail to the log.
type LogMailer struct{}

func (LogMailer) Send(to, subject, body string) error {
	log.Printf("accounts: MAIL to %s: %s\n%s", to, subject, body)
	return nil
}

// OutboxMailer writes each mail to a file in Dir (and logs the subject).
type OutboxMailer struct {
	Dir string
	mu  sync.Mutex
	n   int
}

func (m *OutboxMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%03d.txt", time.Now().Unix(), m.n)
	log.Printf("accounts: MAIL to %s: %s (%s)", to, subject, name)
	return os.WriteFile(filepath.Join(m.Dir, name), []byte("To: "+to+"\nSubject: "+subject+"\n\n"+body), 0o600)
}

// Capture keeps mails in memory (tests).
type Capture struct {
	mu   sync.Mutex
	Mail []Sent
}

type Sent struct{ To, Subject, Body string }

func (c *Capture) Send(to, subject, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Mail = append(c.Mail, Sent{to, subject, body})
	return nil
}

// Last returns the most recent mail, if any.
func (c *Capture) Last() (Sent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.Mail) == 0 {
		return Sent{}, false
	}
	return c.Mail[len(c.Mail)-1], true
}

// Token pulls the one-time token out of the last mail (it follows "#verify=" or "#reset=").
func (c *Capture) Token() string {
	m, ok := c.Last()
	if !ok {
		return ""
	}
	i := strings.LastIndex(m.Body, "=")
	if i < 0 {
		return ""
	}
	return strings.Fields(m.Body[i+1:])[0]
}
