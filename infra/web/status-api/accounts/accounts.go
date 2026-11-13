// Package accounts holds the site's own accounts: a username, an email and an argon2id password, sessions kept as
// hashed tokens, and the permissions that say what an account may do besides playing.  There is no third-party login.
//
// Permissions (Perm*) are granted by an admin (or, for demo_upload, by redeeming an old demo key, which a later step
// adds).  Anything that acts on a permission must also require Account.Verified: an address nobody has confirmed
// proves nothing.  Players without an account can always play; nothing here is needed for that.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Permissions.
const (
	PermDemoUpload = "demo_upload" // upload old client demos
	PermHostServer = "host_server" // register game servers
	PermAdmin      = "admin"       // grant permissions, approve servers
)

// Permissions lists the valid names.
var Permissions = []string{PermDemoUpload, PermHostServer, PermAdmin}

func validPermission(p string) bool {
	for _, q := range Permissions {
		if p == q {
			return true
		}
	}
	return false
}

var (
	ErrUsername  = errors.New("usernames are 3 to 24 letters, digits, _ or -")
	ErrEmail     = errors.New("that does not look like an email address")
	ErrPassword  = fmt.Errorf("passwords are %d to %d characters and may not be your username", MinPassword, MaxPassword)
	ErrTaken     = errors.New("that username or email is already used")
	ErrLogin     = errors.New("wrong username or password")
	ErrThrottled = errors.New("too many tries, wait a few minutes")
	ErrNoAccount = errors.New("no such account")
	ErrBadPerm   = errors.New("unknown permission")
	ErrToken     = errors.New("that link has expired or was already used")
	ErrKeyUsed   = errors.New("that demo key is already used by another account")
	ErrSuspended = errors.New("this account is suspended: ask an admin")
	ErrSelf      = errors.New("use your own account page for that")
	ErrLastAdmin = errors.New("that is the last active admin")
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,24}$`)

// Account is who a request is made by.
type Account struct {
	ID           uint64
	Username     string
	Email        string
	Verified     bool // the email address has been confirmed
	Suspended    bool // an admin suspended it: no login, no permissions
	SuspendedWhy string
	Permissions  []string
	Created      time.Time
}

// Has reports whether the account holds p, has a confirmed email address and is not suspended.
func (a Account) Has(p string) bool {
	if !a.Verified || a.Suspended {
		return false
	}
	for _, q := range a.Permissions {
		if q == p {
			return true
		}
	}
	return false
}

const (
	SessionTTL = 30 * 24 * time.Hour
	maxHashing = 4 // password hashes at once: each takes 19 MiB
)

// Service is the account store.
type Service struct {
	pool *pgxpool.Pool
	Now  func() time.Time

	Mail      Mailer // LogMailer when nil
	PublicURL string // the site's address, for the links in mails (https://s4ndmod.com)

	hashing chan struct{}
	dummy   string // a hash to check against when the username does not exist, so the time does not tell
	rl      limiter
}

// New wraps a pool.  Call Migrate before use.
func New(pool *pgxpool.Pool) *Service {
	s := &Service{pool: pool, hashing: make(chan struct{}, maxHashing)}
	s.dummy, _ = hashPassword("not a real password")
	return s
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Migrate applies the schema files not yet applied, recorded in account_schema_migrations.
func (s *Service) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7438203)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7438203)`)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS account_schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %q: name must start with a number", name)
		}
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Printf("accounts: applied migration %s", name)
	}
	return nil
}

// hash and verify run argon2 with a cap on how many are at once.
func (s *Service) hash(ctx context.Context, pw string) (string, error) {
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return hashPassword(pw)
}

func (s *Service) verify(ctx context.Context, pw, encoded string) bool {
	select {
	case s.hashing <- struct{}{}:
		defer func() { <-s.hashing }()
	case <-ctx.Done():
		return false
	}
	ok, _ := checkPassword(pw, encoded)
	return ok
}

func checkNewPassword(pw, username string) error {
	if len(pw) < MinPassword || len(pw) > MaxPassword || strings.EqualFold(pw, username) {
		return ErrPassword
	}
	return nil
}

func normalizeEmail(e string) (string, error) {
	e = strings.TrimSpace(e)
	a, err := mail.ParseAddress(e)
	if err != nil || a.Name != "" || a.Address != e || len(e) > 254 || !strings.Contains(e[strings.LastIndex(e, "@"):], ".") {
		return "", ErrEmail
	}
	return e, nil
}

