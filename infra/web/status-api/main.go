package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"s4ndmod26/status-api/accounts"
	"s4ndmod26/status-api/link"
	"s4ndmod26/status-api/q3status"
	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/replayviews"
	"s4ndmod26/status-api/servers"
	"s4ndmod26/status-api/stats"
	"s4ndmod26/status-api/ticket"
)

var (
	mu     sync.RWMutex
	cached q3status.Status // the game server's last answer to getstatus (see poll)
)

// gamesList is the games-list response, rebuilt in the background (see refreshGamesList) so a
// request only ever serves bytes that are already encoded.
type gamesList struct {
	mu   sync.RWMutex
	body []byte // JSON array, newest first
}

func (g *gamesList) set(b []byte) {
	g.mu.Lock()
	g.body = b
	g.mu.Unlock()
}

func (g *gamesList) get() []byte {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.body
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// refreshGamesList lists every analyzed game.  Summaries come from small per-game sidecars written
// by the scanner (replay.LoadSummary), and are kept in memory once read, so a refresh normally
// costs one directory read plus a couple of stats per game; only a game that has never been
// summarized needs its full analysis JSON parsed.
func refreshGamesList(dir, statsDir string, known map[string]replay.Summary, out *gamesList) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(entries))
	summaries := make([]replay.Summary, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".summary.json") {
			continue
		}
		base := strings.TrimSuffix(name, ".json")
		seen[base] = true
		s, ok := known[base]
		if !ok {
			if s, ok = replay.LoadSummary(dir, base); !ok {
				continue
			}
			known[base] = s
		}
		s.GametypeName = replay.GametypeName(s.Gametype) // files written before the names were fixed still carry the old one
		// The .rpl can be pruned long before the .json, so check it on every refresh.
		s.Has3D = fileExists(filepath.Join(dir, base+".rpl"))
		s.HasRPL = s.HasPOTG &&
			fileExists(filepath.Join(dir, base+".rpl")) &&
			fileExists(filepath.Join(dir, base+".txt"))
		s.HasStats = statsDir != "" && fileExists(filepath.Join(statsDir, base+".jsonl"))
		summaries = append(summaries, s)
	}
	for base := range known { // files pruned from disk
		if !seen[base] {
			delete(known, base)
		}
	}
	// Newest first.
	for i, j := 0, len(summaries)-1; i < j; i, j = i+1, j-1 {
		summaries[i], summaries[j] = summaries[j], summaries[i]
	}
	if other := otherServersReplays.Load(); other != nil && len(*other) > 0 {
		// replays other registered servers keep (the site holds only their index): mixed in by time
		summaries = append(summaries, *other...)
		sort.SliceStable(summaries, func(a, b int) bool { return listTime(summaries[a].Name).After(listTime(summaries[b].Name)) })
	}
	if body, err := json.Marshal(summaries); err == nil {
		out.set(body)
	}
}

// otherServersReplays is the index of replays kept by other registered servers (stats.Store.IndexedReplays), reloaded in the background.
var otherServersReplays atomic.Pointer[[]replay.Summary]

var listTimeRe = regexp.MustCompile(`replays_(\d{8}_\d{6})$`)

// listTime is when a game started, from its name (the replay's file name, with a server id in front for another server's).
func listTime(name string) time.Time {
	if m := listTimeRe.FindStringSubmatch(name); m != nil {
		if t, err := time.Parse("20060102_150405", m[1]); err == nil {
			return t
		}
	}
	return time.Time{}
}

