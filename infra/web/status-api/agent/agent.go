package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"s4ndmod26/status-api/replayviews"
	"strings"
	"sync"
	"time"

	"s4ndmod26/status-api/q3status"
)

// EngineAPI names the interface between the engine and the mod's cgame/ui.  A browser client (the engine, hosted
// on s4ndmod.com) can only join a server whose pk3 it can run, so it compares this number before joining.  Bump
// it only when that interface changes, not for ordinary mod releases.
const EngineAPI = 1

// Agent serves the HTTP API.  Build one with New, then use Handler and RunStatus.
type Agent struct {
	cfg        Config
	id         *Identity
	sup        *Supervisor // nil when the agent does not run the server
	bridge     *Bridge
	replays    fileDir
	stats      fileDir
	downloads  downloads
	pollEvery  time.Duration
	queryAfter time.Duration

	mu     sync.RWMutex
	status q3status.Status
	pk3    pk3Cache
}

// Pk3 describes the mod pk3 this server runs, so a client can fetch exactly that file.
type Pk3 struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"` // relative to the agent
}

type pk3Cache struct {
	name    string
	size    int64
	modTime time.Time
	sum     string
}

// Info is /v1/info: who this server is and what it runs.
type Info struct {
	ID          string           `json:"id"`
	PublicKey   string           `json:"public_key"`
	Hostname    string           `json:"hostname,omitempty"`
	GameVersion string           `json:"game_version,omitempty"`
	EngineAPI   int              `json:"engine_api"`
	Pk3         *Pk3             `json:"pk3,omitempty"`
	Online      bool             `json:"online"`
	Map         string           `json:"map,omitempty"`
	Players     int              `json:"players"`
	MaxPlayers  int              `json:"max_players,omitempty"`
	GamePort    int              `json:"game_port"`
	WebSocket   string           `json:"websocket"` // path of the browser bridge on this agent
	PublicURL   string           `json:"public_url,omitempty"`
	Process     *SupervisorState `json:"process,omitempty"`
	BrowserConn int              `json:"browser_players"`
	Features    []string         `json:"features"`
}

func New(cfg Config, id *Identity, sup *Supervisor) *Agent {
	return &Agent{
		cfg: cfg,
		id:  id,
		sup: sup,
		bridge: &Bridge{
			Game:        &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: cfg.GamePort},
			AllowOrigin: cfg.CORSOrigin,
			MaxConns:    64,
		},
		replays:    fileDir{dir: cfg.ReplayDir(), pattern: replayName, live: replayLive},
		stats:      fileDir{dir: cfg.StatsDir(), pattern: statsName},
		downloads:  newDownloads(cfg),
		pollEvery:  5 * time.Second,
		queryAfter: 2 * time.Second,
	}
}

// RunStatus polls the game server until ctx ends.
func (a *Agent) RunStatus(ctx context.Context) {
	for {
		s := q3status.Query(a.cfg.GameAddr(), a.queryAfter)
		a.mu.Lock()
		a.status = s
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.pollEvery):
		}
	}
}

func (a *Agent) Status() q3status.Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

// Info is also what the registry heartbeat carries.
func (a *Agent) Info() Info {
	s := a.Status()
	info := Info{
		ID:          a.id.ID,
		PublicKey:   a.id.PublicKeyString(),
		Hostname:    s.Hostname,
		GameVersion: a.cfg.GameVersion,
		EngineAPI:   EngineAPI,
		Pk3:         a.currentPk3(),
		Online:      s.Online,
		Map:         s.Map,
		Players:     s.Players,
		MaxPlayers:  s.MaxPlayers,
		GamePort:    a.cfg.GamePort,
		WebSocket:   "/ws",
		PublicURL:   a.cfg.PublicURL,
		BrowserConn: a.bridge.Conns(),
		Features:    []string{"status", "websocket", "replays", "stats", "downloads", "views"},
	}
	if a.sup != nil {
		st := a.sup.State()
		info.Process = &st
	}
	return info
}

// Pk3Name is the name of the mod pk3 this server serves (what latest.txt names), or "".
func (a *Agent) Pk3Name() string {
	if p := a.currentPk3(); p != nil {
		return p.Name
	}
	return ""
}

// currentPk3 hashes the pk3 named by latest.txt once per change of the file.
func (a *Agent) currentPk3() *Pk3 {
	b, err := os.ReadFile(filepath.Join(a.cfg.ModDir(), "latest.txt"))
	if err != nil {
		return nil
	}
	name := strings.TrimSpace(string(b))
	if !a.downloads.pk3.MatchString(name) {
		return nil
	}
	path := filepath.Join(a.cfg.ModDir(), name)
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	a.mu.RLock()
	c := a.pk3
	a.mu.RUnlock()
	if c.name != name || c.size != st.Size() || !c.modTime.Equal(st.ModTime()) {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil
		}
		c = pk3Cache{name: name, size: st.Size(), modTime: st.ModTime(), sum: hex.EncodeToString(h.Sum(nil))}
		a.mu.Lock()
		a.pk3 = c
		a.mu.Unlock()
	}
	return &Pk3{Name: c.name, Size: c.size, SHA256: c.sum, URL: "/dl/" + a.cfg.Mod + "/" + c.name}
}

// Handler is the agent's whole HTTP surface.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, a.Status())
	})
	api.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, a.Info())
	})
	api.HandleFunc("GET /v1/replays", a.replays.list)
	api.HandleFunc("GET /v1/replays/{name}", a.replays.file)
	// the views the site's pages draw from a replay folder (where players went, the fights, the live game), built here from
	// this server's own files, so they work for a server the site does not share a disk with
	api.HandleFunc("GET /v1/games/{match}/paths", replayviews.PathsHandler(a.cfg.ReplayDir()))
	api.HandleFunc("GET /v1/games/{match}/fights", replayviews.FightsHandler(a.cfg.ReplayDir()))
	api.HandleFunc("GET /v1/games/{match}/fights/{n}/view", replayviews.FightViewHandler(a.cfg.ReplayDir()))
	api.HandleFunc("GET /v1/live/view", replayviews.LiveViewHandler(a.cfg.ReplayDir(), replayviews.LiveViewDelay(os.Getenv("LIVE_VIEW_DELAY_SECONDS"))))
	api.HandleFunc("GET /v1/stats", a.stats.list)
	api.HandleFunc("GET /v1/stats/{name}", a.stats.file)
	api.Handle("GET /dl/{mod}/{name}", a.downloads)
	mux.Handle("/v1/", a.cors(api))
	mux.Handle("/dl/", a.cors(api))
	mux.Handle("/ws", a.bridge)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// The browser client's local-network mode connects to ws://host:port/ with no path.
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			a.bridge.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "s4nd-agent: see /v1/info\n")
	})
	return mux
}

func (a *Agent) cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the play page is cross-origin isolated (COOP/COEP), which only loads other sites' files that allow it like this
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		if a.cfg.CORSOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", a.cfg.CORSOrigin)
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges, Last-Modified")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Range, If-Modified-Since, Cache-Control, Pragma")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}
