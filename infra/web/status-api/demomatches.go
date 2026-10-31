package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"s4ndmod26/status-api/demo"
	"s4ndmod26/status-api/demostore"
)

// Demo matches: old client demos (.dm_57/59/60) that people upload, grouped
// into matches (one game, seen by one or more players) which are merged into a
// 3D replay in the shape /api/games/{match}/paths returns.
//
// Matches are public and permanent: anyone can list them and watch.  Only an
// uploader key (see demostore.Keys) can add a demo, and the same key can
// remove what it uploaded, and only that.  This is a side project, kept
// apart from the server's own replays: nothing here touches the replay
// directory, the scanner, the stats or play-of-the-game.  Off unless
// USERDEMO_UPLOAD_ENABLED=1 and a database is configured; with no keys it is
// read-only.

type demoConfig struct {
	enabled          bool
	keysFile         string
	legacyKey        string // USERDEMO_UPLOAD_KEY: one more key, labelled "default"
	dir              string
	maxFileBytes     int64
	maxDemosPerMatch int
	uploadsPerHour   int
}

func demoConfigFromEnv() demoConfig {
	num := func(name string, def int) int {
		if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
			return n
		}
		return def
	}
	cfg := demoConfig{
		enabled:          os.Getenv("USERDEMO_UPLOAD_ENABLED") == "1",
		keysFile:         os.Getenv("USERDEMO_KEYS_FILE"),
		legacyKey:        os.Getenv("USERDEMO_UPLOAD_KEY"),
		dir:              os.Getenv("USERDEMO_DIR"),
		maxFileBytes:     int64(num("USERDEMO_MAX_FILE_MB", 40)) << 20,
		maxDemosPerMatch: num("USERDEMO_MAX_DEMOS", 60),
		uploadsPerHour:   num("USERDEMO_UPLOADS_PER_HOUR", 60),
	}
	if cfg.dir == "" {
		cfg.dir = "/userdemos"
	}
	return cfg
}

const (
	demoRepsPerMatch = 3 // demos of a match a new upload is compared with
	demoParseWait    = 45 * time.Second
	minSyncOverlapMs = 5000 // demo.Sync needs this much overlap
)

var (
	matchIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)
	demoExtRe = regexp.MustCompile(`(?i)\.dm_(\d{2})$`)
)

type demoService struct {
	cfg   demoConfig
	keys  *demostore.Keys
	db    *demostore.Store
	blobs demostore.Blobs
	heavy chan struct{} // one parse/merge job at a time
	rl    rateLimiter
}

// startDemoMatches starts the tool: it needs a database (DATABASE_URL).  It
// logs and stays off if anything it needs is missing.
func startDemoMatches(cfg demoConfig) {
	if !cfg.enabled {
		return
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Printf("demos: USERDEMO_UPLOAD_ENABLED is set but DATABASE_URL is not; leaving the demo tool off")
		return
	}
	keys, err := demostore.LoadKeys(cfg.keysFile, cfg.legacyKey)
	if err != nil {
		log.Printf("demos: uploader keys: %v; leaving the demo tool off", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := demostore.Open(ctx, dsn)
	if err == nil {
		err = db.Migrate(ctx)
	}
	if err != nil {
		log.Printf("demos: database: %v; leaving the demo tool off", err)
		return
	}
	blobs, err := demostore.NewFSBlobs(cfg.dir)
	if err != nil {
		log.Printf("demos: storage %s: %v; leaving the demo tool off", cfg.dir, err)
		return
	}
	// Parsing holds a few hundred MB per demo.  Without a limit from the environment, keep Go's heap from
	// running away: the container may be killed outright, and nothing restarts this process.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(1536 << 20)
	}
	s := newDemoService(cfg, keys, db, blobs)
	s.routes(http.DefaultServeMux)
	if keys.Len() == 0 {
		log.Printf("demos: no uploader keys configured: matches can be watched but not added to")
	}
	log.Printf("demos: enabled, %d uploader key(s), storing in %s", keys.Len(), cfg.dir)
}

func newDemoService(cfg demoConfig, keys *demostore.Keys, db *demostore.Store, blobs demostore.Blobs) *demoService {
	return &demoService{cfg: cfg, keys: keys, db: db, blobs: blobs, heavy: make(chan struct{}, 1)}
}

func (s *demoService) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/demos/matches", s.listMatches)
	mux.HandleFunc("GET /api/demos/matches/{id}", s.getMatch)
	mux.HandleFunc("GET /api/demos/matches/{id}/paths", s.matchPaths)
	mux.HandleFunc("GET /api/demos/matches/{id}/stats", s.matchStats)
	mux.HandleFunc("POST /api/demos/upload", s.upload)
	mux.HandleFunc("GET /api/demos/mine", s.mine)
	mux.HandleFunc("DELETE /api/demos/files/{id}", s.remove)
	mux.HandleFunc("GET /api/demos/series", s.listSeries)
	mux.HandleFunc("GET /api/demos/series/{id}", s.getSeries)
	mux.HandleFunc("POST /api/demos/series", s.saveSeries)
	mux.HandleFunc("PUT /api/demos/series/{id}", s.saveSeries)
	mux.HandleFunc("DELETE /api/demos/series/{id}", s.deleteSeries)
}

