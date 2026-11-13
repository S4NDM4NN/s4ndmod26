// Package servers is the registry of community game servers.  A server is listed only if it is one others can really
// join: reachable over UDP and over a secure WebSocket with a certificate browsers accept (package probe checks both,
// from outside).  The flow:
//
//  1. an account with host_server asks for a one-time token (NewToken);
//  2. the server's agent sends a claim signed with its own key (Claim): the token, its id, and its public addresses;
//  3. the site probes both addresses.  If either fails the claim is refused, the token is kept, and the answer says
//     what failed, so the host can fix it and the agent can try again;
//  4. otherwise the server is pending until an admin approves it (Approve needs the probes to pass);
//  5. the site keeps probing (ProbeAll): a server is listed only while both probes pass.
//
// The server's identity is its key, not its address, so a changed IP only needs a new claim (no token).
package servers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"s4ndmod26/status-api/probe"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusBlocked  = "blocked"

	TokenTTL      = 7 * 24 * time.Hour
	ListedWithin  = 15 * time.Minute // a server whose probes last passed longer ago is not listed
	MaxNameLength = 60
)

var (
	ErrNoToken     = errors.New("a registration token is needed for a server that is not registered yet")
	ErrBadToken    = errors.New("that registration token is not valid (it may have expired or been used)")
	ErrBlocked     = errors.New("this server has been blocked")
	ErrKeyChanged  = errors.New("that server id is registered with a different key")
	ErrAddrTaken   = errors.New("that address is already registered to another server")
	ErrTooMany     = errors.New("you have reached the number of servers you may register")
	ErrNotFound    = errors.New("no such server")
	ErrNotOwner    = errors.New("that server is not yours")
	ErrUnreachable = errors.New("the server is not reachable over both UDP and a secure WebSocket right now")
	ErrBadRequest  = errors.New("bad request")
)

// ProbeFailure carries what the probes found when a registration was refused.
type ProbeFailure struct{ UDP, WSS Check }

func (e *ProbeFailure) Error() string {
	return "the server is not reachable: " + e.UDP.Error + " " + e.WSS.Error
}

// Check is one probe's outcome.
type Check struct {
	OK      bool       `json:"ok"`
	Error   string     `json:"error,omitempty"`
	Checked *time.Time `json:"checked,omitempty"`
}

// Server is a registered server as the API shows it.
type Server struct {
	ID         int64      `json:"id"`
	ServerID   string     `json:"server_id"`
	Name       string     `json:"name"`
	OwnerID    uint64     `json:"owner_id,omitempty"`
	Owner      string     `json:"owner,omitempty"`
	GameAddr   string     `json:"game_addr"`
	WSSURL     string     `json:"wss_url"`
	Status     string     `json:"status"`
	Official   bool       `json:"official"`
	Created    time.Time  `json:"created"`
	LastOK     *time.Time `json:"last_ok,omitempty"`
	UDP        Check      `json:"udp"`
	WSS        Check      `json:"wss"`
	Hostname   string     `json:"hostname,omitempty"`
	Map        string     `json:"map,omitempty"`
	Players    int        `json:"players"`
	MaxPlayers int        `json:"max_players,omitempty"`
	PublicKey  []byte     `json:"-"`
}

// Service is the registry.
type Service struct {
	pool   *pgxpool.Pool
	Prober probe.Prober
	Now    func() time.Time
	// MaxPerAccount is how many servers (and open tokens) one account may have (5), unless an admin set another number for the account.
	MaxPerAccount int
}

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool, MaxPerAccount: 5} }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Migrate applies the schema files not yet applied (server_schema_migrations).  The accounts schema must exist.
func (s *Service) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(7438204)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7438204)`)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS server_schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
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
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM server_schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
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
		if _, err := tx.Exec(ctx, `INSERT INTO server_schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Printf("servers: applied migration %s", name)
	}
	return nil
}

// ---- ids and addresses ----

var idEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// IDOf is the server id derived from a public key (the agent computes the same).
func IDOf(pub []byte) string {
	sum := sha256.Sum256(pub)
	return strings.ToLower(idEnc.EncodeToString(sum[:10]))
}

// normalizeGameAddr checks host:port and returns it in lower case.
func normalizeGameAddr(a string) (string, error) {
	a = strings.ToLower(strings.TrimSpace(a))
	host, ps, err := net.SplitHostPort(a)
	if err != nil || host == "" {
		return "", fmt.Errorf("%w: the game address must be host:port", ErrBadRequest)
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 1024 || port > 65535 {
		return "", fmt.Errorf("%w: the game port must be 1024-65535", ErrBadRequest)
	}
	if strings.ContainsAny(host, " /\\@") || len(a) > 200 {
		return "", fmt.Errorf("%w: bad game address", ErrBadRequest)
	}
	return net.JoinHostPort(host, ps), nil
}

// normalizeWSS checks wss://host[:port]/path and returns it with the host in lower case.
func normalizeWSS(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 200 {
		return "", fmt.Errorf("%w: the WebSocket address must look like wss://host[:port]/ws", ErrBadRequest)
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || (n != 443 && n < 1024) || n > 65535 {
			return "", fmt.Errorf("%w: the WebSocket port must be 443 or 1024-65535", ErrBadRequest)
		}
	}
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" {
		u.Path = "/ws"
	}
	return u.String(), nil
}

func hashToken(t string) []byte { h := sha256.Sum256([]byte(t)); return h[:] }

// ---- tokens ----

// NewToken makes a registration token for the account (the caller has checked host_server).  The token is returned
// once; only its hash is kept.
func (s *Service) NewToken(ctx context.Context, account uint64, name string) (string, time.Time, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxNameLength || strings.ContainsAny(name, "\n\r") {
		return "", time.Time{}, fmt.Errorf("%w: give the server a name of up to %d characters", ErrBadRequest, MaxNameLength)
	}
	now := s.now()
	s.pool.Exec(ctx, `DELETE FROM server_tokens WHERE expires_at < $1`, now)
	limit, err := s.LimitOf(ctx, account)
	if err != nil {
		return "", time.Time{}, err
	}
	var have int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM servers WHERE owner_id = $1) + (SELECT count(*) FROM server_tokens WHERE account_id = $1)`, int64(account)).Scan(&have); err != nil {
		return "", time.Time{}, err
	}
	if have >= limit {
		return "", time.Time{}, ErrTooMany
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	exp := now.Add(TokenTTL)
	_, err = s.pool.Exec(ctx, `INSERT INTO server_tokens (token_hash, account_id, name, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`, hashToken(tok), int64(account), name, now, exp)
	return tok, exp, err
}

// LimitOf is how many servers (and open tokens) the account may have: its own number if an admin set one, else the default.
func (s *Service) LimitOf(ctx context.Context, account uint64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT max_servers FROM server_limits WHERE account_id = $1`, int64(account)).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.MaxPerAccount, nil
	}
	return n, err
}

