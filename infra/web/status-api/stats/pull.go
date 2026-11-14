package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"s4ndmod26/status-api/probe"
	"s4ndmod26/status-api/replay"
)

// Pulling stats from a server's agent (GET /v1/stats, /v1/replays): the site reads the new bytes of each stats file into Postgres,
// tagged with the server's id, and derives each finished game's event feed from its replay.  The replay itself is only downloaded to a
// temporary folder for that and deleted again: a server keeps and serves its own replays.

const (
	maxStatsChunk  = 16 << 20  // bytes of one stats file read per pass (the rest comes next pass)
	maxReplayBytes = 128 << 20 // a replay bigger than this is not analysed
	maxFiles       = 2000      // files looked at per listing
	feedsPerPass   = 5         // replays downloaded for analysis per server per pass
)

// Remote is a server to pull from.
type Remote struct {
	ServerID string
	BaseURL  string // https://host[:port], where its agent answers
	Private  bool   // may be reached on a private address (our own servers); false for community servers
	Pool     string // the pool its games count in (PoolOfficial or PoolCommunity)
}

func (r Remote) pool() string {
	if r.Pool == "" {
		return PoolCommunity
	}
	return r.Pool
}

// Puller pulls stats from remote servers into a Store.
type Puller struct {
	Store *Store
	// Public refuses private and loopback addresses (a community server could otherwise point us at our own network).
	Public  *http.Client
	Private *http.Client

	mu   sync.Mutex
	seen map[string]int64 // server id + "/" + name -> size already handled
}

// NewPuller makes a Puller with the two clients.  rootCAs is for local testing only (nil uses the system's).
func NewPuller(s *Store) *Puller {
	return &Puller{Store: s, Public: newClient(true), Private: newClient(false), seen: map[string]int64{}}
}

func newClient(publicOnly bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if publicOnly {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !probe.IsPublic(ip) {
				return fmt.Errorf("%s is not a public address", host)
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:   2 * time.Minute,
		Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxIdleConnsPerHost: 2},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

type fileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Live bool   `json:"live"`
}

func (p *Puller) client(r Remote) *http.Client {
	if r.Private {
		return p.Private
	}
	return p.Public
}

func (p *Puller) list(ctx context.Context, r Remote, kind string) ([]fileInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(r.BaseURL, "/")+"/v1/"+kind, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client(r).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", kind, resp.Status)
	}
	var out []fileInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", kind, err)
	}
	if len(out) > maxFiles {
		out = out[len(out)-maxFiles:] // the newest
	}
	return out, nil
}

// fetch reads a file from pos on (at most max bytes).
func (p *Puller) fetch(ctx context.Context, r Remote, kind, name string, pos, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(r.BaseURL, "/")+"/v1/"+kind+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	if pos > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", pos))
	}
	resp, err := p.client(r).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent, resp.StatusCode == http.StatusOK && pos == 0:
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return nil, nil
	default:
		return nil, fmt.Errorf("%s/%s: %s", kind, name, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// Pull reads whatever is new from one server.  Returns the number of stats lines and feed events stored.
func (p *Puller) Pull(ctx context.Context, r Remote) (lines, events int, err error) {
	files, err := p.list(ctx, r, "stats")
	if err != nil {
		return 0, 0, err
	}
	for _, fi := range files {
		if !statsFile.MatchString(fi.Name) {
			continue
		}
		key := r.ServerID + "/" + fi.Name
		p.mu.Lock()
		same := p.seen[key] == fi.Size
		p.mu.Unlock()
		if same {
			continue
		}
		fi := fi
		n, err := p.Store.Ingest(ctx, r.ServerID, r.pool(), fi.Name, Source{Size: fi.Size, Read: func(pos int64) ([]byte, error) {
			return p.fetch(ctx, r, "stats", fi.Name, pos, maxStatsChunk)
		}})
		if err != nil {
			log.Printf("stats: %s from %s: %v", fi.Name, r.ServerID, err)
			continue
		}
		lines += n
		if fi.Size <= maxStatsChunk { // a bigger file is read in several passes
			p.mu.Lock()
			p.seen[key] = fi.Size
			p.mu.Unlock()
		}
	}
	events, err = p.pullFeeds(ctx, r)
	return lines, events, err
}

var (
	statsFile = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,60}\.jsonl$`)
	replayRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,59}$`)
)