// ---- plumbing ----

func demoJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func demoFail(w http.ResponseWriter, code int, msg string) {
	demoJSON(w, code, map[string]string{"error": msg})
}

// uploader is the label of the key the request carries, if it is a real one.
func (s *demoService) uploader(r *http.Request) (string, bool) {
	return s.keys.Lookup(r.Header.Get("X-Upload-Key"))
}

// clientIP is nginx's X-Real-IP when present (nginx overwrites it), else the peer.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type rateLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// allow records an event for key and reports whether it is within limit per hour.
func (rl *rateLimiter) allow(key string, limit int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.hits == nil {
		rl.hits = map[string][]time.Time{}
	}
	now := time.Now()
	recent := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		rl.hits[key] = recent
		return false
	}
	rl.hits[key] = append(recent, now)
	return true
}

// runHeavy runs a parse/merge job in the single worker slot, waiting a bounded
// time for it.  Returned memory is handed back to the OS afterwards.
func (s *demoService) runHeavy(ctx context.Context, job func()) error {
	select {
	case s.heavy <- struct{}{}:
	case <-time.After(demoParseWait):
		return errBusy
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.heavy; debug.FreeOSMemory() }()
	job()
	return nil
}

var errBusy = errors.New("busy, try again shortly")

// loadDemo reads a stored demo back and parses it, keeping what merging needs.
func (s *demoService) loadDemo(ctx context.Context, d demostore.Demo) (*demo.Demo, error) {
	rc, err := s.blobs.Get(ctx, demostore.RawKey(d.SHA256))
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	dm, _ := parseDemo(data, d.Protocol)
	if dm == nil {
		return nil, errors.New("the stored demo cannot be read")
	}
	return dm, nil
}

// parseDemo parses a demo keeping what merging needs.  d is nil if there is
// nothing to use; otherwise err (if any) says the demo was cut short or
// damaged, and d holds the frames before the damage.
func parseDemo(data []byte, protocol int) (*demo.Demo, error) {
	d, perr := demo.ParseWith(data, protocol, demo.Options{Entities: demo.KeepPlayersEvents})
	if d == nil || len(d.Frames) == 0 {
		if perr == nil {
			perr = errors.New("the demo has no frames")
		}
		return nil, perr
	}
	return d, perr
}

// ---- reading (public) ----

func (s *demoService) listMatches(w http.ResponseWriter, r *http.Request) {
	limit, offset := 30, 0
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		offset = n
	}
	ms, total, err := s.db.ListMatches(r.Context(), limit, offset)
	if err != nil {
		log.Printf("demos: list: %v", err)
		demoFail(w, http.StatusInternalServerError, "could not list the matches")
		return
	}
	if ms == nil {
		ms = []demostore.MatchSummary{}
	}
	demoJSON(w, http.StatusOK, map[string]interface{}{"matches": ms, "total": total})
}