// runOtherServersReplays keeps otherServersReplays fresh.
func runOtherServersReplays(ctx context.Context, s *stats.Store) {
	for {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		if list, err := s.IndexedReplays(c); err == nil {
			otherServersReplays.Store(&list)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// replaySourceHandler serves GET /api/replays/{name}/source: where the viewer fetches a replay's files (<name>.json, .rpl, .txt) from.
// A game of this site's own server is in the local folder; another server's is at its agent (or wherever it put it).
func replaySourceHandler(localBase string, s *stats.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if !matchNameRe.MatchString(name) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "bad replay name"})
			return
		}
		out := map[string]string{"base": localBase, "name": name, "games_api": "/api/games", "live_url": "/api/live/view"}
		if s != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if base, src, sid, ok, err := s.ReplaySource(ctx, name); err == nil && ok {
				root := strings.TrimSuffix(base, "/replays") // the agent's /v1: its views are built there from its own files
				out = map[string]string{"base": base, "name": src, "server_id": sid, "games_api": root + "/games", "live_url": root + "/live/view"}
			}
		}
		w.Header().Set("Cache-Control", "public, max-age=60")
		json.NewEncoder(w).Encode(out)
	}
}

// runGamesList keeps out fresh.  The first refresh may backfill sidecars for games analyzed before
// they existed, which is why this runs off the request path.
func runGamesList(dir, statsDir string, out *gamesList) {
	known := map[string]replay.Summary{}
	for {
		refreshGamesList(dir, statsDir, known, out)
		time.Sleep(15 * time.Second)
	}
}

// replayListHandler serves the games list, gzipped when the client accepts it.  The compressed form
// is cached per list body, so a request costs no compression until the list changes.
func replayListHandler(out *gamesList) http.HandlerFunc {
	var (
		mu     sync.Mutex
		rawFor []byte
		gz     []byte
	)
	return func(w http.ResponseWriter, r *http.Request) {
		body := out.get()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Vary", "Accept-Encoding")
		if body == nil {
			// Still building the first list (startup); say so rather than showing "no games".
			http.Error(w, "games list is warming up", http.StatusServiceUnavailable)
			return
		}
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			mu.Lock()
			if len(rawFor) == 0 || len(rawFor) != len(body) || &rawFor[0] != &body[0] {
				var buf bytes.Buffer
				zw := gzip.NewWriter(&buf)
				zw.Write(body)
				zw.Close()
				rawFor, gz = body, buf.Bytes()
			}
			z := gz
			mu.Unlock()
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(z)
			return
		}
		w.Write(body)
	}
}

func poll(addr string) {
	for {
		s := q3status.Query(addr, 3*time.Second)
		mu.Lock()
		cached = s
		mu.Unlock()
		time.Sleep(10 * time.Second)
	}
}

func liveStreamHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		tick := func() (done bool) {
			rplPath, baseName, found := replay.FindLiveReplay(dir)
			if !found {
				fmt.Fprintf(w, "event: no_game\ndata: {}\n\n")
				flusher.Flush()
				return false
			}

			rep, err := replay.ParseLive(rplPath)
			if err == nil && len(rep.Frames) > 0 {
				analysis := replay.AnalyzeLive(rep)
				if data, err := json.Marshal(analysis); err == nil {
					fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
					flusher.Flush()
				}
			}

			// Send match_over once the .txt appears (written at intermission).
			if _, err := os.Stat(filepath.Join(dir, baseName+".txt")); err == nil {
				fmt.Fprintf(w, "event: match_over\ndata: {\"name\":%q}\n\n", baseName)
				flusher.Flush()
				return true
			}
			return false
		}

		if tick() {
			return
		}

		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if tick() {
					return
				}
			}
		}
	}
}

