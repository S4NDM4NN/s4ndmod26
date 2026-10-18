package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"s4ndmod26/status-api/replay"
)

type Player struct {
	Score int    `json:"score"`
	Ping  int    `json:"ping"`
	Name  string `json:"name"`
}

type Status struct {
	Online     bool              `json:"online"`
	Hostname   string            `json:"hostname,omitempty"`
	Map        string            `json:"map,omitempty"`
	Players    int               `json:"players,omitempty"`
	MaxPlayers int               `json:"max_players,omitempty"`
	PlayerList []Player          `json:"player_list,omitempty"`
	CVars      map[string]string `json:"cvars,omitempty"`
}

var (
	mu     sync.RWMutex
	cached Status
)

func query(addr string) Status {
	// rtcw-server runs with hostNetwork: true, so replies routed back through
	// the CNI arrive with their source address rewritten (hairpin masquerade)
	// rather than the address we sent to. A connected socket (net.Dial) drops
	// those as spoofed, so use an unconnected socket that accepts any source.
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return Status{}
	}

	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return Status{}
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.WriteToUDP([]byte("\xff\xff\xff\xffgetstatus\n"), raddr)
	if err != nil {
		return Status{}
	}

	buf := make([]byte, 16384)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return Status{}
	}

	resp := string(buf[:n])
	// Expected format: \xff\xff\xff\xffstatusResponse\n\\key\\val\\...\nplayer lines
	const header = "\xff\xff\xff\xffstatusResponse\n"
	if !strings.HasPrefix(resp, header) {
		return Status{}
	}
	resp = resp[len(header):]

	lines := strings.Split(resp, "\n")
	if len(lines) < 1 {
		return Status{}
	}

	cvars := parseCVars(lines[0])
	players := []Player{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p := parsePlayer(line)
		players = append(players, p)
	}

	maxPlayers, _ := strconv.Atoi(cvars["sv_maxclients"])

	return Status{
		Online:     true,
		Hostname:   stripColors(cvars["sv_hostname"]),
		Map:        cvars["mapname"],
		Players:    len(players),
		MaxPlayers: maxPlayers,
		PlayerList: players,
		CVars:      cvars,
	}
}

func parseCVars(s string) map[string]string {
	m := map[string]string{}
	parts := strings.Split(s, "\\")
	for i := 1; i+1 < len(parts); i += 2 {
		m[parts[i]] = parts[i+1]
	}
	return m
}

func parsePlayer(line string) Player {
	// Format: score ping "name"
	parts := strings.SplitN(line, " ", 3)
	p := Player{}
	if len(parts) >= 1 {
		p.Score, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		p.Ping, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		name := parts[2]
		name = strings.Trim(name, "\"")
		p.Name = stripColors(name)
	}
	return p
}

// stripColors removes Quake-style color codes (^0-^9).
func stripColors(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '^' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// gamesList is the games-list response, rebuilt in the background (see refreshGamesList) so a
// request only ever serves bytes that are already encoded.
type gamesList struct {
	mu   sync.RWMutex
	body []byte // JSON array, newest first
}

func (g *gamesList) set(b []byte) {
	g.mu.Lock()
	g.body = b
	g.mu.Unlock()
}

func (g *gamesList) get() []byte {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.body
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// refreshGamesList lists every analyzed game.  Summaries come from small per-game sidecars written
// by the scanner (replay.LoadSummary), and are kept in memory once read, so a refresh normally
// costs one directory read plus a couple of stats per game; only a game that has never been
// summarized needs its full analysis JSON parsed.
func refreshGamesList(dir, statsDir string, known map[string]replay.Summary, out *gamesList) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(entries))
	summaries := make([]replay.Summary, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".summary.json") {
			continue
		}
		base := strings.TrimSuffix(name, ".json")
		seen[base] = true
		s, ok := known[base]
		if !ok {
			if s, ok = replay.LoadSummary(dir, base); !ok {
				continue
			}
			known[base] = s
		}
		// The .rpl can be pruned long before the .json, so check it on every refresh.
		s.HasRPL = s.HasPOTG &&
			fileExists(filepath.Join(dir, base+".rpl")) &&
			fileExists(filepath.Join(dir, base+".txt"))
		s.HasStats = statsDir != "" && fileExists(filepath.Join(statsDir, base+".jsonl"))
		summaries = append(summaries, s)
	}
	for base := range known { // files pruned from disk
		if !seen[base] {
			delete(known, base)
		}
	}
	// Newest first.
	for i, j := 0, len(summaries)-1; i < j; i, j = i+1, j-1 {
		summaries[i], summaries[j] = summaries[j], summaries[i]
	}
	if body, err := json.Marshal(summaries); err == nil {
		out.set(body)
	}
}

// runGamesList keeps out fresh.  The first refresh may backfill sidecars for games analyzed before
// they existed, which is why this runs off the request path.
func runGamesList(dir, statsDir string, out *gamesList) {
	known := map[string]replay.Summary{}
	for {
		refreshGamesList(dir, statsDir, known, out)
		time.Sleep(15 * time.Second)
	}
}

