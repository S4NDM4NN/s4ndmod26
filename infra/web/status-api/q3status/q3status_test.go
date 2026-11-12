package q3status

import (
	"net"
	"testing"
	"time"
)

const packet = "\xff\xff\xff\xffstatusResponse\n\\sv_hostname\\^8S^14^8NDMoD^126\\mapname\\mp_beach\\sv_maxclients\\20\n" +
	"5 0 \"[BOT]^1Vic\"\n3 48 \"player two\"\n"

func TestParse(t *testing.T) {
	s := Parse([]byte(packet))
	if !s.Online || s.Hostname != "S4NDMoD26" || s.Map != "mp_beach" || s.MaxPlayers != 20 || s.Players != 2 {
		t.Fatalf("status %+v", s)
	}
	if p := s.PlayerList[0]; p.Score != 5 || p.Ping != 0 || p.Name != "[BOT]Vic" {
		t.Errorf("player %+v", p)
	}
	if p := s.PlayerList[1]; p.Ping != 48 || p.Name != "player two" {
		t.Errorf("player %+v", p)
	}
	if Parse([]byte("garbage")).Online {
		t.Error("garbage parsed as online")
	}
}

func TestQuery(t *testing.T) {
	srv, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go func() {
		buf := make([]byte, 1500)
		n, from, err := srv.ReadFromUDP(buf)
		if err == nil && string(buf[:n]) == "\xff\xff\xff\xffgetstatus\n" {
			srv.WriteToUDP([]byte(packet), from)
		}
	}()
	if s := Query(srv.LocalAddr().String(), 2*time.Second); !s.Online || s.Map != "mp_beach" {
		t.Fatalf("status %+v", s)
	}
	srv.Close()
	if s := Query(srv.LocalAddr().String(), 200*time.Millisecond); s.Online {
		t.Error("a closed server answered")
	}
}
