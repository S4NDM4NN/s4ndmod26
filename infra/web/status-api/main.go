package main

import (
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
	Online   bool              `json:"online"`
	Hostname string            `json:"hostname,omitempty"`
	Map      string            `json:"map,omitempty"`
	Players  int               `json:"players,omitempty"`
	MaxPlayers int             `json:"max_players,omitempty"`
	PlayerList []Player        `json:"player_list,omitempty"`
	CVars    map[string]string `json:"cvars,omitempty"`
}

var (
	mu     sync.RWMutex
	cached Status
)

func query(addr string) Status {
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return Status{}
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write([]byte("\xff\xff\xff\xffgetstatus\n"))
	if err != nil {
		return Status{}
	}

	buf := make([]byte, 16384)
	n, err := conn.Read(buf)
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

type replaySummary struct {
	Name         string `json:"name"`
	Map          string `json:"map"`
	Gametype     int    `json:"gametype"`
	GametypeName string `json:"gametype_name"`
	DurationMs   int32  `json:"duration_ms"`
	KillCount    int    `json:"kill_count"`
	PlayerCount  int    `json:"player_count"`
	HasPOTG      bool   `json:"has_potg"`
	GeneratedAt  string `json:"generated_at"`
	MatchStartAt string `json:"match_start_at,omitempty"`
}

func replayListHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			http.Error(w, "replay dir unavailable", http.StatusServiceUnavailable)
			return
		}

		var summaries []replaySummary
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			base := strings.TrimSuffix(e.Name(), ".json")
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			var a replay.Analysis
			if err := json.Unmarshal(data, &a); err != nil {
				continue
			}
			kills := 0
			for _, ev := range a.Events {
				if ev.Type == "KILL" {
					kills++
				}
			}
			s := replaySummary{
				Name:         base,
				Map:          a.Meta.Map,
				Gametype:     a.Meta.Gametype,
				GametypeName: a.Meta.GametypeName,
				DurationMs:   a.Meta.DurationMs,
				KillCount:    kills,
				PlayerCount:  len(a.Players),
				HasPOTG:      a.Meta.POTG != nil,
				GeneratedAt:  a.Meta.GeneratedAt,
				MatchStartAt: a.Meta.MatchStartAt,
			}
			summaries = append(summaries, s)
		}

		// Newest first.
		for i, j := 0, len(summaries)-1; i < j; i, j = i+1, j-1 {
			summaries[i], summaries[j] = summaries[j], summaries[i]
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(summaries)
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

	http.HandleFunc("/api/replays", replayListHandler(replayDir))
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