func main() {
	host := os.Getenv("RTCW_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("RTCW_PORT")
	if port == "" {
		port = "27960"
	}
	apiPort := os.Getenv("STATUS_API_PORT")
	if apiPort == "" {
		apiPort = "8080"
	}

	replayDir := os.Getenv("REPLAY_DIR")
	if replayDir == "" {
		replayDir = "/usr/share/nginx/html/downloads/s4ndmod26/replays"
	}
	replayCfg := replay.RetentionConfig{MaxAgeDays: 10, RplMaxAgeDays: -1, BotOnlyMaxAgeDays: -1}
	// games no human played in (off unless set; dev uses 1)
	if v := os.Getenv("REPLAY_BOT_ONLY_MAX_AGE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			replayCfg.BotOnlyMaxAgeDays = n
		}
	}
	if v := os.Getenv("REPLAY_MAX_AGE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			replayCfg.MaxAgeDays = n
		}
	}
	if v := os.Getenv("REPLAY_RPL_MAX_AGE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			replayCfg.RplMaxAgeDays = n
		}
	}
	go replay.RunScanner(replayDir, replayCfg)

	addr := net.JoinHostPort(host, port)
	log.Printf("polling %s every 10s", addr)
	go poll(addr)

	// Where the game's pk3s are served from (mod directory first); levelshots are looked up in them.
	pk3Dirs := []string{"/usr/share/nginx/html/downloads/s4ndmod26", "/usr/share/nginx/html/downloads/main"}
	if v := os.Getenv("PK3_DIRS"); v != "" {
		pk3Dirs = filepath.SplitList(v)
	}
	shots := &levelshotStore{dirs: pk3Dirs}
	http.HandleFunc("/api/levelshot/", levelshotHandler(shots))
	http.HandleFunc("GET /api/maps/{map}/mesh", mapMeshHandler(&mapMeshStore{shots: shots}))

	// Update manifest for the desktop launcher (see manifest.go); hashes what is served on disk.
	webRoot := os.Getenv("WEB_ROOT")
	if webRoot == "" {
		webRoot = "/usr/share/nginx/html"
	}
	http.HandleFunc("/api/manifest", manifestHandler(newManifestBuilder(webRoot)))

	statsDir := os.Getenv("STATS_DIR")
	if statsDir == "" {
		statsDir = "/usr/share/nginx/html/downloads/s4ndmod26/stats"
	}
	gameStats := startGameStats(statsDir, replayDir)
	http.HandleFunc("GET /api/games/{match}/stats", gameStatsHandler(gameStats))
	http.HandleFunc("GET /api/games/{match}/feed", gameFeedHandler(gameStats))

	http.HandleFunc("GET /api/games/{match}/paths", replayviews.PathsHandler(replayDir))
	http.HandleFunc("GET /api/games/{match}/fights", replayviews.FightsHandler(replayDir))
	http.HandleFunc("GET /api/games/{match}/fights/{n}/view", replayviews.FightViewHandler(replayDir))

	games := &gamesList{}
	listStatsDir := statsDir
	if gameStats == nil {
		listStatsDir = "" // no database: do not offer stats links
	}
	go runGamesList(replayDir, listStatsDir, games)
	http.HandleFunc("/api/replays", replayListHandler(games))
	http.HandleFunc("GET /api/replays/{name}/source", replaySourceHandler("/downloads/s4ndmod26/replays", statsStore))
	if statsStore != nil {
		go runOtherServersReplays(context.Background(), statsStore)
	}
	http.HandleFunc("/api/live/stream", liveStreamHandler(replayDir))
	http.HandleFunc("GET /api/live/view", replayviews.LiveViewHandler(replayDir, replayviews.LiveViewDelay(os.Getenv("LIVE_VIEW_DELAY_SECONDS"))))

	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		s := cached
		mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(s)
	})

	// join tickets (package ticket): off unless TICKET_SIGNING_KEY, TICKET_SERVERS and, until there is a login,
	// TEST_ACCOUNT_TOKEN are all set
	//
	// accounts (package accounts): ACCOUNTS_ENABLED=1 and DATABASE_URL.  When on, they are the login for tickets and
	// links in place of the test token.
	var login ticket.Authenticator
	var registry ticket.Directory
	acct := startAccounts()
	if acct != nil {
		acct.Mount(http.DefaultServeMux)
		login = accounts.Auth{S: acct.S}
		demoAccounts = acct.S
		if reg := startServers(acct.S); reg != nil {
			registry = reg
		}
	}
	// Linking a player's key to an account (package link): with accounts and the registry, the links live in the accounts' database (so
	// deleting an account unlinks its keys) and need nothing else; the older JSON-file store is only for a site running join tickets
	// without accounts.
	linked := false
	var linkStore link.Backend
	if acct != nil && registry != nil {
		pg := link.NewPG(acct.S.Pool())
		mctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := pg.Migrate(mctx); err != nil {
			log.Printf("link: %v; linking keys to accounts is off", err)
		} else {
			(&link.Handler{Store: pg, Auth: login, Servers: registry}).Mount(http.DefaultServeMux)
			linked = true
			linkStore = pg
			log.Printf("link: player keys can be linked to accounts")
		}
		cancel()
	}
	// a player's page: by key, or by account username when keys are linked to accounts
	if statsStore != nil {
		var dir playerDirectory
		if acct != nil && linkStore != nil {
			dir = accountPlayers{acc: acct.S, links: linkStore}
		}
		playersAPI(statsStore, dir, http.DefaultServeMux)
	}
	if tickets := ticket.FromEnvWith(login, registry); tickets != nil {
		http.Handle("POST /api/tickets", tickets)
		http.HandleFunc("GET /api/tickets/key", tickets.KeyHandler)
		if !linked {
			// /link CODE (package link): same login and server list as the tickets
			link.FromTickets(tickets).Mount(http.DefaultServeMux)
		}
	}

	if demos := startDemoMatches(demoConfigFromEnv()); demos != nil && acct != nil {
		// an account can redeem an old uploader key for the demo_upload permission and take over its uploads
		acct.DemoKeyLabel = func(secret string) (string, bool) { return demos.keys.Lookup(secret) }
		acct.AdoptDemos = demos.db.AdoptOwner
		acct.PurgeDemos = demos.purgeOwner
	}

	log.Printf("listening on :%s", apiPort)
	log.Fatal(http.ListenAndServe(":"+apiPort, nil))
}