// SetLimit gives the account its own limit; a negative n goes back to the default.
func (s *Service) SetLimit(ctx context.Context, account uint64, n int) error {
	if n < 0 {
		_, err := s.pool.Exec(ctx, `DELETE FROM server_limits WHERE account_id = $1`, int64(account))
		return err
	}
	if n > 1000 {
		return fmt.Errorf("%w: the limit is at most 1000", ErrBadRequest)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO server_limits (account_id, max_servers) VALUES ($1,$2) ON CONFLICT (account_id) DO UPDATE SET max_servers = $2`, int64(account), n)
	return err
}

// OpenToken is a token not yet used (what the host sees, without the secret).
type OpenToken struct {
	Name        string     `json:"name"`
	Created     time.Time  `json:"created"`
	Expires     time.Time  `json:"expires"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"` // the last claim made with it that was refused ...
	LastError   string     `json:"last_error,omitempty"`   // ... and why
}

// TokensOf lists the account's unused tokens.
func (s *Service) TokensOf(ctx context.Context, account uint64) ([]OpenToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, created_at, expires_at, last_attempt, last_error FROM server_tokens WHERE account_id = $1 AND expires_at > $2 ORDER BY created_at`, int64(account), s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpenToken{}
	for rows.Next() {
		var t OpenToken
		if err := rows.Scan(&t.Name, &t.Created, &t.Expires, &t.LastAttempt, &t.LastError); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- the claim ----

// ClaimBody is what an agent signs (the exact bytes go in X-S4nd-Signature, ed25519, base64).
type ClaimBody struct {
	ServerID  string `json:"server_id"`
	PublicKey string `json:"public_key"` // standard base64
	Token     string `json:"token,omitempty"`
	GameAddr  string `json:"game_addr"`
	WSSURL    string `json:"wss_url"`
	Sent      int64  `json:"sent"` // Unix seconds
}

// ClaimMaxAge is how old a signed claim may be.
const ClaimMaxAge = 2 * time.Minute

// VerifyClaim checks the signature, the freshness and that the id is the one the key gives.
func (s *Service) VerifyClaim(body []byte, sigB64 string, cb ClaimBody) (ed25519.PublicKey, error) {
	pub, err := base64.StdEncoding.DecodeString(cb.PublicKey)
	sig, err2 := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, body, sig) {
		return nil, errors.New("bad signature")
	}
	if IDOf(pub) != strings.ToLower(cb.ServerID) {
		return nil, errors.New("the server id does not match its key")
	}
	if d := s.now().Sub(time.Unix(cb.Sent, 0)); d > ClaimMaxAge || d < -ClaimMaxAge {
		return nil, errors.New("the claim is stale (is the server's clock right?)")
	}
	return pub, nil
}

func (s *Service) probeBoth(ctx context.Context, gameAddr, wssURL string) (udp, wss probe.Result) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); udp = s.Prober.UDP(ctx, gameAddr) }()
	go func() { defer wg.Done(); wss = s.Prober.WSS(ctx, wssURL) }()
	wg.Wait()
	return
}

// Claim registers a server, or refreshes a registered one (a new address, a re-probe).  The signature has been
// checked.  A first registration needs a token; if either probe fails nothing is created, the token stays, and a
// *ProbeFailure says what to fix.
func (s *Service) Claim(ctx context.Context, cb ClaimBody, pub ed25519.PublicKey) (Server, error) {
	gameAddr, err := normalizeGameAddr(cb.GameAddr)
	if err != nil {
		return Server{}, err
	}
	wssURL, err := normalizeWSS(cb.WSSURL)
	if err != nil {
		return Server{}, err
	}
	sid := strings.ToLower(cb.ServerID)
	now := s.now()

	var existing *Server
	if sv, err := s.bySID(ctx, sid); err == nil {
		existing = &sv
	} else if !errors.Is(err, ErrNotFound) {
		return Server{}, err
	}
	var tokenAccount int64
	var tokenName string
	if existing != nil {
		if string(existing.PublicKey) != string(pub) {
			return Server{}, ErrKeyChanged
		}
		if existing.Status == StatusBlocked {
			return Server{}, ErrBlocked
		}
	} else {
		if cb.Token == "" {
			return Server{}, ErrNoToken
		}
		if err := s.pool.QueryRow(ctx, `SELECT account_id, name FROM server_tokens WHERE token_hash = $1 AND expires_at > $2`, hashToken(cb.Token), now).Scan(&tokenAccount, &tokenName); err != nil {
			return Server{}, ErrBadToken
		}
	}
	// the address must not belong to another server
	var other string
	err = s.pool.QueryRow(ctx, `SELECT server_id FROM servers WHERE (game_addr = $1 OR wss_url = $2) AND server_id <> $3 LIMIT 1`, gameAddr, wssURL, sid).Scan(&other)
	if err == nil {
		return Server{}, ErrAddrTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Server{}, err
	}

	var udp, wss probe.Result
	if existing != nil && existing.Official {
		udp, wss = probe.Result{OK: true}, probe.Result{OK: true}
	} else {
		udp, wss = s.probeBoth(ctx, gameAddr, wssURL)
	}

	if existing != nil {
		if err := s.store(ctx, existing.ID, gameAddr, wssURL, udp, wss, true); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				return Server{}, ErrAddrTaken
			}
			return Server{}, err
		}
		return s.bySID(ctx, sid)
	}
	if !udp.OK || !wss.OK {
		pf := &ProbeFailure{UDP: Check{OK: udp.OK, Error: udp.Error}, WSS: Check{OK: wss.OK, Error: wss.Error}}
		why := strings.TrimSpace(strings.TrimSpace(map[bool]string{true: "", false: udp.Error}[udp.OK]) + " " + map[bool]string{true: "", false: wss.Error}[wss.OK])
		s.pool.Exec(ctx, `UPDATE server_tokens SET last_attempt = $2, last_error = $3 WHERE token_hash = $1`, hashToken(cb.Token), now, why)
		return Server{}, pf
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Server{}, err
	}
	defer tx.Rollback(ctx)
	if tag, err := tx.Exec(ctx, `DELETE FROM server_tokens WHERE token_hash = $1`, hashToken(cb.Token)); err != nil || tag.RowsAffected() != 1 {
		return Server{}, ErrBadToken // spent by a parallel claim
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO servers (server_id, public_key, owner_id, name, game_addr, wss_url, status, created_at, last_claim_at,
			udp_ok, udp_checked, ws_ok, ws_checked, last_ok_at, hostname, map, players, max_players)
		VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,$7, true,$7,true,$7,$7, $8,$9,$10,$11) RETURNING id`,
		sid, []byte(pub), tokenAccount, tokenName, gameAddr, wssURL, now, udp.Status.Hostname, udp.Status.Map, udp.Status.Players, udp.Status.MaxPlayers).Scan(&id)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Server{}, ErrAddrTaken
	}
	if err != nil {
		return Server{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Server{}, err
	}
	return s.bySID(ctx, sid)
}

