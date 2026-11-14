package servers

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"s4ndmod26/status-api/accounts"
)

// Handler serves the server registry API.
//
//	GET    /api/servers                          the servers people can join now (public, paged)
//	POST   /api/servers/tokens      {name}       (host_server) a one-time registration token
//	GET    /api/servers/mine                     (host_server) your servers and open tokens
//	DELETE /api/servers/mine/{id}                (host_server) remove one of yours
//	POST   /api/servers/claim                    from a server's agent, signed: register or refresh
//	GET    /api/admin/servers?status=&page=&per= (admin)
//	POST   /api/admin/servers                    (admin) add an official server {public_key, name, game_addr, wss_url}
//	POST   /api/admin/servers/{id}/approve|block|unblock
//	POST   /api/admin/servers/{id}/official      {on}
//	POST   /api/admin/servers/{id}/stats         {on}: pull this server's game stats, or stop
//	POST   /api/admin/servers/{id}/purge-stats   delete every game pulled from this server (works after the server is deleted too, by server id)
//	DELETE /api/admin/servers/{id}[?purge_stats=1]
type Handler struct {
	S   *Service
	Acc *accounts.Service
	// Stats is where a server's pulled game stats live (nil: none are kept), so an admin can delete them.
	Stats StatsStore
}

var serverIDRe = regexp.MustCompile(`^[a-z2-7]{16}$`)

// StatsStore is the part of the stats package the registry's admin actions use.
type StatsStore interface {
	PurgeServer(ctx context.Context, serverID string) (int64, error)
	CountGames(ctx context.Context, serverID string) (int64, error)
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers", h.listed)
	mux.HandleFunc("GET /api/servers/{server_id}", h.listedOne)
	mux.HandleFunc("POST /api/servers/tokens", h.newToken)
	mux.HandleFunc("GET /api/servers/mine", h.mine)
	mux.HandleFunc("DELETE /api/servers/mine/{id}", h.deleteMine)
	mux.HandleFunc("POST /api/servers/claim", h.claim)
	mux.HandleFunc("GET /api/admin/servers", h.adminList)
	mux.HandleFunc("POST /api/admin/servers", h.adminAdd)
	mux.HandleFunc("POST /api/admin/servers/{id}/approve", h.adminApprove)
	mux.HandleFunc("POST /api/admin/servers/{id}/block", func(w http.ResponseWriter, r *http.Request) { h.adminStatus(w, r, StatusBlocked, "block") })
	mux.HandleFunc("POST /api/admin/servers/{id}/unblock", func(w http.ResponseWriter, r *http.Request) { h.adminStatus(w, r, StatusPending, "unblock") })
	mux.HandleFunc("POST /api/admin/servers/{id}/official", h.adminOfficial)
	mux.HandleFunc("POST /api/admin/servers/{id}/stats", h.adminStats)
	mux.HandleFunc("POST /api/admin/servers/{id}/purge-stats", h.adminPurgeStats)
	mux.HandleFunc("DELETE /api/admin/servers/{id}", h.adminDelete)
	mux.HandleFunc("GET /api/admin/accounts/{id}/server-limit", h.adminLimit)
	mux.HandleFunc("PUT /api/admin/accounts/{id}/server-limit", h.adminSetLimit)
}

// publicView is what anyone may see of a listed server: no owner, no probe details.
// CleanHostname is a server's own name (sv_hostname, from the last status poll) made fit to show: colour codes
// (^1, ^7 ...) and control characters removed, spaces collapsed, at most 60 characters.  It is what the server
// list shows, so a host names the server in its server.cfg, not on the site.
func CleanHostname(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '^' && i+1 < len(rs) && rs[i+1] != '^' {
			i++
			continue
		}
		if unicode.IsControl(rs[i]) {
			rs[i] = ' '
		}
		b.WriteRune(rs[i])
	}
	out := []rune(strings.Join(strings.Fields(b.String()), " "))
	if len(out) > 60 {
		out = out[:60]
	}
	return strings.TrimSpace(string(out))
}