// startAccounts opens the account store when ACCOUNTS_ENABLED=1 (it needs DATABASE_URL), or returns nil.
func startAccounts() *accounts.Handler {
	if os.Getenv("ACCOUNTS_ENABLED") != "1" {
		return nil
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Printf("accounts: ACCOUNTS_ENABLED is set but DATABASE_URL is not; accounts are off")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("accounts: %v; accounts are off", err)
		return nil
	}
	s := accounts.New(pool)
	// ACCOUNTS_PUBLIC_URL is the site's address in the mailed links.  Mail is only logged until a real sender is
	// added; ACCOUNTS_MAIL_OUTBOX also writes each mail to a folder (development).
	s.PublicURL = os.Getenv("ACCOUNTS_PUBLIC_URL")
	if dir := os.Getenv("ACCOUNTS_MAIL_OUTBOX"); dir != "" {
		s.Mail = &accounts.OutboxMailer{Dir: dir}
	}
	if err := s.Migrate(ctx); err != nil {
		log.Printf("accounts: %v; accounts are off", err)
		pool.Close()
		return nil
	}
	log.Printf("accounts: on")
	return &accounts.Handler{S: s}
}

// startServers opens the registry of community servers next to the accounts (same database): hosts register through
// their agents, admins approve, and a prober keeps checking from outside.  Returns it as a directory for tickets and
// /link.  SERVERS_MAX_PER_ACCOUNT (5) and SERVERS_PROBE_MINUTES (3) tune it.
func startServers(acc *accounts.Service) *servers.Service {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := servers.New(acc.Pool())
	if err := s.Migrate(ctx); err != nil {
		log.Printf("servers: %v; the server registry is off", err)
		return nil
	}
	if n, err := strconv.Atoi(os.Getenv("SERVERS_MAX_PER_ACCOUNT")); err == nil && n > 0 {
		s.MaxPerAccount = n
	}
	every := 3 * time.Minute
	if n, err := strconv.Atoi(os.Getenv("SERVERS_PROBE_MINUTES")); err == nil && n > 0 {
		every = time.Duration(n) * time.Minute
	}
	if os.Getenv("SERVERS_DEV") == "1" {
		// local testing only: probe loopback and private addresses, and trust the CA in SERVERS_DEV_CA
		s.Prober.AllowPrivate = true
		s.Prober.Timeout = 2 * time.Second
		if n, err := strconv.Atoi(os.Getenv("SERVERS_PROBE_SECONDS")); err == nil && n > 0 {
			every = time.Duration(n) * time.Second
		}
		if f := os.Getenv("SERVERS_DEV_CA"); f != "" {
			if pem, err := os.ReadFile(f); err == nil {
				pool := x509.NewCertPool()
				pool.AppendCertsFromPEM(pem)
				s.Prober.RootCAs = pool
			}
		}
		log.Printf("servers: DEV MODE: probing private addresses; never use this in production")
	}
	h := &servers.Handler{S: s, Acc: acc}
	if statsStore != nil {
		puller := stats.NewPuller(statsStore)
		dev := os.Getenv("SERVERS_DEV") == "1"
		h.Stats = purger{statsStore, puller}
		pullEvery := time.Minute
		if n, err := strconv.Atoi(os.Getenv("STATS_PULL_SECONDS")); err == nil && n > 0 {
			pullEvery = time.Duration(n) * time.Second
		}
		go puller.RunPull(context.Background(), func(ctx context.Context) ([]stats.Remote, error) {
			list, err := s.StatsRemotes(ctx)
			out := make([]stats.Remote, 0, len(list))
			for _, v := range list {
				pool := stats.PoolCommunity
				if v.Official {
					pool = stats.PoolOfficial
				}
				base := v.BaseURL
				if os.Getenv("STATS_PULL_PLAIN_HTTP") == "1" {
					base = "http://" + strings.TrimPrefix(base, "https://") // local testing only: an agent with no TLS in front
				}
				out = append(out, stats.Remote{ServerID: v.ServerID, BaseURL: base, Private: v.Official || dev, Pool: pool})
			}
			return out, err
		}, pullEvery)
		log.Printf("stats: pulling game stats from registered servers every %s", pullEvery)
	}
	h.Mount(http.DefaultServeMux)
	go s.RunProber(context.Background(), every)
	log.Printf("servers: registry on, probing every %s", every)
	return s
}

