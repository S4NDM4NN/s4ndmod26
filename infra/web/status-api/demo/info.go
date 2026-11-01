package demo

import (
	"strconv"
	"strings"
)

const (
	csServerInfo = 0
	csSysInfo    = 1
	csPlayers    = 576 // CS_PLAYERS; identical across protocols 57, 59 and 60
	maxClients   = 64
)

// InfoValue returns the value for key in a backslash-delimited info string.
func InfoValue(info, key string) string {
	parts := strings.Split(info, "\\")
	// info strings begin with a backslash, so parts[0] is empty
	for i := 1; i+1 < len(parts); i += 2 {
		if strings.EqualFold(parts[i], key) {
			return parts[i+1]
		}
	}
	return ""
}

// CleanName strips ^x colour codes from a player name.
func CleanName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '^' && i+1 < len(s) && s[i+1] != '^' {
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

// PlayerInfo is one client slot from the CS_PLAYERS configstrings.
type PlayerInfo struct {
	Name string
	Team int // 1 axis, 2 allies, 3 spectator
}

// Info is the descriptive summary of a demo shown to the user.
type Info struct {
	Protocol   int
	Map        string
	ServerID   string
	Mod        string
	ClientNum  int
	Recorder   string
	Spectator  bool
	StartTime  int
	EndTime    int
	Frames     int
	Players    map[int]PlayerInfo
	CommandCnt int
}

// Players returns the final known roster (updated by "cs" commands).
func (d *Demo) roster() map[int]PlayerInfo {
	out := map[int]PlayerInfo{}
	for i := 0; i < maxClients; i++ {
		s, ok := d.Configstrings[csPlayers+i]
		if !ok || s == "" {
			continue
		}
		team, _ := strconv.Atoi(InfoValue("\\"+s, "t"))
		out[i] = PlayerInfo{Name: CleanName(InfoValue("\\"+s, "n")), Team: team}
	}
	return out
}

// Describe summarises a parsed demo.
func Describe(d *Demo) Info {
	in := Info{
		Protocol:   d.Protocol,
		Map:        InfoValue(d.Configstrings[csServerInfo], "mapname"),
		ServerID:   InfoValue(d.Configstrings[csSysInfo], "sv_serverid"),
		Mod:        InfoValue(d.Configstrings[csServerInfo], "gameversion"),
		ClientNum:  d.ClientNum,
		Frames:     len(d.Frames),
		Players:    d.roster(),
		CommandCnt: len(d.Commands),
	}
	if len(d.Frames) > 0 {
		in.StartTime = d.Frames[0].ServerTime
		in.EndTime = d.Frames[len(d.Frames)-1].ServerTime
	}
	if p, ok := in.Players[d.ClientNum]; ok {
		in.Recorder = p.Name
		in.Spectator = p.Team == 3
	} else {
		in.Spectator = true // a slot outside the roster is a free spectator
	}
	return in
}
