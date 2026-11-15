package probe

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"s4ndmod26/status-api/agent"
)

func TestIsPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "93.184.216.34": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "255.255.255.255": false, "::1": false, "fe80::1": false,
		"fd00::1": false, "::ffff:10.0.0.1": false, "203.0.113.7": false, "2001:db8::1": false, "198.18.0.1": false,
	} {
		if got := IsPublic(net.ParseIP(ip)); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestRefusesInternalTargets(t *testing.T) {
	var p Prober
	ctx := context.Background()
	for _, target := range []string{"127.0.0.1:27960", "10.0.0.5:27960", "169.254.169.254:27960", "[::1]:27960", "localhost:27960"} {
		if r := p.UDP(ctx, target); r.OK || r.Error == "" {
			t.Errorf("UDP %s: %+v", target, r)
		}
	}
	for _, target := range []string{"wss://127.0.0.1:8443/ws", "wss://10.0.0.5/ws", "wss://localhost/ws", "wss://169.254.169.254/ws"} {
		if r := p.WSS(ctx, target); r.OK || !strings.Contains(r.Error, "public internet") && !strings.Contains(r.Error, "resolve") {
			t.Errorf("WSS %s: %+v", target, r)
		}
	}
	// ports: only 443 and 1024+, so a status probe cannot be used to poke at ssh, smtp, and the like
	for _, target := range []string{"8.8.8.8:22", "8.8.8.8:25", "8.8.8.8:80", "8.8.8.8:0", "8.8.8.8:99999", "8.8.8.8"} {
		if r := p.UDP(ctx, target); r.OK || r.Error == "" {
			t.Errorf("UDP %s: %+v", target, r)
		}
	}
	if r := p.WSS(ctx, "ws://8.8.8.8/ws"); r.OK || !strings.Contains(r.Error, "wss://") {
		t.Errorf("plain ws: %+v", r)
	}
	if r := p.WSS(ctx, "wss://user:pw@8.8.8.8/ws"); r.OK {
		t.Errorf("credentials in the address: %+v", r)
	}
}

// fakeGame answers getstatus on a UDP port, as the game server does.
func fakeGame(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if strings.HasPrefix(string(buf[:n]), "\xff\xff\xff\xffgetstatus") {
				c.WriteToUDP([]byte("\xff\xff\xff\xffstatusResponse\n\\sv_hostname\\Test Server\\mapname\\mp_beach\\sv_maxclients\\20\n10 50 \"Ann\"\n"), from)
			}
		}
	}()
	return c
}

func TestUDP(t *testing.T) {
	game := fakeGame(t)
	p := Prober{AllowPrivate: true, Timeout: 2 * time.Second}
	r := p.UDP(context.Background(), game.LocalAddr().String())
	if !r.OK || r.Status.Map != "mp_beach" || r.Status.Players != 1 {
		t.Fatalf("a running server: %+v", r)
	}
	game.Close()
	p.Timeout = 300 * time.Millisecond
	if r := p.UDP(context.Background(), game.LocalAddr().String()); r.OK || !strings.Contains(r.Error, "UDP") {
		t.Errorf("a stopped server: %+v", r)
	}
}

// The whole browser path: TLS with a certificate, the agent's bridge, a status answer from the game.
func TestWSS(t *testing.T) {
	game := fakeGame(t)
	bridge := &agent.Bridge{Game: game.LocalAddr().(*net.UDPAddr), AllowOrigin: "*", MaxConns: 4}
	mux := http.NewServeMux()
	mux.Handle("/ws", bridge)
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	u := "wss" + strings.TrimPrefix(srv.URL, "https") + "/ws"

	good := Prober{AllowPrivate: true, RootCAs: pool, Timeout: 3 * time.Second}
	// the test certificate is for 127.0.0.1 / example.com; connect by the IP it covers
	r := good.WSS(context.Background(), u)
	if !r.OK || r.Status.Hostname != "Test Server" {
		t.Fatalf("a good server: %+v", r)
	}
	// a certificate the browser would not trust
	bad := Prober{AllowPrivate: true, Timeout: 3 * time.Second}
	if r := bad.WSS(context.Background(), u); r.OK || !strings.Contains(r.Error, "not trusted") {
		t.Errorf("untrusted certificate: %+v", r)
	}
	// the WebSocket opens but the game is not behind it
	game.Close()
	good.Timeout = 600 * time.Millisecond
	if r := good.WSS(context.Background(), u); r.OK || !strings.Contains(r.Error, "game server did not answer") {
		t.Errorf("no game behind the bridge: %+v", r)
	}
	// nothing listening on that TCP port
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	if r := good.WSS(context.Background(), "wss://127.0.0.1:"+port+"/ws"); r.OK || r.Error == "" {
		t.Errorf("closed port: %+v", r)
	}
}
