package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"s4ndmod26/status-api/ticket"
)

const CookieName = "s4nd_session"

// Handler serves the account API.
//
//	POST   /api/account/register   {username, email, password}
//	POST   /api/account/login      {login, password}             sets the session cookie
//	POST   /api/account/logout
//	GET    /api/account            the logged-in account
//	POST   /api/account/password   {current, new}
//	DELETE /api/account            {password}
//	POST   /api/account/verify          {token}           confirm the email (the link in the mail)
//	POST   /api/account/verify/resend                     mail the link again
//	POST   /api/account/reset           {email}           mail a password reset link (always 204)
//	POST   /api/account/reset/confirm   {token, new}
//	POST   /api/account/demo-key        {key}             redeem an old demo-upload key for demo_upload
//	GET    /api/admin/accounts?q=                         (admin) find accounts
//	PUT    /api/admin/accounts/{id}/permissions/{perm}    (admin) grant
//	DELETE /api/admin/accounts/{id}/permissions/{perm}    (admin) revoke
//
// The session is the cookie, or "Authorization: Bearer <token>" for scripts.  Bodies must be JSON (Content-Type
// application/json): a page on another site cannot send that without our permission, which together with SameSite=Lax
// keeps other sites from acting for a logged-in visitor.
type Handler struct {
	S *Service

	// The demo tool lives elsewhere; main wires these in when it is on.  DemoKeyLabel says which uploader key a
	// secret is (the label), AdoptDemos hands what that label uploaded to the account's owner name.
	DemoKeyLabel func(secret string) (label string, ok bool)
	AdoptDemos   func(ctx context.Context, label, owner string) error
}

// OwnerName is how the demo tool names an account as the owner of uploads.
func OwnerName(id uint64) string { return "acct:" + strconv.FormatUint(id, 10) }

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/account/register", h.register)
	mux.HandleFunc("POST /api/account/login", h.login)
	mux.HandleFunc("POST /api/account/logout", h.logout)
	mux.HandleFunc("GET /api/account", h.me)
	mux.HandleFunc("POST /api/account/password", h.password)
	mux.HandleFunc("DELETE /api/account", h.remove)
	mux.HandleFunc("POST /api/account/verify", h.verify)
	mux.HandleFunc("POST /api/account/verify/resend", h.resend)
	mux.HandleFunc("POST /api/account/reset", h.reset)
	mux.HandleFunc("POST /api/account/reset/confirm", h.resetConfirm)
	mux.HandleFunc("POST /api/account/demo-key", h.demoKey)
	mux.HandleFunc("GET /api/admin/accounts", h.adminList)
	mux.HandleFunc("PUT /api/admin/accounts/{id}/permissions/{perm}", h.adminGrant)
	mux.HandleFunc("DELETE /api/admin/accounts/{id}/permissions/{perm}", h.adminRevoke)
}

// Auth lets the ticket and link packages ask who a request is from.
type Auth struct{ S *Service }

func (a Auth) Authenticate(r *http.Request) (ticket.Account, bool) {
	acct, _, ok := a.S.fromRequest(r)
	if !ok {
		return ticket.Account{}, false
	}
	return ticket.Account{ID: acct.ID, Name: acct.Username}, true
}

