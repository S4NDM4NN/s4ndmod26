package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// registrationAddrs works out the two public addresses the site probes: the game's (host:port, UDP) and the
// browsers' WebSocket (wss://host[:port]/ws).  Both must be given, because a server others cannot reach is not
// registered.
func (c Config) registrationAddrs() (game, wss string, err error) {
	names := strings.Split(c.PublicNames, ",")
	game = strings.TrimSpace(names[0])
	if _, _, e := net.SplitHostPort(game); e != nil || game == "" {
		return "", "", fmt.Errorf("registering needs AGENT_PUBLIC_NAMES to start with the public game address as host:port (got %q)", c.PublicNames)
	}
	wss = c.PublicWSS
	if wss == "" {
		u, e := url.Parse(c.PublicURL)
		if e != nil || u.Scheme != "https" || u.Host == "" {
			return "", "", fmt.Errorf("registering needs AGENT_PUBLIC_URL as the https address browsers reach this agent at (or AGENT_PUBLIC_WSS)")
		}
		wss = "wss://" + u.Host + "/ws"
	}
	if !strings.HasPrefix(wss, "wss://") {
		return "", "", fmt.Errorf("the WebSocket address must start with wss:// (got %q)", wss)
	}
	return game, wss, nil
}

type claimBody struct {
	ServerID  string `json:"server_id"`
	PublicKey string `json:"public_key"`
	Token     string `json:"token,omitempty"`
	GameAddr  string `json:"game_addr"`
	WSSURL    string `json:"wss_url"`
	Pk3       string `json:"pk3,omitempty"`
	Sent      int64  `json:"sent"`
}

// claimAnswer is what the site says; the probe results are what a host needs to fix a problem.
type claimAnswer struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	UDP    struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"udp"`
	WSS struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"wss"`
}

// postClaim sends one signed claim.  A nil error means the site accepted it (status says pending or approved).
func postClaim(ctx context.Context, client *http.Client, siteURL string, id *Identity, token, game, wss, pk3 string, now time.Time) (claimAnswer, error) {
	body, _ := json.Marshal(claimBody{ServerID: id.ID, PublicKey: id.PublicKeyString(), Token: token, GameAddr: game, WSSURL: wss, Pk3: pk3, Sent: now.Unix()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteURL+"/api/servers/claim", bytes.NewReader(body))
	if err != nil {
		return claimAnswer{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-S4nd-Signature", id.Sign(body))
	resp, err := client.Do(req)
	if err != nil {
		return claimAnswer{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var ans claimAnswer
	json.Unmarshal(raw, &ans)
	if resp.StatusCode != http.StatusOK {
		msg := ans.Error
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		var parts []string
		if !ans.UDP.OK && ans.UDP.Error != "" {
			parts = append(parts, ans.UDP.Error)
		}
		if !ans.WSS.OK && ans.WSS.Error != "" {
			parts = append(parts, ans.WSS.Error)
		}
		if len(parts) > 0 {
			msg += ": " + strings.Join(parts, "; ")
		}
		return ans, fmt.Errorf("%s", msg)
	}
	return ans, nil
}

// RunRegistration keeps this server registered with the site until ctx ends: it sends a claim now, again after
// `retry` while the site refuses it (the log says why, so the host can fix a firewall or a certificate), and after
// `refresh` once accepted (which also tells the site about a new address).
func RunRegistration(ctx context.Context, cfg Config, id *Identity, pk3 func() string, retry, refresh time.Duration) {
	game, wss, err := cfg.registrationAddrs()
	if err != nil {
		log.Printf("registration: %v", err)
		return
	}
	client := &http.Client{Timeout: 30 * time.Second}
	last := ""
	for {
		wait := retry
		ans, err := postClaim(ctx, client, cfg.RegistrationURL, id, cfg.RegistrationToken, game, wss, pk3Name(pk3), time.Now())
		switch {
		case err != nil:
			log.Printf("registration: not accepted yet: %v (trying again in %s)", err, retry)
		default:
			wait = refresh
			if ans.Status != last {
				log.Printf("registration: the site accepted this server (%s); game %s, browsers %s", ans.Status, game, wss)
				if ans.Status == "pending" {
					log.Printf("registration: waiting for an admin to approve it")
				}
				last = ans.Status
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// pk3Name is the mod pk3 this server runs, or "" when it is not known (the site then cannot tell clients which
// version it is).
func pk3Name(f func() string) string {
	if f == nil {
		return ""
	}
	return f()
}
