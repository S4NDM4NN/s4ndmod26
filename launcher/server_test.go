package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIRequiresTokenAndLoopbackHost(t *testing.T) {
	up := NewUpdater(Settings{Server: "http://127.0.0.1:1", Dir: t.TempDir()})
	app, ln, err := NewApp(up)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	h := app.Handler()

	do := func(method, target, host, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		if host != "" {
			r.Host = host
		}
		if token != "" {
			r.Header.Set("X-Token", token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if c := do("GET", "/api/state", "127.0.0.1:5555", "").Code; c != http.StatusForbidden {
		t.Errorf("no token: %d", c)
	}
	if c := do("GET", "/api/state", "127.0.0.1:5555", "wrong").Code; c != http.StatusForbidden {
		t.Errorf("wrong token: %d", c)
	}
	if c := do("GET", "/api/state", "evil.example:5555", app.token).Code; c != http.StatusForbidden {
		t.Errorf("non-loopback host (DNS rebinding): %d", c)
	}
	w := do("GET", "/api/state", "127.0.0.1:5555", app.token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"phase"`) {
		t.Errorf("state: %d %s", w.Code, w.Body.String())
	}
	if c := do("GET", "/api/play", "127.0.0.1:5555", app.token).Code; c != http.StatusMethodNotAllowed {
		t.Errorf("GET on a POST endpoint: %d", c)
	}
	// the page itself also needs the token (via ?t=)
	if c := do("GET", "/", "127.0.0.1:5555", "").Code; c != http.StatusForbidden {
		t.Errorf("index without token: %d", c)
	}
	w = do("GET", "/?t="+app.token, "127.0.0.1:5555", "")
	b, _ := io.ReadAll(w.Body)
	if w.Code != 200 || !strings.Contains(string(b), "S4NDMoD26") {
		t.Errorf("index: %d", w.Code)
	}
}

func TestSettingsRejectsNonHTTPServer(t *testing.T) {
	up := NewUpdater(Settings{Server: "http://127.0.0.1:1", Dir: t.TempDir()})
	app, ln, _ := NewApp(up)
	ln.Close()
	r := httptest.NewRequest("POST", "/api/settings", strings.NewReader(`{"server":"file:///etc/passwd"}`))
	r.Host = "127.0.0.1:1"
	r.Header.Set("X-Token", app.token)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d", w.Code)
	}
}

func TestLogoServedWithTokenOnly(t *testing.T) {
	up := NewUpdater(Settings{Server: "http://127.0.0.1:1", Dir: t.TempDir()})
	app, ln, _ := NewApp(up)
	ln.Close()
	h := app.Handler()

	r := httptest.NewRequest("GET", "/logo.png", nil)
	r.Host = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("logo without token: %d", w.Code)
	}

	r = httptest.NewRequest("GET", "/logo.png?t="+app.token, nil)
	r.Host = "127.0.0.1:1"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Body)
	if w.Code != 200 || len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Errorf("logo: code %d, %d bytes", w.Code, len(b))
	}
}