// replayListHandler serves the games list, gzipped when the client accepts it.  The compressed form
// is cached per list body, so a request costs no compression until the list changes.
func replayListHandler(out *gamesList) http.HandlerFunc {
	var (
		mu     sync.Mutex
		rawFor []byte
		gz     []byte
	)
	return func(w http.ResponseWriter, r *http.Request) {
		body := out.get()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Vary", "Accept-Encoding")
		if body == nil {
			// Still building the first list (startup); say so rather than showing "no games".
			http.Error(w, "games list is warming up", http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			mu.Lock()
			if len(rawFor) == 0 || len(rawFor) != len(body) || &rawFor[0] != &body[0] {
				var buf bytes.Buffer
				zw := gzip.NewWriter(&buf)
				zw.Write(body)
				zw.Close()
				rawFor, gz = body, buf.Bytes()
			}
			z := gz
			mu.Unlock()
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(z)
			return
		}
		w.Write(body)
	}
}

func poll(addr string) {
	for {
		s := query(addr)
		mu.Lock()
		cached = s
		mu.Unlock()
		time.Sleep(10 * time.Second)
	}
}

func liveStreamHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		tick := func() (done bool) {
			rplPath, baseName, found := replay.FindLiveReplay(dir)
			if !found {
				fmt.Fprintf(w, "event: no_game\ndata: {}\n\n")
				flusher.Flush()
				return false
			}

			rep, err := replay.ParseLive(rplPath)
			if err == nil && len(rep.Frames) > 0 {
				analysis := replay.AnalyzeLive(rep)
				if data, err := json.Marshal(analysis); err == nil {
					fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
					flusher.Flush()
				}
			}

			// Send match_over once the .txt appears (written at intermission).
			if _, err := os.Stat(filepath.Join(dir, baseName+".txt")); err == nil {
				fmt.Fprintf(w, "event: match_over\ndata: {\"name\":%q}\n\n", baseName)
				flusher.Flush()
				return true
			}
			return false
		}

		if tick() {
			return
		}

		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if tick() {
					return
				}
			}
		}
	}
}

func main() {
	host := os.Getenv("RTCW_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("RTCW_PORT")
	if port == "" {
		port = "27960"
	}
	apiPort := os.Getenv("STATUS_API_PORT")
	if apiPort == "" {
		apiPort = "8080"
	}

	replayDir := os.Getenv("REPLAY_DIR")
	if replayDir == "" {
		replayDir = "/usr/share/nginx/html/downloads/s4ndmod26/replays"
	}
	replayCfg := replay.RetentionConfig{MaxAgeDays: 10, RplMaxAgeDays: -1}
	if v := os.Getenv("REPLAY_MAX_AGE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			replayCfg.MaxAgeDays = n
		}
	}
	if v := os.Getenv("REPLAY_RPL_MAX_AGE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			replayCfg.RplMaxAgeDays = n
		}
	}
	go replay.RunScanner(replayDir, replayCfg)

	addr := net.JoinHostPort(host, port)
	log.Printf("polling %s every 10s", addr)
	go poll(addr)

	// Where the game's pk3s are served from (mod directory first); levelshots are looked up in them.
	pk3Dirs := []string{"/usr/share/nginx/html/downloads/s4ndmod26", "/usr/share/nginx/html/downloads/main"}
	if v := os.Getenv("PK3_DIRS"); v != "" {
		pk3Dirs = filepath.SplitList(v)
	}
	shots := &levelshotStore{dirs: pk3Dirs}
	http.HandleFunc("/api/levelshot/", levelshotHandler(shots))
	http.HandleFunc("GET /api/maps/{map}/mesh", mapMeshHandler(&mapMeshStore{shots: shots}))

	// Update manifest for the desktop launcher (see manifest.go); hashes what is served on disk.
	webRoot := os.Getenv("WEB_ROOT")
	if webRoot == "" {
		webRoot = "/usr/share/nginx/html"
	}
	http.HandleFunc("/api/manifest", manifestHandler(newManifestBuilder(webRoot)))

	statsDir := os.Getenv("STATS_DIR")
	if statsDir == "" {
		statsDir = "/usr/share/nginx/html/downloads/s4ndmod26/stats"
	}
	gameStats := startGameStats(statsDir, replayDir)
	http.HandleFunc("GET /api/games/{match}/stats", gameStatsHandler(gameStats))
	http.HandleFunc("GET /api/games/{match}/feed", gameFeedHandler(gameStats))
	http.HandleFunc("GET /api/games/{match}/paths", gamePathsHandler(replayDir))
	http.HandleFunc("GET /api/games/{match}/fights", gameFightsHandler(replayDir))

	games := &gamesList{}
	listStatsDir := statsDir
	if gameStats == nil {
		listStatsDir = "" // no database: do not offer stats links
	}
	go runGamesList(replayDir, listStatsDir, games)
	http.HandleFunc("/api/replays", replayListHandler(games))
	http.HandleFunc("/api/live/stream", liveStreamHandler(replayDir))

	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		s := cached
		mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(s)
	})

	log.Printf("listening on :%s", apiPort)
	log.Fatal(http.ListenAndServe(":"+apiPort, nil))
}
