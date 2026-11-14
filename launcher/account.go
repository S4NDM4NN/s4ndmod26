package main

// Signing in to s4ndmod.com from the launcher, and linking this computer's player key to the account.
//
// The launcher never sees a password.  It asks the site for a sign-in (POST /api/device/start), opens the site's
// approval page in the browser (where the person is signed in, or signs in, and compares the code), polls until the
// person approves, and is handed a short session that only the link routes accept.  With it the launcher makes the
// same two calls the website's play page makes (POST /api/link/start, then /api/link/direct with the key's signature
// over the code) and ends the session at once.  Only the username is remembered; there is no token to steal.
//
// Keys are never replaced or deleted.  identity.dat in the mod folder is the key the game uses (the same 33 bytes the
// game's cgame writes: 32-byte Ed25519 seed, then 'w' or 's').  Games played under a key before anyone signed in
// belong to that key, so:
//   - a key exists and nobody owns it: link THAT key, create nothing;
//   - no key: make a strong one (crypto/rand), then link it;
//   - the key belongs to another account (a second person on this computer): leave it alone, and switch the game to
//     a key of this account: one kept from an earlier sign-in, or a new one.  Every key that is switched away from
//     stays in <config>/s4ndmod-launcher/keys/ so it can be switched back to.
//   - signing out only forgets the username.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	modFolder   = "s4ndmod26"
	identityDat = "identity.dat"
)

// Account is the launcher's account state and sign-in flow.
type Account struct {
	server func() string // the site, from the settings
	dir    func() string // the install folder
	client *http.Client
	open   func(url string) // opens the approval page in the browser
	store  string           // <config>/s4ndmod-launcher: account.json and keys/
	poll   time.Duration    // 0: what the site asks for

	mu     sync.Mutex
	st     AccountState
	cancel chan struct{}
}

// AccountState is what the page shows.
type AccountState struct {
	Username string `json:"username"`          // signed in as (remembered)
	Avatar   string `json:"avatar,omitempty"`  // the profile picture's name, as on the site ("medic_axis")
	Phase    string `json:"phase"`             // "", "waiting" (for the approval), "linking"
	Code     string `json:"code,omitempty"`    // the request's id in the approval page's address
	URL      string `json:"url,omitempty"`     // the approval page, in case the browser did not open
	Message  string `json:"message,omitempty"` // the last outcome, or the last problem
	Bad      bool   `json:"bad,omitempty"`
	Key      string `json:"key,omitempty"` // the first characters of the key the game uses
}

type accountFile struct {
	Username string            `json:"username"`
	Avatar   string            `json:"avatar,omitempty"`
	Owners   map[string]string `json:"owners,omitempty"` // key -> the username it was linked to
}

func NewAccount(server, dir func() string, open func(string)) *Account {
	a := &Account{server: server, dir: dir, open: open, client: &http.Client{Timeout: 20 * time.Second}}
	if d, err := os.UserConfigDir(); err == nil {
		a.store = filepath.Join(d, "s4ndmod-launcher")
	}
	a.load()
	return a
}

func (a *Account) filePath() string { return filepath.Join(a.store, "account.json") }

func (a *Account) readFile() accountFile {
	var f accountFile
	if a.store != "" {
		if b, err := os.ReadFile(a.filePath()); err == nil {
			json.Unmarshal(b, &f)
		}
	}
	if f.Owners == nil {
		f.Owners = map[string]string{}
	}
	return f
}

func (a *Account) writeFile(f accountFile) error {
	if a.store == "" {
		return nil
	}
	if err := os.MkdirAll(a.store, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	return writeAtomic(a.filePath(), b, 0o600)
}

func (a *Account) load() {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.readFile()
	a.st.Username, a.st.Avatar = f.Username, avatarOK(f.Avatar)
	a.st.Key = a.currentKeyShort()
}

// State is a copy for the page.
func (a *Account) State() AccountState {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Key = a.currentKeyShort()
	return a.st
}

func (a *Account) set(f func(*AccountState)) {
	a.mu.Lock()
	f(&a.st)
	a.mu.Unlock()
}

// SignOut forgets the username.  Keys stay where they are.
func (a *Account) SignOut() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st.Phase != "" {
		return
	}
	f := a.readFile()
	f.Username, f.Avatar = "", ""
	a.writeFile(f)
	a.st = AccountState{}
}