// Register makes an account with no permissions and an unconfirmed email.
func (s *Service) Register(ctx context.Context, username, email, password string) (Account, error) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		return Account{}, ErrUsername
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return Account{}, err
	}
	if err := checkNewPassword(password, username); err != nil {
		return Account{}, err
	}
	h, err := s.hash(ctx, password)
	if err != nil {
		return Account{}, err
	}
	var id int64
	var created time.Time
	err = s.pool.QueryRow(ctx, `INSERT INTO accounts (username, username_key, email, email_key, password_hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		username, strings.ToLower(username), email, strings.ToLower(email), h, s.now()).Scan(&id, &created)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Account{}, ErrTaken
	}
	if err != nil {
		return Account{}, err
	}
	a := Account{ID: uint64(id), Username: username, Email: email, Created: created}
	s.sendVerification(ctx, a)
	return a, nil
}

// Login checks the password (by username or email) and returns a new session token to give to the browser.
func (s *Service) Login(ctx context.Context, login, password, ip string) (string, Account, error) {
	key := strings.ToLower(strings.TrimSpace(login))
	now := s.now()
	if !s.rl.allow("u:"+ip+"|"+key, 5, 10*time.Minute, now) || !s.rl.allow("i:"+ip, 30, 10*time.Minute, now) {
		return "", Account{}, ErrThrottled
	}
	var id int64
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT id, password_hash FROM accounts WHERE username_key = $1 OR email_key = $1`, key).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		s.verify(ctx, password, s.dummy)
		return "", Account{}, ErrLogin
	}
	if err != nil {
		return "", Account{}, err
	}
	if len(password) > MaxPassword || !s.verify(ctx, password, hash) {
		return "", Account{}, ErrLogin
	}
	s.rl.forget("u:" + ip + "|" + key)
	a, err := s.ByID(ctx, uint64(id))
	if err != nil {
		return "", Account{}, err
	}
	if a.Suspended { // said only to someone who knows the password
		return "", Account{}, ErrSuspended
	}
	token, err := s.newSession(ctx, uint64(id))
	return token, a, err
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

func (s *Service) newSession(ctx context.Context, id uint64) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now()
	s.pool.Exec(ctx, `DELETE FROM account_sessions WHERE expires_at < $1`, now)
	_, err := s.pool.Exec(ctx, `INSERT INTO account_sessions (token_hash, account_id, created_at, expires_at) VALUES ($1, $2, $3, $4)`,
		hashToken(token), int64(id), now, now.Add(SessionTTL))
	return token, err
}

// BySession finds the account a session token belongs to.
func (s *Service) BySession(ctx context.Context, token string) (Account, bool) {
	if len(token) < 20 || len(token) > 100 {
		return Account{}, false
	}
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT account_id FROM account_sessions WHERE token_hash = $1 AND expires_at > $2`, hashToken(token), s.now()).Scan(&id)
	if err != nil {
		return Account{}, false
	}
	a, err := s.ByID(ctx, uint64(id))
	return a, err == nil && !a.Suspended
}

// Logout ends one session.
func (s *Service) Logout(ctx context.Context, token string) {
	s.pool.Exec(ctx, `DELETE FROM account_sessions WHERE token_hash = $1`, hashToken(token))
}

const accountSelect = `SELECT a.id, a.username, a.email, a.email_verified_at IS NOT NULL, a.created_at, a.suspended_at IS NOT NULL, a.suspended_reason,
	COALESCE((SELECT array_agg(permission ORDER BY permission) FROM account_permissions p WHERE p.account_id = a.id), '{}') FROM accounts a`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	var id int64
	err := row.Scan(&id, &a.Username, &a.Email, &a.Verified, &a.Created, &a.Suspended, &a.SuspendedWhy, &a.Permissions)
	a.ID = uint64(id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

func (s *Service) ByID(ctx context.Context, id uint64) (Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, accountSelect+` WHERE a.id = $1`, int64(id)))
}

func (s *Service) ByUsername(ctx context.Context, name string) (Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, accountSelect+` WHERE a.username_key = $1`, strings.ToLower(strings.TrimSpace(name))))
}

// List returns one page of the accounts whose username or email starts with q (all when q is empty), newest first,
// and how many match in all.
func (s *Service) List(ctx context.Context, q string, limit, offset int) ([]Account, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(strings.TrimSpace(q))) + "%"
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts a WHERE a.username_key LIKE $1 OR a.email_key LIKE $1`, q).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, accountSelect+` WHERE a.username_key LIKE $1 OR a.email_key LIKE $1 ORDER BY a.id DESC LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// ChangePassword checks the current password, sets the new one, and ends every other session.
func (s *Service) ChangePassword(ctx context.Context, id uint64, current, next, keepToken string) error {
	var hash, username string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash, username FROM accounts WHERE id = $1`, int64(id)).Scan(&hash, &username); err != nil {
		return ErrNoAccount
	}
	if !s.rl.allow("p:"+strconv.FormatUint(id, 10), 5, 10*time.Minute, s.now()) {
		return ErrThrottled
	}
	if len(current) > MaxPassword || !s.verify(ctx, current, hash) {
		return ErrLogin
	}
	if err := checkNewPassword(next, username); err != nil {
		return err
	}
	h, err := s.hash(ctx, next)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET password_hash = $2 WHERE id = $1`, int64(id), h); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM account_sessions WHERE account_id = $1 AND token_hash <> $2`, int64(id), hashToken(keepToken))
	return err
}

