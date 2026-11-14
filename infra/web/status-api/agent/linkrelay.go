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
}

// parseLink reads one line of LinkFile; ok is false for anything that is not a well-formed request.
func parseLink(line string) (key, code string, ok bool) {
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "link" || len(f[1]) != 43 || len(f[2]) < 4 || len(f[2]) > 16 {
		return "", "", false
	}
	for _, c := range f[1] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", "", false
		}
	}
	for _, c := range f[2] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return "", "", false
		}
	}
	return f[1], f[2], true
}

// postLink signs and sends one request.  The site answers 200 for a code it accepted.
func postLink(ctx context.Context, client *http.Client, url string, id *Identity, key, code string, now time.Time) error {
	body, _ := json.Marshal(linkRequest{ServerID: id.ID, PublicKey: id.PublicKeyString(), Key: key, Code: code, Sent: now.Unix()})
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
			key, code, ok := parseLink(line)
			if !ok {
				continue
			}
			if err := postLink(ctx, client, url, id, key, code, time.Now()); err != nil {
				log.Printf("link: key %.10s: %v", key, err)
			} else {
				log.Printf("link: key %.10s asked to link; waiting for the account holder to confirm", key)
			}
		}
	}
}
