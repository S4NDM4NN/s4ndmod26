// Package probe checks, from outside, that a game server others can join really is reachable: over UDP (what the
// desktop game uses) and over a secure WebSocket (what the browser client uses), the second with a certificate a
// browser accepts and with a status query carried through the agent's bridge to the game itself.
//
// The addresses come from hosts, so the prober is careful what it connects to: only public internet addresses
// (never loopback, private, link-local or other special ranges), checked after name lookup and then dialled by the
// checked IP, so a name that changes its answer between check and connection cannot aim it inward.
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"s4ndmod26/status-api/q3status"
)

// Result is what a probe found.
type Result struct {
	OK     bool
	Error  string // for the host to read: what failed and what to check
	Status q3status.Status
}

// Prober runs the checks.  The zero value is fit for use.
type Prober struct {
	Timeout      time.Duration  // per probe (8 s)
	AllowPrivate bool           // tests only: allow loopback and private addresses, and ws:// instead of wss://
	RootCAs      *x509.CertPool // tests only: the roots to trust (nil: the system's)
	Resolver     *net.Resolver  // nil: the default
}

func (p Prober) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 8 * time.Second
}

var specialNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "100::/64",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// IsPublic reports whether ip is an address on the public internet.
func IsPublic(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, n := range specialNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// ErrNotPublic is returned for a target that is not on the public internet.
var ErrNotPublic = errors.New("that address is not on the public internet")

// resolve returns the IP to connect to for host, refusing anything that is not public.  Every address the name has
// must be public (a name that also points inward is refused), and an IPv4 one is preferred.
func (p Prober) resolve(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !p.AllowPrivate && !IsPublic(ip) {
			return nil, ErrNotPublic
		}
		return ip, nil
	}
	if host == "" || strings.ContainsAny(host, " /\\@") {
		return nil, fmt.Errorf("%q is not a host name", host)
	}
	r := p.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ips, err := r.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("the name %s does not resolve (check its DNS record)", host)
	}
	var pick net.IP
	for _, a := range ips {
		if !p.AllowPrivate && !IsPublic(a.IP) {
			return nil, ErrNotPublic
		}
		if pick == nil || (pick.To4() == nil && a.IP.To4() != nil) {
			pick = a.IP
		}
	}
	return pick, nil
}

func okPort(port int) bool { return port == 443 || port >= 1024 && port <= 65535 }

func splitHostPort(addr string, def int) (string, int, error) {
	host, ps, err := net.SplitHostPort(addr)
	if err != nil {
		if def == 0 {
			return "", 0, fmt.Errorf("%q is not host:port", addr)
		}
		return addr, def, nil
	}
	port, err := strconv.Atoi(ps)
	if err != nil || !okPort(port) {
		return "", 0, fmt.Errorf("port %q is not allowed (use 443 or 1024-65535)", ps)
	}
	return host, port, nil
}

// UDP asks the game server at hostport (host:port) for its status over UDP.
func (p Prober) UDP(ctx context.Context, hostport string) Result {
	host, port, err := splitHostPort(hostport, 0)
	if err != nil {
		return Result{Error: err.Error()}
	}
	ip, err := p.resolve(ctx, host)
	if err != nil {
		return Result{Error: err.Error()}
	}
	st := q3status.Query(net.JoinHostPort(ip.String(), strconv.Itoa(port)), p.timeout())
	if !st.Online {
		return Result{Error: fmt.Sprintf("no answer to a status query on UDP %s:%d (is the game port forwarded and open for UDP, and the server running?)", host, port)}
	}
	return Result{OK: true, Status: st}
}

// WSS opens the secure WebSocket at rawurl (wss://host[:port]/path), checks its certificate the way a browser would,
// and sends a status query through it: the answer proves the bridge reaches the game server.
func (p Prober) WSS(ctx context.Context, rawurl string) Result {
	u, err := url.Parse(rawurl)
	if err != nil || u.Host == "" || u.User != nil {
		return Result{Error: fmt.Sprintf("%q is not a WebSocket address", rawurl)}
	}
	if u.Scheme != "wss" && !(p.AllowPrivate && u.Scheme == "ws") {
		return Result{Error: "browsers on an https page can only use wss:// (with a valid certificate)"}
	}
	def := 443
	if u.Scheme == "ws" {
		def = 80
	}
	host, port, err := splitHostPort(u.Host, def)
	if err != nil {
		return Result{Error: err.Error()}
	}
	ip, err := p.resolve(ctx, host)
	if err != nil {
		return Result{Error: err.Error()}
	}
	pinned := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	client := &http.Client{
		Transport: &http.Transport{
			// always the checked address, whatever the name says now
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: p.timeout()}).DialContext(ctx, network, pinned)
			},
			TLSClientConfig:       &tls.Config{ServerName: host, RootCAs: p.RootCAs, MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   p.timeout(),
			ResponseHeaderTimeout: p.timeout(),
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	c, _, err := websocket.Dial(ctx, rawurl, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"binary"}})
	if err != nil {
		return Result{Error: describeDialError(host, port, err)}
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 16)
	if err := c.Write(ctx, websocket.MessageBinary, []byte("\xff\xff\xff\xffgetstatus\n")); err != nil {
		return Result{Error: "the WebSocket opened but would not take a message: " + err.Error()}
	}
	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			return Result{Error: "the WebSocket opened, but the game server did not answer a status query through it (does the agent reach the game on its own UDP port?)"}
		}
		if st := q3status.Parse(msg); st.Online {
			return Result{OK: true, Status: st}
		}
	}
}

func describeDialError(host string, port int, err error) string {
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ci x509.CertificateInvalidError
	switch {
	case errors.As(err, &ua):
		return "the certificate of " + host + " is not trusted by browsers (a self-signed or incomplete chain)"
	case errors.As(err, &hn):
		return "the certificate does not cover the name " + host
	case errors.As(err, &ci):
		return "the certificate of " + host + " is not valid: " + ci.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("no answer from %s:%d over TCP in time (is the port open for TCP and the agent listening with TLS?)", host, port)
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Sprintf("could not open the WebSocket on %s:%d: %s", host, port, msg)
}