// store writes a probe round (and optionally new addresses) for a server.
func (s *Service) store(ctx context.Context, id int64, gameAddr, wssURL string, udp, wss probe.Result, claimed bool) error {
	now := s.now()
	var claim any
	if claimed {
		claim = now
	}
	both := udp.OK && wss.OK
	host, mp, pl, mx := udp.Status.Hostname, udp.Status.Map, udp.Status.Players, udp.Status.MaxPlayers
	_, err := s.pool.Exec(ctx, `UPDATE servers SET
			game_addr = COALESCE(NULLIF($2,''), game_addr), wss_url = COALESCE(NULLIF($3,''), wss_url),
			last_claim_at = COALESCE($4, last_claim_at),
			udp_ok = $5, udp_error = $6, udp_checked = $9, ws_ok = $7, ws_error = $8, ws_checked = $9,
			last_ok_at = CASE WHEN $10 THEN $9 ELSE last_ok_at END,
			hostname = CASE WHEN $5 THEN $11 ELSE hostname END, map = CASE WHEN $5 THEN $12 ELSE map END,
			players = CASE WHEN $5 THEN $13 ELSE players END, max_players = CASE WHEN $5 THEN $14 ELSE max_players END
		WHERE id = $1`, id, gameAddr, wssURL, claim, udp.OK, udp.Error, wss.OK, wss.Error, now, both, host, mp, pl, mx)
	return err
}

// ---- reading ----

const serverSelect = `SELECT s.id, s.server_id, s.public_key, COALESCE(s.owner_id, 0), COALESCE(a.username, ''), s.name, s.game_addr, s.wss_url, s.status, s.official, s.created_at,
	s.last_ok_at, s.udp_ok, s.udp_error, s.udp_checked, s.ws_ok, s.ws_error, s.ws_checked, s.hostname, s.map, s.players, s.max_players
	FROM servers s LEFT JOIN accounts a ON a.id = s.owner_id`

