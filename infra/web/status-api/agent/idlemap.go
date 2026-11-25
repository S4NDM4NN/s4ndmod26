package agent

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"
)

// Idle map: with AGENT_IDLE_MAP set (say mp_beach), a server nobody human is on for AGENT_IDLE_MINUTES (default 10)
// goes back to that map.  A browser or a fresh launcher install only has the demo maps (mp_beach, mp_trenchtoast),
// so a server left on another map cannot be joined by them.  Bots do not count as players; a player who is still
// connecting does.  The agent runs the server, so it types `status` and `map` on the server's own console
// (stdin/stdout): no rcon password and no network involved.

// statusRow matches one player row of the engine's console `status` output: the slot, score, ping (a number, CON or ZMB),
// then the name (which may hold spaces), the address and the rate.
var statusRow = regexp.MustCompile(`^\s*\d+\s+-?\d+\s+(\d+|CON|ZMB)\s+`)

// isAddrField says whether a status field is the address column: "bot", or an IP (with or without port; the engine
// prefixes the colour code ^7), or "loopback".  The rate column is a plain integer and never matches.
func isAddrField(f string) bool {
	f = strings.TrimPrefix(f, "^7")
	if strings.EqualFold(f, "bot") || strings.EqualFold(f, "loopback") {
		return true
	}
	return strings.ContainsAny(f, ".:") && strings.ContainsAny(f, "0123456789")
}

// CountHumans reads the text a console `status` printed and counts the players that are not bots (their address is
// "bot").  The name can contain spaces, so the address is taken from the end of the row: ... address [rate].  A
// player who is still connecting has no rate yet, so the address is the last or the second to last field.  When
// no address can be found the row counts as a human: a wrong guess must never move a server out from under a player.
func CountHumans(status string) (humans, bots int) {
	for _, line := range strings.Split(status, "\n") {
		if !statusRow.MatchString(line) {
			continue
		}
		f := strings.Fields(line)
		addr := ""
		for i := len(f) - 1; i >= 0 && i >= len(f)-2 && i >= 4; i-- {
			if isAddrField(f[i]) {
				addr = strings.TrimPrefix(f[i], "^7")
				break
			}
		}
		if strings.EqualFold(addr, "bot") {
			bots++
		} else {
			humans++
		}
	}
	return humans, bots
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
// a while.  It talks to the server through its console (Supervisor.Console), so it only works when the agent runs
// the server; it returns at once when the feature is off or the agent does not run the server.
func (a *Agent) RunIdleMap(ctx context.Context) {
	c := a.cfg
	if c.IdleMap == "" {
		return
	}
	if a.sup == nil {
		log.Printf("idle map: AGENT_IDLE_MAP=%s needs the agent to run the server (AGENT_SUPERVISE is off); not watching", c.IdleMap)
		return
	}
	console := func(cmd string) (string, error) { return a.sup.Console(cmd, 10*time.Second) }
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
			} else if text, err := console("status"); err == nil {
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
		if _, err := console(cmd + c.IdleMap); err != nil {
			log.Printf("idle map: could not change the map: %v", err)
		}
	}
}