func (s *demoService) match(w http.ResponseWriter, r *http.Request) (*demostore.Match, bool) {
	id := r.PathValue("id")
	if !matchIDRe.MatchString(id) {
		demoFail(w, http.StatusNotFound, "no such match")
		return nil, false
	}
	m, err := s.db.GetMatch(r.Context(), id)
	if err != nil {
		log.Printf("demos: match %s: %v", id, err)
		demoFail(w, http.StatusInternalServerError, "could not read the match")
		return nil, false
	}
	if m == nil {
		demoFail(w, http.StatusNotFound, "no such match")
		return nil, false
	}
	return m, true
}

func (s *demoService) getMatch(w http.ResponseWriter, r *http.Request) {
	m, ok := s.match(w, r)
	if !ok {
		return
	}
	ref, err := s.db.SeriesOf(r.Context(), m.ID)
	if err != nil {
		log.Printf("demos: series of %s: %v", m.ID, err)
		demoFail(w, http.StatusInternalServerError, "could not read the match")
		return
	}
	demoJSON(w, http.StatusOK, struct {
		*demostore.Match
		Series *demostore.SeriesRef `json:"series"`
	}{m, ref})
}

// mergedBody is the gzipped merged output of a match: the kept one, or built
// now (in the worker slot).  A kept one that lacks what is wanted (needs
// returns false) is built again.  On failure it has answered the request.
func (s *demoService) mergedBody(w http.ResponseWriter, r *http.Request, m *demostore.Match, needs func([]byte) bool) ([]byte, bool) {
	var body []byte // gzipped JSON
	if m.MergedKey != "" {
		if rc, err := s.blobs.Get(r.Context(), m.MergedKey); err == nil {
			body, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	if body != nil && needs != nil && !needs(body) {
		body = nil
	}
	if body == nil {
		var failure error
		if err := s.runHeavy(r.Context(), func() { body, failure = s.buildAndKeep(r.Context(), m) }); err != nil {
			demoFail(w, http.StatusServiceUnavailable, err.Error())
			return nil, false
		}
		if failure != nil {
			demoFail(w, http.StatusUnprocessableEntity, failure.Error())
			return nil, false
		}
	}
	return body, true
}

// matchStats serves just the statistics of a match: they are part of the
// merged output, so this reads that and passes on the one part.  Output kept
// before statistics existed is built again.
func (s *demoService) matchStats(w http.ResponseWriter, r *http.Request) {
	m, ok := s.match(w, r)
	if !ok {
		return
	}
	body, ok := s.mergedBody(w, r, m, func(b []byte) bool { return len(statsOf(b)) > 0 })
	if !ok {
		return
	}
	stats := statsOf(body)
	if len(stats) == 0 {
		demoFail(w, http.StatusInternalServerError, "could not read the stored replay")
		return
	}
	etag := fmt.Sprintf(`"%s-v%d-stats"`, m.ID, m.Version)
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "public, max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(stats)
}

// statsOf picks the statistics out of gzipped merged output; nil if there are none.
func statsOf(gz []byte) json.RawMessage {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil
	}
	var v struct {
		Stats json.RawMessage `json:"stats"`
	}
	if json.NewDecoder(zr).Decode(&v) != nil || string(v.Stats) == "null" {
		return nil
	}
	return v.Stats
}

// matchPaths serves the merged replay of a match.  The merge is kept (gzipped,
// for the version of the match it was built from) and rebuilt only when the
// match's demos change.
func (s *demoService) matchPaths(w http.ResponseWriter, r *http.Request) {
	m, ok := s.match(w, r)
	if !ok {
		return
	}
	body, ok := s.mergedBody(w, r, m, nil)
	if !ok {
		return
	}
	etag := fmt.Sprintf(`"%s-v%d"`, m.ID, m.Version)
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("Vary", "Accept-Encoding")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(body)
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		demoFail(w, http.StatusInternalServerError, "could not read the stored replay")
		return
	}
	io.Copy(w, zr)
}