// Delete removes the account after checking the password; its sessions and permissions go with it.
func (s *Service) Delete(ctx context.Context, id uint64, password string) error {
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE id = $1`, int64(id)).Scan(&hash); err != nil {
		return ErrNoAccount
	}
	if !s.rl.allow("p:"+strconv.FormatUint(id, 10), 5, 10*time.Minute, s.now()) {
		return ErrThrottled
	}
	if len(password) > MaxPassword || !s.verify(ctx, password, hash) {
		return ErrLogin
	}
	if a, err := s.ByID(ctx, id); err == nil && a.Has(PermAdmin) {
		if n, err := s.activeAdmins(ctx, id); err == nil && n == 0 {
			return ErrLastAdmin // make another admin first
		}
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, int64(id))
	return err
}

// Grant gives an account a permission; by is the admin doing it (0 for the command line).
func (s *Service) Grant(ctx context.Context, id uint64, perm string, by uint64) error {
	if !validPermission(perm) {
		return ErrBadPerm
	}
	var granter any
	if by != 0 {
		granter = int64(by)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO account_permissions (account_id, permission, granted_by, granted_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id, permission) DO NOTHING`, int64(id), perm, granter, s.now())
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		return ErrNoAccount
	}
	return err
}

// Revoke takes a permission away.
func (s *Service) Revoke(ctx context.Context, id uint64, perm string) error {
	if !validPermission(perm) {
		return ErrBadPerm
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM account_permissions WHERE account_id = $1 AND permission = $2`, int64(id), perm)
	return err
}

// MarkVerified records that the account's email was confirmed (the mail step uses it; accountsctl can too).
func (s *Service) MarkVerified(ctx context.Context, id uint64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE accounts SET email_verified_at = COALESCE(email_verified_at, $2) WHERE id = $1`, int64(id), s.now())
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNoAccount
	}
	return err
}