func publicView(v Server) map[string]any {
	return map[string]any{"server_id": v.ServerID, "name": v.Name, "hostname": CleanHostname(v.Hostname), "game_addr": v.GameAddr, "wss_url": v.WSSURL,
		"map": v.Map, "players": v.Players, "max_players": v.MaxPlayers, "official": v.Official, "pk3": v.Pk3}
}

func (h *Handler) listed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	page, per := pageParams(r, 25, 100)
	list, total, err := h.S.Listed(r.Context(), r.URL.Query().Get("kind"), per, (page-1)*per)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		out = append(out, publicView(v))
	}
	writeJSON(w, map[string]any{"servers": out, "total": total, "page": page, "per": per})
}

// listedOne is how the play page finds the address of the server a Join button names: only a listed server answers.
func (h *Handler) listedOne(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	v, err := h.S.ListedOne(r.Context(), r.PathValue("server_id"))
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusNotFound, "that server is not listed right now")
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, publicView(v))
}

func pageParams(r *http.Request, def, max int) (page, per int) {
	page, per = 1, def
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 && n < 1_000_000 {
		page = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("per")); err == nil && n > 0 && n <= max {
		per = n
	}
	return page, per
}

// ---- hosts ----

func (h *Handler) host(w http.ResponseWriter, r *http.Request) (accounts.Account, bool) {
	w.Header().Set("Cache-Control", "no-store")
	a, ok := h.Acc.FromRequest(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		jsonError(w, http.StatusUnauthorized, "log in first")
		return a, false
	}
	if !a.Has(accounts.PermHostServer) {
		jsonError(w, http.StatusForbidden, "your account may not host servers yet: ask an admin")
		return a, false
	}
	return a, true
}

func body(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonError(w, http.StatusUnsupportedMediaType, "send JSON (Content-Type: application/json)")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(v); err != nil {
		jsonError(w, http.StatusBadRequest, "could not read the request")
		return false
	}
	return true
}

func (h *Handler) newToken(w http.ResponseWriter, r *http.Request) {
	a, ok := h.host(w, r)
	if !ok {
		return
	}
	var req struct{ Name string }
	if !body(w, r, &req) {
		return
	}
	tok, exp, err := h.S.NewToken(r.Context(), a.ID, req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	log.Printf("servers: %q made a registration token for %q", a.Username, req.Name)
	writeJSON(w, map[string]any{"token": tok, "expires": exp.Unix(), "name": strings.TrimSpace(req.Name),
		"env": "AGENT_REGISTRATION_TOKEN=" + tok, "note": "Shown once. It works for one registration."})
}

func (h *Handler) mine(w http.ResponseWriter, r *http.Request) {
	a, ok := h.host(w, r)
	if !ok {
		return
	}
	list, err := h.S.Mine(r.Context(), a.ID)
	if err != nil {
		fail(w, err)
		return
	}
	toks, err := h.S.TokensOf(r.Context(), a.ID)
	if err != nil {
		fail(w, err)
		return
	}
	max, err := h.S.LimitOf(r.Context(), a.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"servers": list, "tokens": toks, "max": max})
}

func (h *Handler) deleteMine(w http.ResponseWriter, r *http.Request) {
	a, ok := h.host(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad server id")
		return
	}
	if _, err := h.S.Delete(r.Context(), id, a.ID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- the agent's claim ----

func checkJSON(c Check) map[string]any {
	m := map[string]any{"ok": c.OK}
	if c.Error != "" {
		m["error"] = c.Error
	}
	return m
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	var cb ClaimBody
	if err != nil || json.Unmarshal(raw, &cb) != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !h.claimLimiter(r) {
		jsonError(w, http.StatusTooManyRequests, "too many claims, wait a minute")
		return
	}
	pub, err := h.S.VerifyClaim(raw, r.Header.Get("X-S4nd-Signature"), cb)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, err.Error())
		return
	}
	sv, err := h.S.Claim(r.Context(), cb, ed25519.PublicKey(pub))
	var pf *ProbeFailure
	if errors.As(err, &pf) {
		// the token is kept: fix the problem, and the agent tries again
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{"error": "the server is not reachable from the internet yet", "udp": checkJSON(pf.UDP), "wss": checkJSON(pf.WSS)})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	log.Printf("servers: claim from %s (%s): %s", sv.ServerID, sv.Name, sv.Status)
	writeJSON(w, map[string]any{"status": sv.Status, "name": sv.Name, "server_id": sv.ServerID, "udp": checkJSON(sv.UDP), "wss": checkJSON(sv.WSS)})
}

