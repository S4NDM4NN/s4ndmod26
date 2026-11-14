package accounts

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Mailer sends the account emails.  There is no real sender yet: LogMailer prints them (and OutboxMailer writes
// them to a folder), which is enough for development and for tests.  An SMTP or provider sender slots in here.
type Mailer interface {
	Send(to, subject, body string) error
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
