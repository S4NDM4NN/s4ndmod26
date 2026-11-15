package accounts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Signing in a program (the launcher) through the website, the way a TV app does: the program asks for a login and
// shows a short code, the person opens the site's approval page (already signed in there, with their own password,
// email checks and everything else the site does), compares the code with the program's and approves, and the program
// is handed a session.  The program never sees the password.  The session is short, only the link routes accept it
// (the program signs in just to link its key; see LinkScope), and the program ends it when it is done.
const (
	LinkScope    = "link"
	deviceTTL    = 10 * time.Minute // how long a pending login may wait for approval
	deviceSessTL = time.Hour        // the session handed to the program
	codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

var (
	ErrDeviceGone = errors.New("that sign-in has expired or does not exist: start again in the launcher")
	ErrDenied     = errors.New("the sign-in was refused")
)

// DeviceLogin is what the approval page shows.
type DeviceLogin struct {
	Label    string `json:"label"`
	IP       string `json:"ip"`
	Created  int64  `json:"created"`
	Approved bool   `json:"approved"`
}

func cleanLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= ' ' && r != 0x7f && r < 0xFFFD {
			b.WriteRune(r)
		}
		if b.Len() >= 60 {
			break
		}
	}
	if out := strings.TrimSpace(b.String()); out != "" {
		return out
	}
	return "a program"
}

func newUserCode() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	out := make([]byte, 9)
	for i := 0; i < 8; i++ {
		c := codeAlphabet[int(raw[i])%len(codeAlphabet)]
		j := i
		if i >= 4 {
			j++
		}
		out[j] = c
	}
	out[4] = '-'
	return string(out), nil
}

// normalizeUserCode accepts what a person types: any case, with or without the dash or spaces.
func normalizeUserCode(c string) string {
	c = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(c))
	if len(c) != 8 {
		return ""
	}
	for _, r := range c {
		if !strings.ContainsRune(codeAlphabet, r) {
			return ""
		}
	}
	return c[:4] + "-" + c[4:]
}

// StartDevice makes a pending login.  deviceCode is the program's secret; userCode is what the person compares.
func (s *Service) StartDevice(ctx context.Context, label, ip string) (deviceCode, userCode string, err error) {
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return
	}
	deviceCode = base64.RawURLEncoding.EncodeToString(raw[:])
	now := s.now()
	s.pool.Exec(ctx, `DELETE FROM device_logins WHERE expires_at < $1`, now)
	for try := 0; try < 5; try++ {
		if userCode, err = newUserCode(); err != nil {
			return
		}
		_, err = s.pool.Exec(ctx, `INSERT INTO device_logins (device_hash, user_code, label, ip, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_code) DO NOTHING`, hashToken(deviceCode), userCode, cleanLabel(label), ip, now, now.Add(deviceTTL))
		if err != nil {
			return
		}
		var n int
		s.pool.QueryRow(ctx, `SELECT count(*) FROM device_logins WHERE device_hash = $1`, hashToken(deviceCode)).Scan(&n)
		if n == 1 {
			return
		}
	}
	return "", "", errors.New("could not make a code, try again")
}

// DeviceByCode is the pending login with this code (as typed), for the approval page.
func (s *Service) DeviceByCode(ctx context.Context, code string) (DeviceLogin, error) {
	code = normalizeUserCode(code)
	var d DeviceLogin
	var created time.Time
	var acct *int64
	var denied bool
	err := s.pool.QueryRow(ctx, `SELECT label, ip, created_at, account_id, denied FROM device_logins WHERE user_code = $1 AND expires_at > $2`, code, s.now()).
		Scan(&d.Label, &d.IP, &created, &acct, &denied)
	if err != nil || code == "" || denied {
		return DeviceLogin{}, ErrDeviceGone
	}
	d.Created, d.Approved = created.Unix(), acct != nil
	return d, nil
}

// DecideDevice approves (for this account) or refuses a pending login.
func (s *Service) DecideDevice(ctx context.Context, code string, id uint64, approve bool) error {
	code = normalizeUserCode(code)
	var tag int64
	var err error
	if approve {
		t, e := s.pool.Exec(ctx, `UPDATE device_logins SET account_id = $3 WHERE user_code = $1 AND expires_at > $2 AND NOT denied AND account_id IS NULL`, code, s.now(), int64(id))
		tag, err = t.RowsAffected(), e
	} else {
		t, e := s.pool.Exec(ctx, `UPDATE device_logins SET denied = true WHERE user_code = $1 AND expires_at > $2 AND account_id IS NULL`, code, s.now())
		tag, err = t.RowsAffected(), e
	}
	if err != nil {
		return err
	}
	if code == "" || tag == 0 {
		return ErrDeviceGone
	}
	return nil
}

