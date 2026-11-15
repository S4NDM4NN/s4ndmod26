// Package q3status asks an id Tech 3 server (iowolfded) for its status over UDP ("getstatus") and parses the
// answer.  Shared by the website's status API and the game server's agent.
package q3status

import (
	"net"
	"strconv"
	"strings"
	"time"
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

// Query sends getstatus to addr (host:port) and waits up to timeout for the answer.  An unreachable or silent
// server gives a Status with Online false.
func Query(addr string, timeout time.Duration) Status {
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

	conn.SetDeadline(time.Now().Add(timeout))
	_, err = conn.WriteToUDP([]byte("\xff\xff\xff\xffgetstatus\n"), raddr)
	if err != nil {
		return Status{}
	}

	buf := make([]byte, 16384)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return Status{}
	}
	return Parse(buf[:n])
}

// Parse reads a statusResponse packet.
func Parse(packet []byte) Status {
	resp := string(packet)
	// Expected format: \xff\xff\xff\xffstatusResponse\n\\key\\val\\...\nplayer lines
	const header = "\xff\xff\xff\xffstatusResponse\n"
	if !strings.HasPrefix(resp, header) {
		return Status{}
	}
	resp = resp[len(header):]

	lines := strings.Split(resp, "\n")
	cvars := ParseCVars(lines[0])
	players := []Player{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		players = append(players, ParsePlayer(line))
	}

	maxPlayers, _ := strconv.Atoi(cvars["sv_maxclients"])

	return Status{
		Online:     true,
		Hostname:   StripColors(cvars["sv_hostname"]),
		Map:        cvars["mapname"],
		Players:    len(players),
		MaxPlayers: maxPlayers,
		PlayerList: players,
		CVars:      cvars,
	}
}

// ParseCVars reads "\key\value\key\value...".
func ParseCVars(s string) map[string]string {
	m := map[string]string{}
	parts := strings.Split(s, "\\")
	for i := 1; i+1 < len(parts); i += 2 {
		m[parts[i]] = parts[i+1]
	}
	return m
}

// ParsePlayer reads one player line: score ping "name".
func ParsePlayer(line string) Player {
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
		p.Name = StripColors(name)
	}
	return p
}

// StripColors removes Quake-style color codes (^0-^9).
func StripColors(s string) string {
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