// claimLimiter keeps one address from hammering the probe: each claim makes the site connect out.
func (h *Handler) claimLimiter(r *http.Request) bool {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.RemoteAddr
	}
	return claimRL.allow(ip, 10, time.Minute, time.Now())
}

// ---- admin ----

func (h *Handler) admin(w http.ResponseWriter, r *http.Request) (accounts.Account, bool) {
	w.Header().Set("Cache-Control", "no-store")
	a, ok := h.Acc.FromRequest(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		jsonError(w, http.StatusUnauthorized, "log in first")
		return a, false
	}
	if !a.Has(accounts.PermAdmin) {
		jsonError(w, http.StatusForbidden, "admins only")
		return a, false
	}
	return a, true
}

func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	st := r.URL.Query().Get("status")
	if st != "" && st != StatusPending && st != StatusApproved && st != StatusBlocked {
		jsonError(w, http.StatusBadRequest, "status is pending, approved or blocked")
		return
	}
	page, per := pageParams(r, 25, 100)
	list, total, err := h.S.List(r.Context(), st, per, (page-1)*per)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"servers": list, "total": total, "page": page, "per": per})
}

func (h *Handler) target(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad server id")
		return 0, false
	}
	return id, true
}

func (h *Handler) audit(r *http.Request, admin accounts.Account, action string, v Server, detail string) {
	h.Acc.AuditRaw(r.Context(), admin, action, uint64(v.ID), "server "+v.Name, detail)
}

func (h *Handler) adminApprove(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	v, err := h.S.Approve(r.Context(), id, admin.ID)
	var pf *ProbeFailure
	if errors.As(err, &pf) {
		jsonError(w, http.StatusConflict, "not approved: "+strings.TrimSpace(pf.UDP.Error+" "+pf.WSS.Error))
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	h.audit(r, admin, "server_approve", v, "")
	writeJSON(w, v)
}

func (h *Handler) adminStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	v, err := h.S.SetStatus(r.Context(), id, status, admin.ID)
	if err != nil {
		fail(w, err)
		return
	}
	h.audit(r, admin, "server_"+action, v, "")
	writeJSON(w, v)
}

func (h *Handler) adminOfficial(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	var req struct{ On bool }
	if !body(w, r, &req) {
		return
	}
	v, err := h.S.SetOfficial(r.Context(), id, req.On)
	if err != nil {
		fail(w, err)
		return
	}
	h.audit(r, admin, "server_official", v, strconv.FormatBool(req.On))
	writeJSON(w, v)
}