// Cancel stops a sign-in that is waiting for the approval.
func (a *Account) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		close(a.cancel)
		a.cancel = nil
	}
}

var errBusy = errors.New("a sign-in is already running")

// SignIn starts the flow in the background.
func (a *Account) SignIn() error {
	a.mu.Lock()
	if a.st.Phase != "" {
		a.mu.Unlock()
		return errBusy
	}
	a.st = AccountState{Username: a.st.Username, Avatar: a.st.Avatar, Phase: "waiting", Message: "Asking the site…"}
	cancel := make(chan struct{})
	a.cancel = cancel
	a.mu.Unlock()
	go func() {
		user, avatar, msg, err := a.run(cancel)
		a.mu.Lock()
		a.cancel = nil
		a.st.Phase, a.st.Code, a.st.URL = "", "", ""
		if err != nil {
			a.st.Message, a.st.Bad = err.Error(), true
		} else {
			a.st.Username, a.st.Avatar, a.st.Message, a.st.Bad = user, avatar, msg, false
		}
		a.mu.Unlock()
	}()
	return nil
}

// ---------- the site's calls ----------

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func (a *Account) call(method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(a.server(), "/")+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the site: %v", errText(err))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("the site answered %d", resp.StatusCode)
		}
		return &apiError{resp.StatusCode, e.Error}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("the site's answer made no sense")
		}
	}
	return nil
}

func errText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// ---------- the flow ----------

func (a *Account) run(cancel chan struct{}) (user, avatar, msg string, err error) {
	var st struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	host, _ := os.Hostname()
	if err = a.call("POST", "/api/device/start", "", map[string]string{"label": "S4NDMoD launcher on " + host}, &st); err != nil {
		return
	}
	// the page to open is built here from the site address the person chose, never taken from the answer
	page := strings.TrimRight(a.server(), "/") + "/account/device/?code=" + url.QueryEscape(st.UserCode)
	a.set(func(s *AccountState) {
		s.Code, s.URL, s.Message = st.UserCode, page, "Approve the sign-in on the website."
	})
	if a.open != nil {
		a.open(page)
	}
	interval := time.Duration(st.Interval) * time.Second
	if a.poll > 0 {
		interval = a.poll
	}
	if interval < time.Second && a.poll == 0 {
		interval = 2 * time.Second
	}
	deadline := time.Now().Add(time.Duration(st.ExpiresIn) * time.Second)
	var got struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Avatar   string `json:"avatar"`
	}
	for {
		select {
		case <-cancel:
			return "", "", "", errors.New("Sign-in cancelled.")
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return "", "", "", errors.New("The sign-in was not approved in time. Press Sign in to try again.")
		}
		err = a.call("POST", "/api/device/poll", "", map[string]string{"device_code": st.DeviceCode}, &got)
		var ae *apiError
		switch {
		case err == nil && got.Token != "":
			goto approved
		case err == nil, errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests:
			continue // still waiting
		case errors.As(err, &ae) && ae.Status == http.StatusForbidden:
			return "", "", "", errors.New("The sign-in was refused on the website.")
		case errors.As(err, &ae) && ae.Status == http.StatusGone:
			return "", "", "", errors.New("The sign-in expired. Press Sign in to try again.")
		default:
			return "", "", "", err
		}
	}
approved:
	a.set(func(s *AccountState) { s.Phase, s.Message = "linking", "Linking this computer to "+got.Username+"…" })
	defer a.call("POST", "/api/account/logout", got.Token, nil, nil) // the session is only for this
	note, err := a.linkKey(got.Token, got.Username)
	if err != nil {
		return "", "", "", fmt.Errorf("Signed in as %s, but linking the player key failed: %v", got.Username, err)
	}
	f := a.readFile()
	f.Username, f.Avatar = got.Username, avatarOK(got.Avatar)
	a.writeFile(f)
	return got.Username, f.Avatar, note, nil
}

// avatarOK passes a profile picture name only when it has the site's shape ("medic_axis"), since the page builds an
// image address from it.
func avatarOK(s string) string {
	for _, team := range []string{"_axis", "_allies"} {
		if c, ok := strings.CutSuffix(s, team); ok && c != "" && strings.Trim(c, "abcdefghijklmnopqrstuvwxyz") == "" {
			return s
		}
	}
	return ""
}

// ---------- keys ----------

