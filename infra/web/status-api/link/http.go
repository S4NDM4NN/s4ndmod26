package link

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"s4ndmod26/status-api/ticket"
)

// ClaimMaxAge is how old a signed request from a server may be.
const ClaimMaxAge = 2 * time.Minute

// Handler serves the link API:
//
//	POST   /api/link/codes          (account)  make a code to type in game
//	POST   /api/link/auto-code      (account)  {"server_id"}: a code for joining that server while signed in (see AutoCode)
//	GET    /api/link                (account)  confirmed and pending keys
//	POST   /api/link/confirm        (account)  {"key": "..."}
//	DELETE /api/link/{key}          (account)  reject a pending key or remove a link
//	POST   /api/link/claim          (server)   signed by a server agent, see Claim
type Handler struct {
	Store   Backend
	Auth    ticket.Authenticator
	Servers ticket.Directory // only servers it lists may claim
	Now     func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Mount registers the routes on mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/link/codes", h.codes)
	mux.HandleFunc("POST /api/link/auto-code", h.autoCode)
	mux.HandleFunc("GET /api/link", h.list)
	mux.HandleFunc("POST /api/link/confirm", h.confirm)
	mux.HandleFunc("DELETE /api/link/{key}", h.remove)
	mux.HandleFunc("POST /api/link/claim", h.claim)
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	w.Header().Set("Cache-Control", "no-store")
	a, ok := h.Auth.Authenticate(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		jsonError(w, http.StatusUnauthorized, "log in first")
		return 0, false
	}
	return a.ID, true
}