func (h *Handler) adminDelete(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	v, err := h.S.Delete(r.Context(), id, 0)
	if err != nil {
		fail(w, err)
		return
	}
	detail := v.GameAddr
	if r.URL.Query().Get("purge_stats") == "1" && h.Stats != nil {
		n, err := h.Stats.PurgeServer(r.Context(), v.ServerID)
		if err != nil {
			fail(w, err)
			return
		}
		detail += " (purged " + strconv.FormatInt(n, 10) + " games)"
	}
	h.audit(r, admin, "server_delete", v, detail)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminStats(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	var req struct{ On bool }
	if !body(w, r, &req) {
		return
	}
	v, err := h.S.SetStats(r.Context(), id, req.On)
	if err != nil {
		fail(w, err)
		return
	}
	h.audit(r, admin, "server_stats", v, strconv.FormatBool(req.On))
	writeJSON(w, v)
}

// adminPurgeStats deletes the games pulled from a server.  {id} is the registry row id, or the server id itself (for a server that
// was deleted, whose row is gone).
func (h *Handler) adminPurgeStats(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	if h.Stats == nil {
		jsonError(w, http.StatusServiceUnavailable, "stats are not kept")
		return
	}
	sid := strings.ToLower(r.PathValue("id"))
	if id, err := strconv.ParseInt(sid, 10, 64); err == nil {
		v, err := h.S.ByID(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		sid = v.ServerID
	}
	if !serverIDRe.MatchString(sid) {
		jsonError(w, http.StatusBadRequest, "bad server id")
		return
	}
	n, err := h.Stats.PurgeServer(r.Context(), sid)
	if err != nil {
		fail(w, err)
		return
	}
	h.audit(r, admin, "server_purge_stats", Server{ServerID: sid}, strconv.FormatInt(n, 10)+" games")
	writeJSON(w, map[string]any{"server_id": sid, "games_deleted": n})
}

func (h *Handler) adminAdd(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	var req struct {
		PublicKey string `json:"public_key"`
		Name      string `json:"name"`
		GameAddr  string `json:"game_addr"`
		WSSURL    string `json:"wss_url"`
	}
	if !body(w, r, &req) {
		return
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.PublicKey))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		jsonError(w, http.StatusBadRequest, "public_key must be the server's Ed25519 public key (base64, from its /v1/info)")
		return
	}
	v, err := h.S.AddOfficial(r.Context(), pub, req.Name, req.GameAddr, req.WSSURL, admin.ID)
	if err != nil {
		fail(w, err)
		return
	}
	h.audit(r, admin, "server_add_official", v, v.GameAddr)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, v)
}

// ---- plumbing ----

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrBadRequest):
		code = http.StatusBadRequest
	case errors.Is(err, ErrNoToken), errors.Is(err, ErrBadToken), errors.Is(err, ErrBlocked), errors.Is(err, ErrNotOwner), errors.Is(err, ErrKeyChanged):
		code = http.StatusForbidden
	case errors.Is(err, ErrAddrTaken):
		code = http.StatusConflict
	case errors.Is(err, ErrTooMany):
		code = http.StatusTooManyRequests
	case errors.Is(err, ErrNotFound):
		code = http.StatusNotFound
	}
	if code == http.StatusInternalServerError {
		log.Printf("servers: %v", err)
		jsonError(w, code, "something went wrong")
		return
	}
	jsonError(w, code, err.Error())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// a small sliding-window limiter for the claim endpoint
var claimRL limiter

var limiterMu sync.Mutex

type limiter struct {
	hits map[string][]time.Time
}

func (l *limiter) allow(key string, max int, window time.Duration, now time.Time) bool {
	limiterMu.Lock()
	defer limiterMu.Unlock()
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

// adminLimit says how many servers the account may have, how many it uses, and whether the number is its own.
func (h *Handler) adminLimit(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	n, err := h.S.LimitOf(r.Context(), uint64(id))
	if err != nil {
		fail(w, err)
		return
	}
	var own bool
	if err := h.S.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM server_limits WHERE account_id = $1)`, id).Scan(&own); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"limit": n, "default": h.S.MaxPerAccount, "own": own})
}

// adminSetLimit sets the account's own limit ({"limit": 8}), or {"limit": null} to use the default again.
func (h *Handler) adminSetLimit(w http.ResponseWriter, r *http.Request) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, ok := h.target(w, r)
	if !ok {
		return
	}
	var req struct {
		Limit *int `json:"limit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "send {\"limit\": number} or {\"limit\": null}")
		return
	}
	n, detail := -1, "default"
	if req.Limit != nil {
		n, detail = *req.Limit, strconv.Itoa(*req.Limit)
		if n < 0 {
			jsonError(w, http.StatusBadRequest, "the limit cannot be negative")
			return
		}
	}
	if err := h.S.SetLimit(r.Context(), uint64(id), n); err != nil {
		fail(w, err)
		return
	}
	h.Acc.AuditRaw(r.Context(), admin, "server_limit", uint64(id), "account "+strconv.FormatInt(id, 10), detail)
	h.adminLimit(w, r)
}