func scanServer(row pgx.Row) (Server, error) {
	var v Server
	var owner int64
	err := row.Scan(&v.ID, &v.ServerID, &v.PublicKey, &owner, &v.Owner, &v.Name, &v.GameAddr, &v.WSSURL, &v.Status, &v.Official, &v.Created,
		&v.LastOK, &v.UDP.OK, &v.UDP.Error, &v.UDP.Checked, &v.WSS.OK, &v.WSS.Error, &v.WSS.Checked, &v.Hostname, &v.Map, &v.Players, &v.MaxPlayers)
	v.OwnerID = uint64(owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return v, err
}

func (s *Service) bySID(ctx context.Context, sid string) (Server, error) {
	return scanServer(s.pool.QueryRow(ctx, serverSelect+` WHERE s.server_id = $1`, sid))
}

func (s *Service) ByID(ctx context.Context, id int64) (Server, error) {
	return scanServer(s.pool.QueryRow(ctx, serverSelect+` WHERE s.id = $1`, id))
}

func (s *Service) query(ctx context.Context, where string, args ...any) ([]Server, error) {
	rows, err := s.pool.Query(ctx, serverSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		v, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Mine lists the servers an account registered.
func (s *Service) Mine(ctx context.Context, account uint64) ([]Server, error) {
	return s.query(ctx, ` WHERE s.owner_id = $1 ORDER BY s.id DESC`, int64(account))
}

// List returns one page of servers for the admin, newest first, optionally of one status, and the total.
func (s *Service) List(ctx context.Context, status string, limit, offset int) ([]Server, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM servers WHERE $1 = '' OR status = $1`, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	list, err := s.query(ctx, ` WHERE $1 = '' OR s.status = $1 ORDER BY s.id DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	return list, total, err
}

const listedWhere = ` s.status = 'approved' AND (s.official OR (s.udp_ok AND s.ws_ok AND s.last_ok_at > $1))`

// Listed returns one page of the servers people can join now (approved, and both probes passed lately), and the total.
// kind is "" for all of them, "official" or "community".  Official servers come first.
func (s *Service) Listed(ctx context.Context, kind string, limit, offset int) ([]Server, int, error) {
	where := listedWhere
	switch kind {
	case "official":
		where += ` AND s.official`
	case "community":
		where += ` AND NOT s.official`
	case "":
	default:
		return nil, 0, fmt.Errorf("%w: kind is official or community", ErrBadRequest)
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	since := s.now().Add(-ListedWithin)
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM servers s WHERE`+where, since).Scan(&total); err != nil {
		return nil, 0, err
	}
	list, err := s.query(ctx, ` WHERE`+where+` ORDER BY s.official DESC, s.players DESC, s.id LIMIT $2 OFFSET $3`, since, limit, offset)
	return list, total, err
}

// Lookup finds an approved server by its id or its game address (for tickets and /link): the Directory the other
// packages use.
func (s *Service) Lookup(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var sid string
	err := s.pool.QueryRow(ctx, `SELECT server_id FROM servers WHERE status = 'approved' AND (server_id = $1 OR game_addr = $1) LIMIT 1`, name).Scan(&sid)
	return sid, err == nil
}

// ---- decisions (admin) and the owner ----

// Approve lets a pending server be listed.  It probes first: a server that is not reachable now is not approved
// (an official server is not probed).
func (s *Service) Approve(ctx context.Context, id int64, admin uint64) (Server, error) {
	v, err := s.ByID(ctx, id)
	if err != nil {
		return v, err
	}
	if !v.Official {
		udp, wss := s.probeBoth(ctx, v.GameAddr, v.WSSURL)
		if err := s.store(ctx, id, "", "", udp, wss, false); err != nil {
			return v, err
		}
		if !udp.OK || !wss.OK {
			return v, &ProbeFailure{UDP: Check{OK: udp.OK, Error: udp.Error}, WSS: Check{OK: wss.OK, Error: wss.Error}}
		}
	}
	_, err = s.pool.Exec(ctx, `UPDATE servers SET status = 'approved', decided_at = $2, decided_by = $3 WHERE id = $1`, id, s.now(), int64(admin))
	if err != nil {
		return v, err
	}
	return s.ByID(ctx, id)
}

// SetStatus blocks a server, or puts a blocked one back to pending.
func (s *Service) SetStatus(ctx context.Context, id int64, status string, admin uint64) (Server, error) {
	if status != StatusBlocked && status != StatusPending {
		return Server{}, ErrBadRequest
	}
	v, err := s.ByID(ctx, id)
	if err != nil {
		return v, err
	}
	_, err = s.pool.Exec(ctx, `UPDATE servers SET status = $2, decided_at = $3, decided_by = $4 WHERE id = $1`, id, status, s.now(), int64(admin))
	if err != nil {
		return v, err
	}
	return s.ByID(ctx, id)
}

// SetOfficial marks a server as ours (running in our cluster, so not probed from outside) or not.
func (s *Service) SetOfficial(ctx context.Context, id int64, on bool) (Server, error) {
	v, err := s.ByID(ctx, id)
	if err != nil {
		return v, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE servers SET official = $2 WHERE id = $1`, id, on); err != nil {
		return v, err
	}
	return s.ByID(ctx, id)
}

// AddOfficial registers a server of ours directly, approved and not probed from outside.  pub is its public key.
func (s *Service) AddOfficial(ctx context.Context, pub ed25519.PublicKey, name, gameAddr, wssURL string, admin uint64) (Server, error) {
	gameAddr, err := normalizeGameAddr(gameAddr)
	if err != nil {
		return Server{}, err
	}
	wssURL, err = normalizeWSS(wssURL)
	if err != nil {
		return Server{}, err
	}
	name = strings.TrimSpace(name)
	if len(pub) != ed25519.PublicKeySize || name == "" || len(name) > MaxNameLength {
		return Server{}, ErrBadRequest
	}
	now := s.now()
	_, err = s.pool.Exec(ctx, `INSERT INTO servers (server_id, public_key, name, game_addr, wss_url, status, official, created_at, decided_at, decided_by, udp_ok, ws_ok, last_ok_at)
		VALUES ($1,$2,$3,$4,$5,'approved',true,$6,$6,$7,true,true,$6)`, IDOf(pub), []byte(pub), name, gameAddr, wssURL, now, int64(admin))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Server{}, ErrAddrTaken
	}
	if err != nil {
		return Server{}, err
	}
	return s.bySID(ctx, IDOf(pub))
}

// Delete removes a registration (an admin, or the owner with owner set to their account id).  The server can claim
// again with a new token.
func (s *Service) Delete(ctx context.Context, id int64, owner uint64) (Server, error) {
	v, err := s.ByID(ctx, id)
	if err != nil {
		return v, err
	}
	if owner != 0 && v.OwnerID != owner {
		return v, ErrNotOwner
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM servers WHERE id = $1`, id)
	return v, err
}

// ---- keeping an eye on them ----

// ProbeAll probes every pending and approved server that is not official, a few at a time, and stores the results.
func (s *Service) ProbeAll(ctx context.Context) error {
	list, err := s.query(ctx, ` WHERE s.status IN ('pending','approved') AND NOT s.official ORDER BY s.id`)
	if err != nil {
		return err
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, v := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func(v Server) {
			defer wg.Done()
			defer func() { <-sem }()
			udp, wss := s.probeBoth(ctx, v.GameAddr, v.WSSURL)
			if err := s.store(ctx, v.ID, "", "", udp, wss, false); err != nil {
				log.Printf("servers: storing the probe of %s: %v", v.ServerID, err)
			}
		}(v)
	}
	wg.Wait()
	return nil
}

// RunProber calls ProbeAll every interval until ctx ends.
func (s *Service) RunProber(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.ProbeAll(ctx); err != nil {
				log.Printf("servers: probing: %v", err)
			}
		}
	}
}