func (h *Handler) codes(w http.ResponseWriter, r *http.Request) {
	id, ok := h.account(w, r)
	if !ok {
		return
	}
	c, exp, err := h.Store.NewCode(id)
	if err != nil {
		jsonError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	writeJSON(w, map[string]any{"code": c, "expires": exp.Unix(), "say": "/link " + c})
}

// autoCode is how a signed-in page links the player it is about to start: it asks for a code for the server being joined and hands it to
// the game (a launch argument).  The game's cgame signs it with the player's key once the server has identified the player and sends the
// request through the server's agent (claim with Auto); the key is then linked with no further step, because this signed-in request is the
// account holder's say-so.  Only a server the registry lists can be named, and the code works only from that server.
func (h *Handler) autoCode(w http.ResponseWriter, r *http.Request) {
	id, ok := h.account(w, r)
	if !ok {
		return
	}
	var req struct {
		Server string `json:"server_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || req.Server == "" {
		jsonError(w, http.StatusBadRequest, `send {"server_id": "<the server being joined>"}`)
		return
	}
	sid, listed := h.Servers.Lookup(req.Server)
	if !listed {
		jsonError(w, http.StatusNotFound, "that server is not listed")
		return
	}
	c, exp, err := h.Store.NewAutoCode(id, strings.ToLower(sid))
	if err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, map[string]any{"code": c, "expires": exp.Unix(), "server_id": strings.ToLower(sid)})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	id, ok := h.account(w, r)
	if !ok {
		return
	}
	links, pending := h.Store.LinksFor(id), h.Store.PendingFor(id)
	if links == nil {
		links = []Link{}
	}
	if pending == nil {
		pending = []Pending{}
	}
	writeJSON(w, map[string]any{"links": links, "pending": pending})
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	id, ok := h.account(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || !ValidKey(req.Key) {
		jsonError(w, http.StatusBadRequest, `send {"key": "<the key to confirm>"}`)
		return
	}
	if err := h.Store.Confirm(id, req.Key); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	log.Printf("link: account %d confirmed key %s", id, Fingerprint(req.Key))
	writeJSON(w, map[string]string{"linked": req.Key})
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := h.account(w, r)
	if !ok {
		return
	}
	if err := h.Store.Reject(id, r.PathValue("key")); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ClaimBody is what a server agent signs: the exact bytes go in the X-S4nd-Signature header (ed25519, base64).
type ClaimBody struct {
	Server    string `json:"server_id"`
	PublicKey string `json:"public_key"` // the server's own key (base64); its id is derived from it
	Key       string `json:"key"`        // the player's key
	Code      string `json:"code"`
	Sent      int64  `json:"sent"` // Unix seconds
	// Auto is a request from a player who joined while signed in on the site (autoCode): Sig is the player's own signature (URL-safe
	// base64) over LinkMessage(code, server id), made by the game client with the key, which the server cannot make for it.
	Auto bool   `json:"auto,omitempty"`
	Sig  string `json:"sig,omitempty"`
}

// claim checks that the request is signed by a server the site knows (the id must be the one derived from the
// public key, and listed in the directory), then spends the code.
func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	var cb ClaimBody
	if err != nil || json.Unmarshal(body, &cb) != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	pub, err := base64.StdEncoding.DecodeString(cb.PublicKey)
	sig, err2 := base64.StdEncoding.DecodeString(r.Header.Get("X-S4nd-Signature"))
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, body, sig) {
		jsonError(w, http.StatusUnauthorized, "bad signature")
		return
	}
	sum := sha256.Sum256(pub)
	var b [10]byte
	copy(b[:], sum[:10])
	if ticket.ServerID(b) != strings.ToLower(cb.Server) {
		jsonError(w, http.StatusUnauthorized, "server id does not match its key")
		return
	}
	if _, listed := h.Servers.Lookup(cb.Server); !listed {
		jsonError(w, http.StatusForbidden, "that server is not listed")
		return
	}
	if d := h.now().Sub(time.Unix(cb.Sent, 0)); d > ClaimMaxAge || d < -ClaimMaxAge {
		jsonError(w, http.StatusBadRequest, "request is stale (check the server clock)")
		return
	}
	if cb.Auto {
		if err := h.claimAuto(cb); err != nil {
			jsonError(w, statusOf(err), err.Error())
			return
		}
		log.Printf("link: key %s linked automatically via server %s", Fingerprint(cb.Key), cb.Server)
		writeJSON(w, map[string]string{"status": "linked", "fingerprint": Fingerprint(cb.Key)})
		return
	}
	if err := h.Store.Claim(cb.Code, cb.Key, strings.ToLower(cb.Server)); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	log.Printf("link: key %s asked to link via server %s", Fingerprint(cb.Key), cb.Server)
	writeJSON(w, map[string]string{"status": "pending", "fingerprint": Fingerprint(cb.Key)})
}

// claimAuto checks the player's own signature, then spends the code and links the key.
func (h *Handler) claimAuto(cb ClaimBody) error {
	code := NormalizeCode(cb.Code)
	if code == "" {
		return ErrBadCode
	}
	if !ValidKey(cb.Key) {
		return ErrBadKey
	}
	pub, err := base64.RawURLEncoding.DecodeString(cb.Key)
	sig, err2 := base64.RawURLEncoding.DecodeString(cb.Sig)
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize ||
		!ed25519.Verify(pub, LinkMessage(code, strings.ToLower(cb.Server)), sig) {
		return ErrBadSig
	}
	return h.Store.ClaimAuto(code, cb.Key, strings.ToLower(cb.Server))
}

func statusOf(err error) int {
	switch err {
	case ErrBadCode, ErrBadKey, ErrBadSig:
		return http.StatusBadRequest
	case ErrNotFound:
		return http.StatusNotFound
	case ErrTooMany:
		return http.StatusTooManyRequests
	case ErrKeyLinked:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
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

// FromTickets builds the handler from the ticket setup (same login, same server list), or nil when tickets are off.
// LINK_STATE_FILE names the JSON file the codes and links are kept in; unset, they live in memory only.
func FromTickets(i *ticket.Issuer) *Handler {
	if i == nil {
		return nil
	}
	path := os.Getenv("LINK_STATE_FILE")
	if path == "" {
		log.Printf("link: LINK_STATE_FILE is not set, links are lost when the site restarts")
	}
	return &Handler{Store: &Store{Path: path}, Auth: i.Auth, Servers: i.Servers}
}
