package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
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

func poll(addr string) {
	for {
		s := query(addr)
		mu.Lock()
		cached = s
		mu.Unlock()
		time.Sleep(10 * time.Second)
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

	addr := net.JoinHostPort(host, port)
	log.Printf("polling %s every 10s", addr)
	go poll(addr)

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
