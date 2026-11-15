package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/stats"
)

// Erasing a player on request (admin tool).  Someone writes to the privacy contact, the admin checks who they are, looks the
// player up by key or by username, sees what would change, and erases: the player's games get "Deleted Player" instead of their
// name and key (stats.Store.EraseKey), their keys are unlinked, and optionally their account is deleted.  Other players' games
// and results stay.  The audit log gets the action and a key fingerprint, never a name.
//
//	POST /api/admin/privacy/lookup  {"who": "<key or username>"}
//	POST /api/admin/privacy/erase   {"who": "...", "confirm": "...", "all_keys": bool, "delete_account": bool}
//
// who is a key (43 characters, one player) or a username (the account and every key linked to it).  A key linked to an account
// is erased on its own unless all_keys is set.  confirm must repeat who, so a stray request cannot erase anyone.
type privacyStats interface {
	PreviewErase(ctx context.Context, key string) (stats.EraseResult, error)
	EraseKey(ctx context.Context, key string) (stats.EraseResult, error)
}

type privacyAPI struct {
	admin  func(*http.Request) (accounts.Account, bool)
	stats  privacyStats
	dir    playerDirectory // nil: no accounts, a player is only a key
	unlink func(account uint64, key string) error
	delete func(ctx context.Context, admin accounts.Account, id uint64) error
	audit  func(ctx context.Context, admin accounts.Account, detail string)
}

func (p *privacyAPI) mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/privacy/lookup", p.lookup)
	mux.HandleFunc("POST /api/admin/privacy/erase", p.erase)
}

type privacyWho struct {
	Who           string `json:"who"`
	Confirm       string `json:"confirm"`
	AllKeys       bool   `json:"all_keys"`
	DeleteAccount bool   `json:"delete_account"`
}

type privacyTarget struct {
	Kind      string   `json:"kind"`               // key | account
	Username  string   `json:"username,omitempty"` // the account's, when there is one
	Account   uint64   `json:"-"`
	Keys      []string `json:"keys"` // what an erase would cover
	LinkedTo  string   `json:"linked_to,omitempty"`
	OtherKeys int      `json:"other_keys,omitempty"` // keys of the same account that this erase would leave alone
}

func fingerprint(k string) string {
	if len(k) > 10 {
		return k[:10]
	}
	return k
}

// resolve turns what the admin typed into the keys to erase.
func (p *privacyAPI) resolve(ctx context.Context, req privacyWho) (privacyTarget, string) {
	who := strings.TrimSpace(req.Who)
	switch {
	case pidRe.MatchString(who):
		t := privacyTarget{Kind: "key", Keys: []string{who}}
		if p.dir != nil {
			if acct, ok := p.dir.AccountOfKey(who); ok {
				t.Account = acct
				if info, ok := p.dir.AccountInfo(ctx, acct); ok {
					t.LinkedTo = info.Username
				}
				all := p.dir.KeysOfAccount(acct)
				if req.AllKeys {
					t.Keys = all
					t.Kind = "account"
					t.Username = t.LinkedTo
				} else {
					t.OtherKeys = len(all) - 1
				}
			}
		}
		return t, ""
	case usernameRe.MatchString(who) && p.dir != nil:
		acct, ok := p.dir.AccountByUsername(ctx, who)
		if !ok {
			return privacyTarget{}, "no account with that username"
		}
		t := privacyTarget{Kind: "account", Account: acct, Keys: p.dir.KeysOfAccount(acct)}
		if info, ok := p.dir.AccountInfo(ctx, acct); ok {
			t.Username = info.Username
		}
		return t, ""
	}
	return privacyTarget{}, "give a player key (43 characters) or an account's username"
}

func (p *privacyAPI) check(w http.ResponseWriter, r *http.Request) (accounts.Account, privacyWho, bool) {
	w.Header().Set("Cache-Control", "no-store")
	a, ok := p.admin(r)
	if !ok {
		jsonErr(w, http.StatusForbidden, "admins only")
		return a, privacyWho{}, false
	}
	var req privacyWho
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		jsonErr(w, http.StatusBadRequest, "send JSON")
		return a, req, false
	}
	return a, req, true
}

func (p *privacyAPI) lookup(w http.ResponseWriter, r *http.Request) {
	_, req, ok := p.check(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	t, msg := p.resolve(ctx, req)
	if msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	var total stats.EraseResult
	for _, k := range t.Keys {
		res, err := p.stats.PreviewErase(ctx, k)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "could not count")
			return
		}
		total.Games += res.Games
		total.Rows += res.Rows
		total.Events += res.Events
	}
	writeJSONOK(w, map[string]any{"target": t, "would_change": total})
}

func (p *privacyAPI) erase(w http.ResponseWriter, r *http.Request) {
	admin, req, ok := p.check(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Confirm) != strings.TrimSpace(req.Who) || strings.TrimSpace(req.Who) == "" {
		jsonErr(w, http.StatusBadRequest, "type the key or username again to confirm")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	t, msg := p.resolve(ctx, req)
	if msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	var total stats.EraseResult
	for _, k := range t.Keys {
		res, err := p.stats.EraseKey(ctx, k)
		if err != nil {
			log.Printf("privacy: erasing %s: %v", fingerprint(k), err)
			jsonErr(w, http.StatusInternalServerError, "erasing failed part way; run it again")
			return
		}
		total.Games += res.Games
		total.Rows += res.Rows
		total.Events += res.Events
		total.Index += res.Index
		if t.Account != 0 && p.unlink != nil {
			p.unlink(t.Account, k) // already gone is fine
		}
	}
	deleted := false
	if req.DeleteAccount && t.Account != 0 && p.delete != nil {
		if err := p.delete(ctx, admin, t.Account); err != nil {
			jsonErr(w, http.StatusConflict, "the keys were erased but the account was not deleted: "+err.Error())
			return
		}
		deleted = true
	}
	var fps []string
	for _, k := range t.Keys {
		fps = append(fps, fingerprint(k))
	}
	if p.audit != nil {
		d := "keys " + strings.Join(fps, ",") + ": " + itoa(total.Games) + " games, " + itoa(total.Rows) + " rows"
		if deleted {
			d += "; account deleted"
		}
		p.audit(ctx, admin, d)
	}
	log.Printf("privacy: erased %d key(s) (%s) on request", len(t.Keys), strings.Join(fps, ","))
	writeJSONOK(w, map[string]any{"erased": total, "keys": len(t.Keys), "account_deleted": deleted})
}

func itoa(n int) string { return strconv.Itoa(n) }

func writeJSONOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
