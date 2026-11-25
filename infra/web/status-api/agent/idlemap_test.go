package agent

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const statusText = `map: mp_base
cl score ping name            address                                 rate 
-- ----- ---- --------------- --------------------------------------- -----
  0     5    0 ^7Bot One         ^7bot                                    25000
 1    12   31 ^7Some Human      ^7203.0.113.5:27960                      25000
 2     0  CON ^7Joiner          ^7198.51.100.7:51234                    25000
`

func TestCountHumans(t *testing.T) {
	h, b := CountHumans(statusText)
	if h != 2 || b != 1 {
		t.Fatalf("humans %d bots %d, want 2 and 1", h, b)
	}
	if h, b := CountHumans("map: mp_beach\ncl score ping name address rate\n-- ----- ---- ---\n"); h != 0 || b != 0 {
		t.Fatalf("empty server: %d %d", h, b)
	}
	// a player still connecting has no rate column yet (real iowolfded output)
	connecting := " 0     0  999 Silent          ^7127.0.0.1                              \n"
	if h, b := CountHumans(connecting); h != 1 || b != 0 {
		t.Fatalf("connecting human: %d %d", h, b)
	}
	if h, b := CountHumans(" 1     0  999 Some Name       ^7203.0.113.9:27960                  25000\n"); h != 1 || b != 0 {
		t.Fatalf("human with port: %d %d", h, b)
	}
	only := " 0     5    0 ^7A B C        ^7bot                                    25000\n"
	if h, b := CountHumans(only); h != 0 || b != 1 {
		t.Fatalf("bots only: %d %d", h, b)
	}
}

func TestIdleMapDecide(t *testing.T) {
	var s IdleMapState
	t0 := time.Unix(1000, 0)
	after := 10 * time.Minute
	if s.Decide(t0, true, false, 0, after) {
		t.Fatal("changed the map at once")
	}
	if s.Decide(t0.Add(9*time.Minute), true, false, 0, after) {
		t.Fatal("changed before the time was up")
	}
	// a human arrives: the clock starts over
	if s.Decide(t0.Add(9*time.Minute+30*time.Second), true, false, 1, after) {
		t.Fatal("changed with a human on")
	}
	if s.Decide(t0.Add(11*time.Minute), true, false, 0, after) {
		t.Fatal("did not start over")
	}
	if !s.Decide(t0.Add(21*time.Minute+time.Second), true, false, 0, after) {
		t.Fatal("did not change after 10 quiet minutes")
	}
	// unknown count, already on the idle map, or server down: never
	for _, c := range []struct {
		online, idle bool
		humans       int
	}{{true, false, -1}, {true, true, 0}, {false, false, 0}} {
		var q IdleMapState
		q.Decide(t0, c.online, c.idle, c.humans, after)
		if q.Decide(t0.Add(time.Hour), c.online, c.idle, c.humans, after) {
			t.Fatalf("changed in state %+v", c)
		}
	}
}

// A fake game server: answers getstatus, rcon status and records rcon commands.
func TestRunIdleMapEndToEnd(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	var mu sync.Mutex
	var cmds []string
	humans := false
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			msg := string(buf[4:n])
			mu.Lock()
			switch {
			case strings.HasPrefix(msg, "getstatus"):
				// one bot, and one human when there is one
				resp := "\xff\xff\xff\xffstatusResponse\n\\mapname\\mp_base\\sv_maxclients\\16\n0 0 \"Bot\"\n"
				if humans {
					resp += "5 40 \"Human\"\n"
				}
				conn.WriteToUDP([]byte(resp), from)
			case strings.HasPrefix(msg, "rcon wrong "):
				// no answer: a wrong password is silent
			case strings.HasPrefix(msg, "rcon pw status"):
				text := "map: mp_base\ncl score ping name address rate\n-- ----- ---- ----\n" +
					" 0     0    0 ^7Bot             ^7bot                                    25000\n"
				if humans {
					text += " 1     5   40 ^7Human           ^7203.0.113.5:27960                      25000\n"
				}
				conn.WriteToUDP([]byte("\xff\xff\xff\xffprint\n"+text), from)
			case strings.HasPrefix(msg, "rcon pw map "):
				cmds = append(cmds, strings.TrimPrefix(msg, "rcon pw "))
				conn.WriteToUDP([]byte("\xff\xff\xff\xffprint\nok\n"), from)
			}
			mu.Unlock()
		}
	}()

	cfg := Config{GamePort: port, RconPassword: "pw", IdleMap: "mp_beach", IdleMinutes: 1}
	a := New(cfg, nil, nil)
	a.pollEvery, a.queryAfter, a.idleEvery, a.idleAfter = 20*time.Millisecond, 200*time.Millisecond, 20*time.Millisecond, 400*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunStatus(ctx)
	go a.RunIdleMap(ctx)

	// with a human on, nothing may happen however long it takes (the wait is the 1 minute, scaled down below)
	mu.Lock()
	humans = true
	mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if len(cmds) != 0 {
		t.Fatalf("changed the map with a human on: %v", cmds)
	}
	mu.Unlock()

	// the human leaves (the bot stays): after the quiet time the agent sends the map command, exactly once
	mu.Lock()
	humans = false
	mu.Unlock()
	time.Sleep(2500 * time.Millisecond)
	mu.Lock()
	if len(cmds) < 1 || cmds[0] != "map mp_beach" {
		t.Fatalf("expected 'map mp_beach' after the server emptied, got %v", cmds)
	}
	mu.Unlock()

	// the rcon helper itself: right password answers, wrong one reports no answer
	if out, err := rcon(cfg.GameAddr(), "pw", "status", 50*time.Millisecond); err != nil || !strings.Contains(out, "Bot") {
		t.Fatalf("rcon status: %q %v", out, err)
	}
	if _, err := rcon(cfg.GameAddr(), "wrong", "status", 50*time.Millisecond); err == nil {
		t.Fatal("a wrong password should give an error")
	}
}

// Console talks to a process through stdin/stdout: a fake "server" answers `status` and `echo`.
func TestSupervisorConsole(t *testing.T) {
	script := `while IFS= read -r l; do case "$l" in "echo "*) echo "${l#echo }";; status) printf 'noise before\nmap: x\n 0 0 999 Silent ^7127.0.0.1\n';; map*) echo "loading $l";; esac; done`
	s := &Supervisor{Path: "/bin/sh", Args: []string{"-c", script}, MinBackoff: time.Hour, MaxBackoff: time.Hour, StableAfter: time.Hour, StopTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	var out string
	var err error
	for time.Now().Before(deadline) {
		if out, err = s.Console("status", 2*time.Second); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if h, b := CountHumans(out); h != 1 || b != 0 || strings.Contains(out, "s4nd-console") {
		t.Fatalf("console output %q -> %d humans %d bots", out, h, b)
	}
	if out, err = s.Console("map mp_beach", 2*time.Second); err != nil || !strings.Contains(out, "loading map mp_beach") {
		t.Fatalf("map: %q %v", out, err)
	}
}

// With no server process there is nothing to talk to.
func TestSupervisorConsoleNotRunning(t *testing.T) {
	s := &Supervisor{}
	if _, err := s.Console("status", 100*time.Millisecond); err == nil {
		t.Fatal("expected an error")
	}
}