// purger deletes what was pulled from a server and makes the puller forget it.
type purger struct {
	*stats.Store
	p *stats.Puller
}

func (u purger) PurgeServer(ctx context.Context, serverID string) (int64, error) {
	n, err := u.Store.PurgeServer(ctx, serverID)
	if err == nil {
		u.p.Forget(serverID)
	}
	return n, err
}

// accountPlayers answers the player pages' questions about accounts: which account a key is linked to, and an account's keys.
type accountPlayers struct {
	acc   *accounts.Service
	links link.Backend
}

func (a accountPlayers) AccountOfKey(key string) (uint64, bool) { return a.links.AccountOf(key) }

func (a accountPlayers) KeysOfAccount(account uint64) []string {
	var out []string
	for _, l := range a.links.LinksFor(account) {
		out = append(out, l.Key)
	}
	return out
}

func (a accountPlayers) AccountByUsername(ctx context.Context, name string) (uint64, bool) {
	acc, err := a.acc.ByUsername(ctx, name)
	return acc.ID, err == nil
}

func (a accountPlayers) AccountInfo(ctx context.Context, account uint64) (stats.AccountInfo, bool) {
	acc, err := a.acc.ByID(ctx, account)
	if err != nil {
		return stats.AccountInfo{}, false
	}
	return stats.AccountInfo{Username: acc.Username, Avatar: acc.Avatar}, true
}