func token(r *http.Request) string {
	if t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return t
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

func (s *Service) fromRequest(r *http.Request) (Account, string, bool) {
	t := token(r)
	if t == "" {
		return Account{}, "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	a, ok := s.BySession(ctx, t)
	return a, t, ok
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func view(a Account) map[string]any {
	perms := a.Permissions
	if perms == nil {
		perms = []string{}
	}
	return map[string]any{"id": a.ID, "username": a.Username, "email": a.Email, "email_verified": a.Verified, "permissions": perms, "created": a.Created.Unix()}
}

func (h *Handler) body(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		jsonError(w, http.StatusUnsupportedMediaType, "send JSON (Content-Type: application/json)")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(v); err != nil {
		jsonError(w, http.StatusBadRequest, "could not read the request")
		return false
	}
	return true
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct{ Username, Email, Password string }
	if !h.body(w, r, &req) {
		return
	}
	if !h.S.rl.allow("reg:"+clientIP(r), 5, time.Hour, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, ErrThrottled.Error())
		return
	}
	a, err := h.S.Register(r.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	log.Printf("accounts: registered %q (id %d)", a.Username, a.ID)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, view(a))
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct{ Login, Password string }
	if !h.body(w, r, &req) {
		return
	}
	tok, a, err := h.S.Login(r.Context(), req.Login, req.Password, clientIP(r))
	if err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: tok, Path: "/", MaxAge: int(SessionTTL / time.Second), HttpOnly: true,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https", SameSite: http.SameSiteLaxMode})
	out := view(a)
	out["token"] = tok // for scripts; the page uses the cookie
	writeJSON(w, out)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if t := token(r); t != "" {
		h.S.Logout(r.Context(), t)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) login401(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	jsonError(w, http.StatusUnauthorized, "log in first")
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	out := view(a)
	out["demo_keys"] = h.S.DemoKeyLabels(r.Context(), a.ID)
	writeJSON(w, out)
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct{ Token string }
	if !h.body(w, r, &req) {
		return
	}
	if !h.S.rl.allow("vfy:"+clientIP(r), 20, time.Hour, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, ErrThrottled.Error())
		return
	}
	if err := h.S.Verify(r.Context(), req.Token); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	if err := h.S.ResendVerification(r.Context(), a.ID); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct{ Email string }
	if !h.body(w, r, &req) {
		return
	}
	if err := h.S.RequestReset(r.Context(), req.Email, clientIP(r)); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent) // the same whether or not the address has an account
}

func (h *Handler) resetConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct{ Token, New string }
	if !h.body(w, r, &req) {
		return
	}
	if !h.S.rl.allow("rstc:"+clientIP(r), 20, time.Hour, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, ErrThrottled.Error())
		return
	}
	if err := h.S.ConfirmReset(r.Context(), req.Token, req.New); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) demoKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	if h.DemoKeyLabel == nil {
		jsonError(w, http.StatusNotFound, "demo uploads are not available on this server")
		return
	}
	var req struct{ Key string }
	if !h.body(w, r, &req) {
		return
	}
	if !h.S.rl.allow("dk:"+strconv.FormatUint(a.ID, 10), 5, 10*time.Minute, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, ErrThrottled.Error())
		return
	}
	label, ok := h.DemoKeyLabel(strings.TrimSpace(req.Key))
	if !ok {
		jsonError(w, http.StatusBadRequest, "that is not a demo key")
		return
	}
	if err := h.S.RedeemDemoKey(r.Context(), a.ID, label); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	if h.AdoptDemos != nil {
		if err := h.AdoptDemos(r.Context(), label, OwnerName(a.ID)); err != nil {
			log.Printf("accounts: adopting the demos of %q: %v", label, err)
			jsonError(w, http.StatusInternalServerError, "the key was accepted but moving your old uploads failed; try again")
			return
		}
	}
	log.Printf("accounts: %q redeemed demo key %q", a.Username, label)
	writeJSON(w, map[string]any{"label": label, "permission": PermDemoUpload, "email_verified": a.Verified})
}

func (h *Handler) password(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, tok, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	var req struct{ Current, New string }
	if !h.body(w, r, &req) {
		return
	}
	if err := h.S.ChangePassword(r.Context(), a.ID, req.Current, req.New, tok); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	var req struct{ Password string }
	if !h.body(w, r, &req) {
		return
	}
	if err := h.S.Delete(r.Context(), a.ID, req.Password); err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	log.Printf("accounts: %q (id %d) deleted their account", a.Username, a.ID)
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

// admin returns the caller when they hold the admin permission with a confirmed email.
func (h *Handler) admin(w http.ResponseWriter, r *http.Request) (Account, bool) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return a, false
	}
	if !a.Has(PermAdmin) {
		jsonError(w, http.StatusForbidden, "admins only")
		return a, false
	}
	return a, true
}

func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	list, err := h.S.List(r.Context(), r.URL.Query().Get("q"), 50)
	if err != nil {
		log.Printf("accounts: list: %v", err)
		jsonError(w, http.StatusInternalServerError, "could not list accounts")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		out = append(out, view(a))
	}
	writeJSON(w, map[string]any{"accounts": out})
}

func (h *Handler) adminPerm(w http.ResponseWriter, r *http.Request, grant bool) {
	admin, ok := h.admin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad account id")
		return
	}
	perm := r.PathValue("perm")
	if grant {
		err = h.S.Grant(r.Context(), id, perm, admin.ID)
	} else {
		if id == admin.ID && perm == PermAdmin {
			jsonError(w, http.StatusBadRequest, "ask another admin to remove your admin permission")
			return
		}
		err = h.S.Revoke(r.Context(), id, perm)
	}
	if err != nil {
		jsonError(w, statusOf(err), err.Error())
		return
	}
	log.Printf("accounts: admin %d: grant=%v %s on %d", admin.ID, grant, perm, id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminGrant(w http.ResponseWriter, r *http.Request)  { h.adminPerm(w, r, true) }
func (h *Handler) adminRevoke(w http.ResponseWriter, r *http.Request) { h.adminPerm(w, r, false) }

func statusOf(err error) int {
	switch {
	case errors.Is(err, ErrUsername), errors.Is(err, ErrEmail), errors.Is(err, ErrPassword), errors.Is(err, ErrBadPerm):
		return http.StatusBadRequest
	case errors.Is(err, ErrToken):
		return http.StatusBadRequest
	case errors.Is(err, ErrTaken), errors.Is(err, ErrKeyUsed):
		return http.StatusConflict
	case errors.Is(err, ErrLogin):
		return http.StatusUnauthorized
	case errors.Is(err, ErrThrottled):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrNoAccount):
		return http.StatusNotFound
	}
	log.Printf("accounts: %v", err)
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

// FromRequest is the logged-in account of a request (cookie or Bearer token).
func (s *Service) FromRequest(r *http.Request) (Account, bool) {
	a, _, ok := s.fromRequest(r)
	return a, ok
}