// pullFeeds derives the event feed of finished games (a .rpl with its .txt) that have stats but no feed yet.  The replay is
// downloaded to a temporary folder, analysed and deleted.
func (p *Puller) pullFeeds(ctx context.Context, r Remote) (int, error) {
	reps, err := p.list(ctx, r, "replays")
	if err != nil {
		return 0, err
	}
	have := map[string]int64{}
	for _, f := range reps {
		have[f.Name] = f.Size
	}
	if err := p.indexReplays(ctx, r, reps, have); err != nil {
		log.Printf("stats: replay index of %s: %v", r.ServerID, err)
	}
	total, tried := 0, 0
	for i := len(reps) - 1; i >= 0; i-- { // newest first: a player wants the game they just finished
		f := reps[i]
		match, ok := strings.CutSuffix(f.Name, ".rpl")
		if !ok || f.Live || !replayRe.MatchString(match) {
			continue
		}
		if _, ok := have[match+".txt"]; !ok {
			continue
		}
		if f.Size <= 0 || f.Size > maxReplayBytes {
			continue
		}
		if has, err := p.Store.HasGame(ctx, r.ServerID, match); err != nil || !has {
			continue
		}
		if done, err := p.Store.FeedDone(ctx, r.ServerID, match); err != nil || done {
			continue
		}
		if tried >= feedsPerPass {
			break
		}
		tried++
		n, err := p.Store.IngestFeed(ctx, r.ServerID, match, f.Size, func() ([]FeedEvent, error) {
			return p.analyse(ctx, r, match)
		})
		if err != nil {
			log.Printf("stats: feed of %s from %s: %v", match, r.ServerID, err)
			continue
		}
		total += n
	}
	return total, nil
}

func (p *Puller) analyse(ctx context.Context, r Remote, match string) ([]FeedEvent, error) {
	dir, err := os.MkdirTemp("", "pull-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for _, ext := range []string{".rpl", ".txt"} {
		data, err := p.fetch(ctx, r, "replays", match+ext, 0, maxReplayBytes+1)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxReplayBytes {
			return nil, errors.New("replay too big")
		}
		if err := os.WriteFile(filepath.Join(dir, match+ext), data, 0o600); err != nil {
			return nil, err
		}
	}
	rp, err := replay.Parse(filepath.Join(dir, match+".rpl"))
	if err != nil {
		return nil, err
	}
	return FeedFromAnalysis(replay.Analyze(rp, filepath.Join(dir, match+".txt"))), nil
}

// PullAll pulls from each remote, a few at a time.
func (p *Puller) PullAll(ctx context.Context, remotes []Remote) {
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, r := range remotes {
		wg.Add(1)
		sem <- struct{}{}
		go func(r Remote) {
			defer wg.Done()
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			lines, events, err := p.Pull(c, r)
			if err != nil {
				log.Printf("stats: pulling from %s: %v", r.ServerID, err)
			} else if lines > 0 || events > 0 {
				log.Printf("stats: pulled %d line(s) and %d feed event(s) from %s", lines, events, r.ServerID)
			}
		}(r)
	}
	wg.Wait()
}

// Forget drops what the puller remembers about a server (after a purge, so it reads everything again if it is turned back on).
func (p *Puller) Forget(serverID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.seen {
		if strings.HasPrefix(k, serverID+"/") {
			delete(p.seen, k)
		}
	}
}

// RunPull pulls from the servers `remotes` returns, every interval, until ctx ends.
func (p *Puller) RunPull(ctx context.Context, remotes func(context.Context) ([]Remote, error), every time.Duration) {
	for {
		if list, err := remotes(ctx); err != nil {
			log.Printf("stats: listing servers to pull from: %v", err)
		} else {
			p.PullAll(ctx, list)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// indexReplays keeps the site's index of a server's replays up to date from its listing: a summary is read when it is new or its size
// changed, whether the raw replay is still there is refreshed, and replays the server has pruned are dropped.
func (p *Puller) indexReplays(ctx context.Context, r Remote, reps []fileInfo, have map[string]int64) error {
	base := strings.TrimRight(r.BaseURL, "/") + "/v1/replays"
	state, err := p.Store.replayIndexState(ctx, r.ServerID)
	if err != nil {
		return err
	}
	listed := map[string]bool{}
	for _, f := range reps {
		name, ok := strings.CutSuffix(f.Name, ".summary.json")
		if !ok || !replayRe.MatchString(name) || f.Size <= 0 || f.Size > maxSummaryBytes {
			continue
		}
		listed[name] = true
		_, hasRPL := have[name+".rpl"]
		_, hasTxt := have[name+".txt"]
		hasRPL = hasRPL && hasTxt
		old, known := state[name]
		if known && old.SumSize == f.Size && old.HasRPL == hasRPL && old.BaseURL == base {
			continue
		}
		data, err := p.fetch(ctx, r, "replays", f.Name, 0, maxSummaryBytes)
		if err != nil {
			log.Printf("stats: summary %s of %s: %v", f.Name, r.ServerID, err)
			continue
		}
		var sm replay.Summary
		if err := json.Unmarshal(data, &sm); err != nil {
			log.Printf("stats: summary %s of %s: %v", f.Name, r.ServerID, err)
			continue
		}
		if err := p.Store.putReplay(ctx, r.ServerID, name, sm, f.Size, hasRPL, base); err != nil {
			return err
		}
	}
	if len(reps) < maxFiles { // a cut-off listing does not say what is gone
		for name := range state {
			if !listed[name] {
				if _, err := p.Store.pool.Exec(ctx, `DELETE FROM replay_index WHERE server_id = $1 AND name = $2`, r.ServerID, name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