// buildAndKeep merges a match and stores the result.  It runs in the worker slot.
func (s *demoService) buildAndKeep(ctx context.Context, m *demostore.Match) ([]byte, error) {
	if len(m.Demos) == 0 {
		return nil, errors.New("the match has no demos")
	}
	raw, err := buildMatchPaths(m.ID, m.Demos, func(i int) (*demo.Demo, error) { return s.loadDemo(ctx, m.Demos[i]) })
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	key := demostore.PathsKey(m.ID, m.Version)
	if err := s.blobs.Put(ctx, key, bytes.NewReader(buf.Bytes())); err != nil {
		log.Printf("demos: keeping %s: %v", key, err)
		return buf.Bytes(), nil // served this once, rebuilt next time
	}
	old, ok, err := s.db.SetMerged(ctx, m.ID, m.Version, key)
	if err != nil || !ok {
		s.blobs.Delete(ctx, key) // the match changed while it was being built: this is out of date
	} else if old != "" && old != key {
		s.blobs.Delete(ctx, old)
	}
	return buf.Bytes(), nil
}

// ---- writing (needs a key) ----

type uploadResult struct {
	Demo     demostore.Demo `json:"demo"`
	Status   string         `json:"status"` // new_match, joined, already_stored
	Reason   string         `json:"reason,omitempty"`
	LinkedTo []string       `json:"linked_to,omitempty"` // whose demos it matched
	Anchors  int            `json:"anchors,omitempty"`
	Basis    string         `json:"basis,omitempty"`
}