// limiter counts events per key in a sliding window.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *limiter) allow(key string, max int, window time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	if len(l.hits) > 10000 { // bound memory: forget the quiet keys
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > window {
				delete(l.hits, k)
			}
		}
	}
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= max {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

func (l *limiter) forget(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

const (
	purposeVerify = "verify"
	purposeReset  = "reset"
	verifyTTL     = 48 * time.Hour
	resetTTL      = time.Hour
)

func (s *Service) mailer() Mailer {
	if s.Mail != nil {
		return s.Mail
	}
	return LogMailer{}
}

// newToken stores a one-time token for the account and returns it (only the hash is kept).  Older unused tokens of
// the same purpose are dropped, so only the latest mail works.
func (s *Service) newToken(ctx context.Context, id uint64, purpose string, ttl time.Duration) (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now()
	s.pool.Exec(ctx, `DELETE FROM account_tokens WHERE expires_at < $1 OR (account_id = $2 AND purpose = $3)`, now, int64(id), purpose)
	_, err := s.pool.Exec(ctx, `INSERT INTO account_tokens (token_hash, account_id, purpose, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		hashToken(tok), int64(id), purpose, now, now.Add(ttl))
	return tok, err
}

// useToken checks and spends a token, returning the account it was made for.
func (s *Service) useToken(ctx context.Context, tok, purpose string) (uint64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `DELETE FROM account_tokens WHERE token_hash = $1 AND purpose = $2 AND expires_at > $3 RETURNING account_id`,
		hashToken(tok), purpose, s.now()).Scan(&id)
	if err != nil {
		return 0, ErrToken
	}
	return uint64(id), nil
}

func (s *Service) sendVerification(ctx context.Context, a Account) {
	tok, err := s.newToken(ctx, a.ID, purposeVerify, verifyTTL)
	if err != nil {
		log.Printf("accounts: verification token: %v", err)
		return
	}
	body := fmt.Sprintf("Hello %s,\n\nconfirm your email address for your s4ndmod.com account:\n\n%s/account/#verify=%s\n\nThe link works once and for 48 hours.  If you did not make this account, ignore this mail.\n",
		a.Username, strings.TrimRight(s.PublicURL, "/"), tok)
	if err := s.mailer().Send(a.Email, "Confirm your s4ndmod.com email", body); err != nil {
		log.Printf("accounts: could not send mail to account %d: %v", a.ID, err)
	}
}

// ResendVerification mails a new confirmation link to an account that has not confirmed yet.
func (s *Service) ResendVerification(ctx context.Context, id uint64) error {
	a, err := s.ByID(ctx, id)
	if err != nil {
		return err
	}
	if a.Verified {
		return nil
	}
	if !s.rl.allow("mail:"+strconv.FormatUint(id, 10), 3, time.Hour, s.now()) {
		return ErrThrottled
	}
	s.sendVerification(ctx, a)
	return nil
}

// Verify spends a confirmation token and marks the address confirmed.
func (s *Service) Verify(ctx context.Context, tok string) error {
	id, err := s.useToken(ctx, tok, purposeVerify)
	if err != nil {
		return err
	}
	return s.MarkVerified(ctx, id)
}

// RequestReset mails a reset link when the address belongs to an account.  It says nothing either way, so it cannot be
// used to find out who has an account.
func (s *Service) RequestReset(ctx context.Context, email, ip string) error {
	key := strings.ToLower(strings.TrimSpace(email))
	if !s.rl.allow("rst:"+ip, 5, time.Hour, s.now()) || !s.rl.allow("rst:"+key, 3, time.Hour, s.now()) {
		return ErrThrottled
	}
	a, err := scanAccount(s.pool.QueryRow(ctx, accountSelect+` WHERE a.email_key = $1`, key))
	if err != nil {
		return nil
	}
	tok, err := s.newToken(ctx, a.ID, purposeReset, resetTTL)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("Hello %s,\n\nsomeone asked to reset the password of your s4ndmod.com account.  To choose a new one:\n\n%s/account/#reset=%s\n\nThe link works once and for one hour.  If it was not you, ignore this mail: your password has not changed.\n",
		a.Username, strings.TrimRight(s.PublicURL, "/"), tok)
	if err := s.mailer().Send(a.Email, "Reset your s4ndmod.com password", body); err != nil {
		log.Printf("accounts: could not send mail to account %d: %v", a.ID, err)
	}
	return nil
}

// ConfirmReset sets a new password with a reset token.  Every session of the account ends.  Reaching the mailbox
// also proves the address, so it is marked confirmed.
func (s *Service) ConfirmReset(ctx context.Context, tok, next string) error {
	var username string
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT a.id, a.username FROM account_tokens t JOIN accounts a ON a.id = t.account_id
		WHERE t.token_hash = $1 AND t.purpose = $2 AND t.expires_at > $3`, hashToken(tok), purposeReset, s.now()).Scan(&id, &username)
	if err != nil {
		return ErrToken
	}
	if err := checkNewPassword(next, username); err != nil {
		return err // the token stays valid for another try
	}
	h, err := s.hash(ctx, next)
	if err != nil {
		return err
	}
	if _, err := s.useToken(ctx, tok, purposeReset); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET password_hash = $2 WHERE id = $1`, id, h); err != nil {
		return err
	}
	s.pool.Exec(ctx, `DELETE FROM account_sessions WHERE account_id = $1`, id)
	return s.MarkVerified(ctx, uint64(id))
}

// RedeemDemoKey ties an old demo-upload key (by its label; the caller has checked the secret) to the account and
// grants demo_upload.  A key belongs to one account; redeeming it again from the same account is fine.
func (s *Service) RedeemDemoKey(ctx context.Context, id uint64, label string) error {
	var owner int64
	err := s.pool.QueryRow(ctx, `INSERT INTO account_demo_keys (label, account_id, redeemed_at) VALUES ($1, $2, $3)
		ON CONFLICT (label) DO UPDATE SET label = EXCLUDED.label RETURNING account_id`, label, int64(id), s.now()).Scan(&owner)
	if err != nil {
		return err
	}
	if uint64(owner) != id {
		return ErrKeyUsed
	}
	return s.Grant(ctx, id, PermDemoUpload, 0)
}

// AccountOfDemoKey says which account redeemed the key with this label.
func (s *Service) AccountOfDemoKey(ctx context.Context, label string) (uint64, bool) {
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT account_id FROM account_demo_keys WHERE label = $1`, label).Scan(&id); err != nil {
		return 0, false
	}
	return uint64(id), true
}

// DemoKeyLabels lists the old keys an account has redeemed.
func (s *Service) DemoKeyLabels(ctx context.Context, id uint64) []string {
	rows, err := s.pool.Query(ctx, `SELECT label FROM account_demo_keys WHERE account_id = $1 ORDER BY label`, int64(id))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if rows.Scan(&l) == nil {
			out = append(out, l)
		}
	}
	return out
}