// PollDevice is the program asking whether it was approved.  Approved: the login is spent and a link-scoped session
// is returned.  Not yet: ok is false and err nil.
func (s *Service) PollDevice(ctx context.Context, deviceCode string) (tok string, a Account, ok bool, err error) {
	if len(deviceCode) < 20 || len(deviceCode) > 100 {
		return "", Account{}, false, ErrDeviceGone
	}
	h := hashToken(deviceCode)
	s.pool.Exec(ctx, `DELETE FROM device_logins WHERE expires_at < $1`, s.now()) // the requester's address does not outlive the sign-in
	var id int64
	err = s.pool.QueryRow(ctx, `DELETE FROM device_logins WHERE device_hash = $1 AND expires_at > $2 AND account_id IS NOT NULL RETURNING account_id`, h, s.now()).Scan(&id)
	if err == nil {
		if a, err = s.ByID(ctx, uint64(id)); err != nil {
			return "", Account{}, false, err
		}
		if a.Suspended {
			return "", Account{}, false, ErrSuspended
		}
		tok, err = s.newScopedSession(ctx, a.ID, LinkScope, deviceSessTL)
		return tok, a, err == nil, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", Account{}, false, err
	}
	var denied bool
	err = s.pool.QueryRow(ctx, `SELECT denied FROM device_logins WHERE device_hash = $1 AND expires_at > $2`, h, s.now()).Scan(&denied)
	if err != nil {
		return "", Account{}, false, ErrDeviceGone
	}
	if denied {
		s.pool.Exec(ctx, `DELETE FROM device_logins WHERE device_hash = $1`, h)
		return "", Account{}, false, ErrDenied
	}
	return "", Account{}, false, nil
}

func (h *Handler) mountDevice(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/device/start", h.deviceStart)
	mux.HandleFunc("POST /api/device/poll", h.devicePoll)
	mux.HandleFunc("GET /api/device", h.deviceShow)
	mux.HandleFunc("POST /api/device/decide", h.deviceDecide)
}

func (h *Handler) deviceStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct{ Label string }
	if !h.body(w, r, &req) {
		return
	}
	if !h.S.rl.allow("devstart:"+clientIP(r), 10, time.Hour, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, ErrThrottled.Error())
		return
	}
	dev, user, err := h.S.StartDevice(r.Context(), req.Label, clientIP(r))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base := strings.TrimRight(h.S.PublicURL, "/")
	writeJSON(w, map[string]any{"device_code": dev, "user_code": user, "verify_url": base + "/account/device/?code=" + url.QueryEscape(user),
		"expires_in": int(deviceTTL / time.Second), "interval": 2})
}

func (h *Handler) devicePoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	if !h.body(w, r, &req) {
		return
	}
	if !h.S.rl.allow("devpoll:"+req.DeviceCode, 40, time.Minute, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, "slow down")
		return
	}
	tok, a, ok, err := h.S.PollDevice(r.Context(), req.DeviceCode)
	switch {
	case errors.Is(err, ErrDeviceGone):
		jsonError(w, http.StatusGone, err.Error())
	case errors.Is(err, ErrDenied):
		jsonError(w, http.StatusForbidden, err.Error())
	case err != nil:
		jsonError(w, statusOf(err), err.Error())
	case !ok:
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]any{"status": "pending"})
	default:
		writeJSON(w, map[string]any{"status": "approved", "token": tok, "username": a.Username, "avatar": a.Avatar})
	}
}

func (h *Handler) deviceShow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	if !h.S.rl.allow("devlook:"+a.Username, 30, 10*time.Minute, h.S.now()) {
		jsonError(w, http.StatusTooManyRequests, ErrThrottled.Error())
		return
	}
	d, err := h.S.DeviceByCode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, d)
}

func (h *Handler) deviceDecide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a, _, ok := h.S.fromRequest(r)
	if !ok {
		h.login401(w)
		return
	}
	var req struct {
		Code    string `json:"code"`
		Approve bool   `json:"approve"`
	}
	if !h.body(w, r, &req) {
		return
	}
	if err := h.S.DecideDevice(r.Context(), req.Code, a.ID, req.Approve); err != nil {
		if errors.Is(err, ErrDeviceGone) {
			jsonError(w, http.StatusNotFound, err.Error())
		} else {
			jsonError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
