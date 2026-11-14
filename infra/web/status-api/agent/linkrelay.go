package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LinkFile is where the game server writes the `/link CODE` requests of identified players, one per line:
//
//	link <player key> <CODE>
//	autolink <player key> <CODE> <signature>
//
// (g_auth.c, G_Link_Command).  The agent posts each to the site, signed with the server's key, and the site parks the
// key until the account holder confirms it.
const LinkFile = "link.log"

type linkRequest struct {
	ServerID  string `json:"server_id"`
	PublicKey string `json:"public_key"`
	Key       string `json:"key"`
	Code      string `json:"code"`
	Sent      int64  `json:"sent"`
	// the player joined while signed in on the site: the code was given to their game by the page, and Sig is the player's own signature
	// over it (made by their game client with their key; the site checks it, this server only passes it on)
	Auto bool   `json:"auto,omitempty"`
	Sig  string `json:"sig,omitempty"`
}

func isKeyChars(s string, extra string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(extra, c)) {
			return false
		}
	}
	return true
}

// parseLink reads one line of LinkFile; ok is false for anything that is not a well-formed request.  sig is empty for a typed
// code (`link`), and set for an automatic one (`autolink`).
func parseLink(line string) (key, code, sig string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 3 || len(f[1]) != 43 || len(f[2]) < 4 || len(f[2]) > 16 || !isKeyChars(f[1], "-_") || !isKeyChars(f[2], "-") {
		return "", "", "", false
	}
	switch {
	case f[0] == "link" && len(f) == 3:
		return f[1], f[2], "", true
	case f[0] == "autolink" && len(f) == 4 && len(f[3]) == 86 && isKeyChars(f[3], "-_"): // a 64-byte signature, URL-safe base64
		return f[1], f[2], f[3], true
	}
	return "", "", "", false
}

// postLink signs and sends one request.  The site answers 200 for a code it accepted.  A non-empty sig makes it an automatic link.
func postLink(ctx context.Context, client *http.Client, url string, id *Identity, key, code, sig string, now time.Time) error {
	body, _ := json.Marshal(linkRequest{ServerID: id.ID, PublicKey: id.PublicKeyString(), Key: key, Code: code, Sent: now.Unix(), Auto: sig != "", Sig: sig})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-S4nd-Signature", id.Sign(body))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	if resp.StatusCode != http.StatusOK {
		return &linkError{status: resp.StatusCode, body: strings.TrimSpace(string(msg))}
	}
	return nil
}

type linkError struct {
	status int
	body   string
}

func (e *linkError) Error() string { return http.StatusText(e.status) + ": " + e.body }

// RunLinkRelay follows LinkFile in dir and posts what is added to it, until ctx ends.  Requests that were in the file
// before the agent started are left alone (their codes have expired).
func RunLinkRelay(ctx context.Context, dir, url string, id *Identity, every time.Duration) {
	path := filepath.Join(dir, LinkFile)
	client := &http.Client{Timeout: 10 * time.Second}
	var offset int64
	if st, err := os.Stat(path); err == nil {
		offset = st.Size()
	}
	var partial string
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if st, err := f.Stat(); err == nil && st.Size() < offset {
			offset, partial = 0, "" // the file was replaced
		}
		f.Seek(offset, io.SeekStart)
		data, _ := io.ReadAll(io.LimitReader(f, 64<<10))
		f.Close()
		offset += int64(len(data))
		lines := strings.Split(partial+string(data), "\n")
		partial = lines[len(lines)-1] // a line still being written
		for _, line := range lines[:len(lines)-1] {
			key, code, sig, ok := parseLink(line)
			if !ok {
				continue
			}
			if err := postLink(ctx, client, url, id, key, code, sig, time.Now()); err != nil {
				log.Printf("link: key %.10s: %v", key, err)
			} else if sig != "" {
				log.Printf("link: key %.10s linked to the account it joined with", key)
			} else {
				log.Printf("link: key %.10s asked to link; waiting for the account holder to confirm", key)
			}
		}
	}
}

// PostLink sends one request with a default HTTP client (for tools and tests).
func PostLink(ctx context.Context, url string, id *Identity, key, code string, now time.Time) error {
	return postLink(ctx, &http.Client{Timeout: 10 * time.Second}, url, id, key, code, "", now)
}

// PostAutoLink sends an automatic link request: sig is the player's signature over link.LinkMessage(code, server id).
func PostAutoLink(ctx context.Context, url string, id *Identity, key, code, sig string, now time.Time) error {
	return postLink(ctx, &http.Client{Timeout: 10 * time.Second}, url, id, key, code, sig, now)
}