// activeAdmins counts the admins who can act (permission, confirmed email, not suspended), leaving out one account.
func (s *Service) activeAdmins(ctx context.Context, except uint64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts a JOIN account_permissions p ON p.account_id = a.id AND p.permission = $1
		WHERE a.email_verified_at IS NOT NULL AND a.suspended_at IS NULL AND a.id <> $2`, PermAdmin, int64(except)).Scan(&n)
	return n, err
}

// guardTarget refuses to act on the admin's own account, or on the last admin who could still act.
func (s *Service) guardTarget(ctx context.Context, admin Account, target Account) error {
	if admin.ID == target.ID {
		return ErrSelf
	}
	if target.Has(PermAdmin) {
		if n, err := s.activeAdmins(ctx, target.ID); err != nil {
			return err
		} else if n == 0 {
			return ErrLastAdmin
		}
	}
	return nil
}

// AdminDelete removes another account (the admin needs no password).  Everything of the account goes with it: its
// permissions, sessions, mailed links, linked demo key.  What it uploaded stays unless the caller purged it first.
func (s *Service) AdminDelete(ctx context.Context, admin Account, id uint64) (Account, error) {
	t, err := s.ByID(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if err := s.guardTarget(ctx, admin, t); err != nil {
		return t, err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, int64(id))
	return t, err
}

// Suspend stops an account from logging in or using its permissions, and ends its sessions.
func (s *Service) Suspend(ctx context.Context, admin Account, id uint64, reason string) (Account, error) {
	t, err := s.ByID(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if err := s.guardTarget(ctx, admin, t); err != nil {
		return t, err
	}
	if len(reason) > 300 {
		reason = reason[:300]
	}
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET suspended_at = COALESCE(suspended_at, $2), suspended_reason = $3 WHERE id = $1`, int64(id), s.now(), reason); err != nil {
		return t, err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM account_sessions WHERE account_id = $1`, int64(id))
	return t, err
}

// Unsuspend lets a suspended account back in.
func (s *Service) Unsuspend(ctx context.Context, id uint64) (Account, error) {
	t, err := s.ByID(ctx, id)
	if err != nil {
		return Account{}, err
	}
	_, err = s.pool.Exec(ctx, `UPDATE accounts SET suspended_at = NULL, suspended_reason = '' WHERE id = $1`, int64(id))
	return t, err
}

// AuditEntry is one line of the admin log.
type AuditEntry struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	ActorID    uint64    `json:"actor_id"`
	ActorName  string    `json:"actor"`
	Action     string    `json:"action"`
	TargetID   uint64    `json:"target_id"`
	TargetName string    `json:"target"`
	Detail     string    `json:"detail"`
}

// Audit records something an admin did.  A failure to write it is logged, not returned: the action has happened.
func (s *Service) Audit(ctx context.Context, actor Account, action string, target Account, detail string) {
	var actorID, targetID any
	if actor.ID != 0 {
		actorID = int64(actor.ID)
	}
	if target.ID != 0 {
		targetID = int64(target.ID)
	}
	if len(detail) > 300 {
		detail = detail[:300]
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO account_audit (at, actor_id, actor_name, action, target_id, target_name, detail) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		s.now(), actorID, actor.Username, action, targetID, target.Username, detail); err != nil {
		log.Printf("accounts: audit %s: %v", action, err)
	}
}

// AuditPage returns one page of the log, newest first, and the total.
func (s *Service) AuditPage(ctx context.Context, limit, offset int) ([]AuditEntry, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM account_audit`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, at, COALESCE(actor_id, 0), actor_name, action, COALESCE(target_id, 0), target_name, detail
		FROM account_audit ORDER BY at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorName, &e.Action, &e.TargetID, &e.TargetName, &e.Detail); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