func (a *Account) identityFile() string { return filepath.Join(a.dir(), modFolder, identityDat) }
func (a *Account) keysDir() string      { return filepath.Join(a.store, "keys") }

func pubOf(seed []byte) string {
	return base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
}

// readIdentity returns the seed and its quality letter, or ok=false when there is no usable file.
func readIdentity(path string) (seed []byte, q byte, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) != 33 || (b[32] != 'w' && b[32] != 's') {
		return nil, 0, false
	}
	return b[:32], b[32], true
}

func (a *Account) currentKeyShort() string {
	if a.dir == nil {
		return ""
	}
	if seed, _, ok := readIdentity(a.identityFile()); ok {
		return pubOf(seed)[:10]
	}
	return ""
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// keep copies a key into the launcher's store (never overwriting a different one).
func (a *Account) keep(seed []byte, q byte) {
	if a.store == "" {
		return
	}
	p := filepath.Join(a.keysDir(), pubOf(seed)+".dat")
	if _, err := os.Stat(p); err == nil {
		return
	}
	writeAtomic(p, append(append([]byte{}, seed...), q), 0o600)
}

func (a *Account) install(seed []byte, q byte) error {
	return writeAtomic(a.identityFile(), append(append([]byte{}, seed...), q), 0o600)
}

func newStrongSeed() ([]byte, error) {
	seed := make([]byte, 32)
	_, err := rand.Read(seed)
	return seed, err
}

// ownKey is a key kept from an earlier sign-in as this account, other than skip.
func (a *Account) ownKey(f accountFile, user, skip string) (seed []byte, q byte, ok bool) {
	for pub, owner := range f.Owners {
		if !strings.EqualFold(owner, user) || pub == skip {
			continue
		}
		if s, qq, ok := readIdentity(filepath.Join(a.keysDir(), pub+".dat")); ok && pubOf(s) == pub {
			return s, qq, true
		}
	}
	return nil, 0, false
}

// linkKey applies the rules above and returns what to tell the person.
func (a *Account) linkKey(token, user string) (string, error) {
	if a.dir == nil || a.dir() == "" {
		return "", errors.New("no install folder")
	}
	f := a.readFile()
	seed, q, have := readIdentity(a.identityFile())
	note := "Signed in as " + user + "."
	if have {
		a.keep(seed, q) // never lose the key the game has been using
	} else {
		// no key: one this account used before, else a new strong one
		if s, qq, ok := a.ownKey(f, user, ""); ok {
			seed, q = s, qq
		} else {
			var err error
			if seed, err = newStrongSeed(); err != nil {
				return "", err
			}
			q = 's'
		}
		a.keep(seed, q)
		if err := a.install(seed, q); err != nil {
			return "", err
		}
		note += " Made a player key for this computer."
	}
	err := a.linkDirect(token, seed)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict {
		// the key belongs to someone else: leave it, use one of this account's
		other := pubOf(seed)
		if s, qq, ok := a.ownKey(f, user, other); ok {
			seed, q = s, qq
		} else {
			var e2 error
			if seed, e2 = newStrongSeed(); e2 != nil {
				return "", e2
			}
			q = 's'
		}
		a.keep(seed, q)
		if err := a.install(seed, q); err != nil {
			return "", err
		}
		if err = a.linkDirect(token, seed); err != nil {
			return "", err
		}
		note = "Signed in as " + user + ". The player key on this computer belongs to another account, so the game now uses a key of yours. The other one is kept."
	} else if err != nil {
		return "", err
	} else if have {
		note += " This computer's player key is linked: its past games are yours."
	}
	f.Owners[pubOf(seed)] = user
	a.writeFile(f)
	return note, nil
}

// linkDirect does what the play page does: ask for a code, sign it with the key, post the signature.
func (a *Account) linkDirect(token string, seed []byte) error {
	var c struct {
		Code string `json:"code"`
	}
	if err := a.call("POST", "/api/link/start", token, struct{}{}, &c); err != nil {
		return err
	}
	code := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(c.Code))
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), []byte("s4nd-link-v1\n"+code+"\ndirect"))
	host, _ := os.Hostname() // the key's name in the account's key list (the owner can rename it)
	return a.call("POST", "/api/link/direct", token, map[string]string{
		"key": pubOf(seed), "code": c.Code, "sig": base64.RawURLEncoding.EncodeToString(sig), "label": host}, nil)
}