// displayName is an uploaded file's name made safe to show: no directories,
// no control characters, not too long.
func displayName(s string) string {
	s = path.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func (s *demoService) upload(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.uploader(r)
	if !ok {
		demoFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	if !s.rl.allow("k:"+owner, s.cfg.uploadsPerHour) || !s.rl.allow("i:"+clientIP(r), s.cfg.uploadsPerHour*2) {
		demoFail(w, http.StatusTooManyRequests, "too many uploads, try later")
		return
	}
	// the file name only tells the protocol; it is never used as a path
	name := r.URL.Query().Get("name")
	m := demoExtRe.FindStringSubmatch(name)
	if m == nil {
		demoFail(w, http.StatusUnsupportedMediaType, "send one raw .dm_57, .dm_59 or .dm_60 file (not a zip)")
		return
	}
	proto, _ := strconv.Atoi(m[1])
	if demo.LayoutFor(proto) == nil {
		demoFail(w, http.StatusUnsupportedMediaType, fmt.Sprintf("demo protocol %d is not supported", proto))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.maxFileBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		demoFail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than %d MB", s.cfg.maxFileBytes>>20))
		return
	}
	if len(data) < 16 || (data[0] == 'P' && data[1] == 'K') {
		demoFail(w, http.StatusUnsupportedMediaType, "that is not a raw demo file (zips are not accepted)")
		return
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	filename := displayName(name)
	ctx := r.Context()

	// a demo we already have costs nothing: the uploader just claims it too
	if res, ok, err := s.alreadyStored(ctx, sha, owner, filename); err != nil {
		demoFail(w, http.StatusInternalServerError, "could not check the demo")
		return
	} else if ok {
		demoJSON(w, http.StatusOK, res)
		return
	}

	var res uploadResult
	var failure *apiError
	if err := s.runHeavy(ctx, func() { res, failure = s.addNew(ctx, data, proto, sha, owner, filename) }); err != nil {
		demoFail(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if failure != nil {
		demoFail(w, failure.code, failure.msg)
		return
	}
	demoJSON(w, http.StatusCreated, res)
}

type apiError struct {
	code int
	msg  string
}

func (s *demoService) alreadyStored(ctx context.Context, sha, owner, filename string) (uploadResult, bool, error) {
	d, err := s.db.DemoBySHA(ctx, sha)
	if err != nil || d == nil {
		return uploadResult{}, false, err
	}
	if _, err := s.db.AddUpload(ctx, d.ID, owner, filename); err != nil {
		return uploadResult{}, false, err
	}
	return uploadResult{Demo: *d, Status: "already_stored"}, true, nil
}

// addNew parses a new demo, finds the match it belongs to (or starts one) and
// stores it.  It runs in the worker slot.
func (s *demoService) addNew(ctx context.Context, data []byte, proto int, sha, owner, filename string) (uploadResult, *apiError) {
	d, perr := parseDemo(data, proto)
	if d == nil {
		return uploadResult{}, &apiError{http.StatusUnprocessableEntity, "could not read that demo: " + perr.Error()}
	}
	info := demo.Describe(d)
	if info.Map == "" {
		return uploadResult{}, &apiError{http.StatusUnprocessableEntity, "that demo does not say which map it is from"}
	}
	nd := demostore.NewDemo{
		SHA256: sha, Protocol: proto, Size: int64(len(data)), Map: info.Map, ServerID: info.ServerID, Recorder: info.Recorder,
		RecorderSlot: info.ClientNum, Spectator: info.Spectator, StartMs: info.StartTime, EndMs: info.EndTime, Frames: info.Frames,
		Gametype: demo.Gametype(d), Rounds: len(demo.RoundsOf(d)), Players: rosterNames(info),
	}
	if perr != nil {
		nd.Warning = "the demo is cut short or damaged; the part before the damage is used"
	}
	at, offset, pl, err := s.place(ctx, d, info)
	if err != nil {
		log.Printf("demos: placing: %v", err)
		return uploadResult{}, &apiError{http.StatusInternalServerError, "could not match that demo against the others"}
	}
	if at.MatchID != "" {
		if m, _ := s.db.GetMatch(ctx, at.MatchID); m != nil && len(m.Demos) >= s.cfg.maxDemosPerMatch {
			return uploadResult{}, &apiError{http.StatusConflict, fmt.Sprintf("a match holds at most %d demos", s.cfg.maxDemosPerMatch)}
		}
	}
	nd.BaseOffsetMs = offset
	// the file first (stored by content, so storing it twice is harmless), then the rows that point at it
	if err := s.blobs.Put(ctx, demostore.RawKey(sha), bytes.NewReader(data)); err != nil {
		log.Printf("demos: storing %s: %v", sha, err)
		return uploadResult{}, &apiError{http.StatusInternalServerError, "could not store the demo"}
	}
	stored, stale, err := s.db.AddDemo(ctx, nd, owner, filename, at)
	if errors.Is(err, demostore.ErrDuplicate) { // the same file arrived twice at once
		if res, ok, err2 := s.alreadyStored(ctx, sha, owner, filename); ok && err2 == nil {
			return res, nil
		}
	}
	if err != nil {
		log.Printf("demos: recording %s: %v", sha, err)
		return uploadResult{}, &apiError{http.StatusInternalServerError, "could not record the demo"}
	}
	for _, k := range stale {
		s.blobs.Delete(ctx, k)
	}
	pl.res.Demo = stored
	return pl.res, nil
}

func rosterNames(info demo.Info) []string {
	var out []string
	for _, p := range info.Players {
		if p.Name != "" {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

type placement struct {
	res uploadResult
}

// place decides which match a new demo belongs to.  Only demos of matches that
// could be the same game are parsed for the comparison: same map, overlapping
// server time, and (for two players' demos) the same server id.  It returns
// what to do, the demo's offset onto the joined match's clock, and the verdict.
func (s *demoService) place(ctx context.Context, d *demo.Demo, info demo.Info) (demostore.Attachment, int, placement, error) {
	cands, err := s.db.MatchesOfMap(ctx, info.Map)
	if err != nil {
		return demostore.Attachment{}, 0, placement{}, err
	}
	type link struct {
		match demostore.Match
		off   int
	}
	var links []link
	var firstReason string
	var out placement
	for _, m := range cands {
		reps := candidateReps(m.Demos, info)
		if len(reps) == 0 {
			if firstReason == "" && len(m.Demos) > 0 {
				firstReason = "recorded in a different round or map load (not the same game)"
			}
			continue
		}
		for _, rep := range reps {
			rd, err := s.loadDemo(ctx, rep)
			if err != nil {
				continue
			}
			res := demo.Sync(rd, d)
			rd = nil
			if res.Synced {
				links = append(links, link{m, res.OffsetMs + rep.BaseOffsetMs})
				who := rep.Recorder
				if rep.Spectator || who == "" {
					who = "a spectator"
				}
				out.res.LinkedTo = append(out.res.LinkedTo, who)
				if res.CmdAnchors > out.res.Anchors {
					out.res.Anchors, out.res.Basis = res.CmdAnchors, res.Basis
				}
				break
			}
			if firstReason == "" {
				firstReason = res.Reason
			}
		}
	}
	if len(links) == 0 {
		out.res.Status, out.res.Reason = "new_match", firstReason
		return demostore.Attachment{}, 0, out, nil
	}
	// join the earliest match; any other it links is folded into it, its demos
	// shifted onto the joined clock
	at := demostore.Attachment{MatchID: links[0].match.ID, Merge: map[string]int{}}
	for _, l := range links[1:] {
		at.Merge[l.match.ID] = links[0].off - l.off
	}
	out.res.Status = "joined"
	return at, links[0].off, out, nil
}

// candidateReps picks the demos of a match worth comparing a new demo with: the
// ones that could be of the same game, the ones overlapping it most first.
func candidateReps(ds []demostore.Demo, info demo.Info) []demostore.Demo {
	type scored struct {
		d       demostore.Demo
		overlap int
	}
	var sc []scored
	for _, d := range ds {
		if !d.Spectator && !info.Spectator && d.ServerID != "" && info.ServerID != "" && d.ServerID != info.ServerID {
			continue // two players' demos of one game share a server id
		}
		// two demos of one game share the server's clock, so they overlap
		// on it (the match's demos are on one clock, shifted by their offsets)
		lo, hi := max(d.StartMs+d.BaseOffsetMs, info.StartTime), min(d.EndMs+d.BaseOffsetMs, info.EndTime)
		if hi-lo < minSyncOverlapMs {
			continue
		}
		sc = append(sc, scored{d, hi - lo})
	}
	sort.SliceStable(sc, func(i, j int) bool { return sc[i].overlap > sc[j].overlap })
	var out []demostore.Demo
	for i := 0; i < len(sc) && i < demoRepsPerMatch; i++ {
		out = append(out, sc[i].d)
	}
	return out
}

func (s *demoService) mine(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.uploader(r)
	if !ok {
		demoFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	ups, err := s.db.UploadsOf(r.Context(), owner)
	if err != nil {
		demoFail(w, http.StatusInternalServerError, "could not list your uploads")
		return
	}
	series, err := s.db.SeriesOfOwner(r.Context(), owner)
	if err != nil {
		demoFail(w, http.StatusInternalServerError, "could not list your uploads")
		return
	}
	demoJSON(w, http.StatusOK, map[string]interface{}{"owner": owner, "uploads": ups, "series": series})
}

// remove deletes one of the key's own uploads (and, once no one else has
// uploaded the same file, the file and the match it leaves empty).
func (s *demoService) remove(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.uploader(r)
	if !ok {
		demoFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		demoFail(w, http.StatusBadRequest, "bad demo number")
		return
	}
	var rem demostore.Removal
	var found bool
	var dberr error
	// in the worker slot: a removal and a merge or an upload must not interleave
	if err := s.runHeavy(r.Context(), func() { rem, found, dberr = s.db.RemoveUpload(r.Context(), id, owner) }); err != nil {
		demoFail(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if dberr != nil {
		log.Printf("demos: removing %d: %v", id, dberr)
		demoFail(w, http.StatusInternalServerError, "could not remove that")
		return
	}
	if !found { // not found and not yours look the same: others' uploads are not for you to learn about
		demoFail(w, http.StatusNotFound, "you have no such upload")
		return
	}
	for _, k := range rem.Stale {
		s.blobs.Delete(r.Context(), k)
	}
	if rem.DemoGone {
		s.blobs.Delete(r.Context(), demostore.RawKey(rem.SHA256))
	}
	demoJSON(w, http.StatusOK, map[string]interface{}{"removed": true, "demo_deleted": rem.DemoGone, "match_deleted": rem.MatchGone, "match_id": rem.MatchID})
}
