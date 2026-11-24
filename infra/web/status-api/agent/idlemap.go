package agent

import (
	"context"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"time"
)

// Idle map: with AGENT_IDLE_MAP set (say mp_beach), a server nobody human is on for AGENT_IDLE_MINUTES (default 10)
// goes back to that map.  A browser or a fresh launcher install only has the demo maps (mp_beach, mp_trenchtoast),
// so a server left on another map cannot be joined by them.  Bots do not count as players; a player who is still
// connecting does.  The change is an rcon `map` command to the server's own port, with RCON_PASSWORD.

// rconStatusRow matches one player row of the engine's `status` output: the slot, score, ping (a number, CON or ZMB),
// then the name (which may hold spaces), the address and the rate.
var rconStatusRow = regexp.MustCompile(`^\s*\d+\s+-?\d+\s+(\d+|CON|ZMB)\s+`)

// CountHumans reads the text an rcon `status` printed and counts the players that are not bots (their address is
// "bot").  The name can contain spaces, so the address is taken from the end of the row: ... address rate.
func CountHumans(status string) (humans, bots int) {
	for _, line := range strings.Split(status, "\n") {
		if !rconStatusRow.MatchString(line) {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		if strings.HasPrefix(strings.TrimPrefix(f[len(f)-2], "^7"), "bot") {
			bots++
		} else {
			humans++
		}
	}
	return humans, bots
}

// rcon sends one command and returns what the server printed.  The engine answers with one or more "print"
// packets, so it reads until the server has been quiet for a moment.
func rcon(addr, password, command string, wait time.Duration) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return "", err
	}
	// An unconnected socket, like q3status.Query: replies can arrive from another source address.
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.WriteToUDP([]byte("\xff\xff\xff\xffrcon "+password+" "+command), raddr); err != nil {
		return "", err
	}
	var out strings.Builder
	buf := make([]byte, 16384)
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		const hdr = "\xff\xff\xff\xffprint\n"
		if strings.HasPrefix(string(buf[:n]), hdr) {
			out.Write(buf[len(hdr):n])
		}
		deadline = time.Now().Add(wait) // more may follow, but not for long
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("no answer from the server (is the rcon password right?)")
	}
	return out.String(), nil
}

// IdleMapState is the watcher's memory between polls; it is a type of its own so the decision can be tested
// without a server or a clock.
type IdleMapState struct {
	emptySince time.Time
}

// Decide is called after each status poll.  onIdleMap says the server is already on the wanted map; humans is -1
// when it is not known yet.  It returns true when the server should be sent back to the idle map now.
func (s *IdleMapState) Decide(now time.Time, online, onIdleMap bool, humans int, after time.Duration) bool {
	if !online || onIdleMap || humans != 0 {
		s.emptySince = time.Time{}
		return false
	}
	if s.emptySince.IsZero() {
		s.emptySince = now
	}
	if now.Sub(s.emptySince) < after {
		return false
	}
	s.emptySince = time.Time{} // starts over if the change did not take
	return true
}

// RunIdleMap watches the status the agent already polls and changes the map when nobody human has been there for
// a while.  It returns at once when the feature is off or has no rcon password to work with.
func (a *Agent) RunIdleMap(ctx context.Context) {
	c := a.cfg
	if c.IdleMap == "" {
		return
	}
	if c.RconPassword == "" {
		log.Printf("idle map: AGENT_IDLE_MAP=%s needs RCON_PASSWORD (the starter config makes a random one the agent cannot read back); not watching", c.IdleMap)
		return
	}
	after := time.Duration(c.IdleMinutes) * time.Minute
	if a.idleAfter > 0 {
		after = a.idleAfter // tests
	}
	log.Printf("idle map: back to %s after %s with no human players", c.IdleMap, after)
	var st IdleMapState
	every := a.idleEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s := a.Status()
		onIdle := strings.EqualFold(s.Map, c.IdleMap)
		humans := -1
		if s.Online && !onIdle {
			if s.Players == 0 {
				humans = 0
			} else if text, err := rcon(c.GameAddr(), c.RconPassword, "status", 300*time.Millisecond); err == nil {
				humans, _ = CountHumans(text)
			} // on an error humans stays unknown: never change the map on a guess
		}
		if !st.Decide(time.Now(), s.Online, onIdle, humans, after) {
			continue
		}
		cmd := "map "
		if c.DevMap {
			cmd = "devmap "
		}
		log.Printf("idle map: no human players on %s for %s, changing to %s", s.Map, after, c.IdleMap)
		if _, err := rcon(c.GameAddr(), c.RconPassword, cmd+c.IdleMap, 200*time.Millisecond); err != nil {
			log.Printf("idle map: could not change the map: %v", err)
		}
	}
}
