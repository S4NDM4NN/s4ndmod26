package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Bridge carries browser players to the game server: each WebSocket gets its own UDP socket, so the server sees
// every browser player as a separate client, and every binary message is one UDP packet in each direction.  It
// replaces wasm/ws_udp_bridge.py (the websockify pod), and speaks the same protocol: the first message to the
// browser is "\xff\xff\xff\xffport" plus the game port (two bytes, big-endian), which the engine uses to name
// the server it is talking to; the browser may send the same frame back, and it is not forwarded.
type Bridge struct {
	Game        *net.UDPAddr
	AllowOrigin string // "*" accepts a page from any site (the bridge has no cookies or other credentials)
	MaxConns    int
	IdleTimeout time.Duration // a player who sends nothing for this long is dropped

	conns atomic.Int64
}

const maxPacket = 65535

func portFrame(port int) []byte {
	return append([]byte("\xff\xff\xff\xffport"), byte(port>>8), byte(port))
}

func isPortFrame(b []byte) bool {
	return len(b) == 10 && bytes.HasPrefix(b, []byte("\xff\xff\xff\xffport"))
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.MaxConns > 0 && b.conns.Load() >= int64(b.MaxConns) {
		http.Error(w, "server is full", http.StatusServiceUnavailable)
		return
	}
	opts := &websocket.AcceptOptions{Subprotocols: []string{"binary"}}
	if b.AllowOrigin == "*" || b.AllowOrigin == "" {
		opts.InsecureSkipVerify = true
	} else if u, err := url.Parse(b.AllowOrigin); err == nil && u.Host != "" {
		opts.OriginPatterns = []string{u.Host}
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return // Accept has answered the request
	}
	b.conns.Add(1)
	defer b.conns.Add(-1)
	ws.SetReadLimit(maxPacket)
	defer ws.CloseNow()

	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		log.Printf("bridge: %v", err)
		ws.Close(websocket.StatusInternalError, "no socket")
		return
	}
	defer udp.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, portFrame(b.Game.Port)); err != nil {
		return
	}

	// game -> browser
	go func() {
		defer cancel()
		buf := make([]byte, maxPacket)
		for {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if from.Port != b.Game.Port {
				continue // only the game server's answers
			}
			if err := ws.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				return
			}
		}
	}()

	// browser -> game
	for {
		readCtx, done := context.WithTimeout(ctx, b.idle())
		_, msg, err := ws.Read(readCtx)
		done()
		if err != nil {
			var ce websocket.CloseError
			if !errors.As(err, &ce) && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
				log.Printf("bridge: %s: %v", r.RemoteAddr, err)
			}
			return
		}
		if isPortFrame(msg) {
			continue
		}
		if _, err := udp.WriteToUDP(msg, b.Game); err != nil {
			return
		}
	}
}

func (b *Bridge) idle() time.Duration {
	if b.IdleTimeout > 0 {
		return b.IdleTimeout
	}
	return 2 * time.Minute
}

// Conns is the number of browser players connected now.
func (b *Bridge) Conns() int { return int(b.conns.Load()) }
