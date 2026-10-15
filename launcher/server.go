package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

//go:embed ui/index.html
var indexHTML []byte

// App is the local web UI: an http server on 127.0.0.1 that the native window (or the browser, as a
// fallback) points at.  Every request needs the random token, and the Host header must be loopback, so
// neither a web page in another tab nor a DNS-rebinding page can drive the launcher.
type App struct {
	up    *Updater
	token string
	quit  chan struct{}
	port  int

	clients  atomic.Int32 // open event streams
	everSeen atomic.Bool
}

func NewApp(up *Updater) (*App, net.Listener, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	a := &App{up: up, token: hex.EncodeToString(b[:]), quit: make(chan struct{}), port: ln.Addr().(*net.TCPAddr).Port}
	return a, ln, nil
}

func (a *App) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", a.port, a.token) }

func (a *App) Quit() {
	select {
	case <-a.quit:
	default:
		close(a.quit)
	}
}

func (a *App) authed(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host != "127.0.0.1" && host != "localhost" {
		return false
	}
	t := r.Header.Get("X-Token")
	if t == "" {
		t = r.URL.Query().Get("t")
	}
	return subtle.ConstantTimeCompare([]byte(t), []byte(a.token)) == 1
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !a.authed(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	post := func(h http.HandlerFunc) http.HandlerFunc {
		return guard(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			h(w, r)
		})
	}

	mux.HandleFunc("/", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(indexHTML)
	}))
	mux.HandleFunc("/api/state", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.up.Snapshot())
	}))
	mux.HandleFunc("/api/events", guard(a.events))
	mux.HandleFunc("/api/update", post(func(w http.ResponseWriter, r *http.Request) {
		go a.up.Update()
		w.WriteHeader(http.StatusAccepted)
	}))
	mux.HandleFunc("/api/recheck", post(func(w http.ResponseWriter, r *http.Request) {
		go a.up.Check()
		w.WriteHeader(http.StatusAccepted)
	}))
	mux.HandleFunc("/api/play", post(func(w http.ResponseWriter, r *http.Request) {
		if err := a.up.Play(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		go func() { time.Sleep(1200 * time.Millisecond); a.Quit() }()
	}))
	mux.HandleFunc("/api/openfolder", post(func(w http.ResponseWriter, r *http.Request) {
		openPath(a.up.Snapshot().Dir)
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/settings", post(func(w http.ResponseWriter, r *http.Request) {
		var in Settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		in.Server = strings.TrimRight(strings.TrimSpace(in.Server), "/")
		if in.Server != "" && !strings.HasPrefix(in.Server, "http://") && !strings.HasPrefix(in.Server, "https://") {
			http.Error(w, "server must start with http:// or https://", http.StatusBadRequest)
			return
		}
		in.Dir, in.Connect = strings.TrimSpace(in.Dir), strings.TrimSpace(in.Connect)
		if err := a.up.Configure(in); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	mux.HandleFunc("/api/quit", post(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go a.Quit()
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// events streams snapshots as server-sent events.
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch, cancel := a.up.Subscribe()
	defer cancel()
	a.everSeen.Store(true)
	a.clients.Add(1)
	defer a.clients.Add(-1)
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case s := <-ch:
			b, _ := json.Marshal(s)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-keep.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-a.quit:
			return
		}
	}
}

// WatchBrowserClose quits once a page has been connected and then goes away for a few seconds (the
// browser-fallback mode has no window of its own to tell us it was closed).
func (a *App) WatchBrowserClose() {
	var gone time.Time
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			if !a.everSeen.Load() {
				continue
			}
			if a.clients.Load() > 0 {
				gone = time.Time{}
				continue
			}
			if gone.IsZero() {
				gone = time.Now()
			} else if time.Since(gone) > 5*time.Second {
				a.Quit()
				return
			}
		}
	}
}
