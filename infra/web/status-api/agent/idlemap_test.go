package agent

import (
	"context"
	"net"
	"os"
	"strings"
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

// A fake game server for the status poll (UDP getstatus) and a fake server process for the console (stdin/stdout):
// with a human on, nothing may happen; after the human leaves the agent types `map mp_beach`, once.
func TestRunIdleMapEndToEnd(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port
	dir := t.TempDir()
	humanFlag, cmdLog := dir+"/human", dir+"/cmds"

	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n > 4 && strings.HasPrefix(string(buf[4:n]), "getstatus") {
				resp := "\xff\xff\xff\xffstatusResponse\n\\mapname\\mp_base\\sv_maxclients\\16\n0 0 \"Bot\"\n"
				if _, err := os.Stat(humanFlag); err == nil {
					resp += "5 40 \"Human\"\n"
				}
				conn.WriteToUDP([]byte(resp), from)
			}
		}
	}()

	script := `while IFS= read -r l; do case "$l" in
"echo "*) echo "${l#echo }";;
status) printf 'map: mp_base\n 0 0 0 ^7Bot ^7bot 25000\n'; [ -e "$H" ] && printf ' 1 5 40 ^7Human ^7203.0.113.5:27960 25000\n';;
map*) echo "$l" >> "$C";; esac; done`
	sup := &Supervisor{Path: "/bin/sh", Args: []string{"-c", script}, MinBackoff: time.Hour, MaxBackoff: time.Hour, StableAfter: time.Hour, StopTimeout: time.Second}
	os.Setenv("H", humanFlag)
	os.Setenv("C", cmdLog)
	defer os.Unsetenv("H")
	defer os.Unsetenv("C")

	cfg := Config{GamePort: port, IdleMap: "mp_beach", IdleMinutes: 1}
	a := New(cfg, nil, sup)
	a.pollEvery, a.queryAfter, a.idleEvery, a.idleAfter = 20*time.Millisecond, 200*time.Millisecond, 20*time.Millisecond, 400*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	go a.RunStatus(ctx)
	go a.RunIdleMap(ctx)

	read := func() string { b, _ := os.ReadFile(cmdLog); return string(b) }
	if err := os.WriteFile(humanFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if got := read(); got != "" {
		t.Fatalf("changed the map with a human on: %q", got)
	}
	os.Remove(humanFlag) // the human leaves, the bot stays
	time.Sleep(2500 * time.Millisecond)
	// (the fake server never really changes map, so the agent tries again after each quiet period: the lines repeat)
	got := read()
	if got == "" || strings.Trim(strings.ReplaceAll(got, "map mp_beach\n", ""), "\n") != "" {
		t.Fatalf("expected 'map mp_beach' after the server emptied, got %q", got)
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
