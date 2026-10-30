package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"time"

	"s4ndmod26/status-api/demo"
	"s4ndmod26/status-api/replay"
)

// User demos: old client demos (.dm_57/59/60) that someone uploads one at a
// time into a private session.  Each upload is parsed and checked against the
// demos already there; demos of one game form a group, and a group is merged
// into a 3D replay in the shape /api/games/{match}/paths returns.
//
// This is a side project and deliberately separate from the server's own
// replays: sessions live in their own directory, never in the replay
// directory, so the replay list, the scanner, stats and play-of-the-game never
// see them.  There is no listing endpoint and ids are 128 random bits.  Off
// unless USERDEMO_UPLOAD_ENABLED=1 and a key is configured.

type userDemoConfig struct {
	enabled         bool
	key             string
	dir             string
	maxFileBytes    int64
	maxDemos        int
	maxAge          time.Duration
	sessionsPerHour int
	uploadsPerHour  int
}

func userDemoConfigFromEnv() userDemoConfig {
	num := func(name string, def int) int {
		if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
			return n
		}
		return def
	}
	cfg := userDemoConfig{
		enabled:         os.Getenv("USERDEMO_UPLOAD_ENABLED") == "1",
		key:             os.Getenv("USERDEMO_UPLOAD_KEY"),
		dir:             os.Getenv("USERDEMO_DIR"),
		maxFileBytes:    int64(num("USERDEMO_MAX_FILE_MB", 40)) << 20,
		maxDemos:        num("USERDEMO_MAX_DEMOS", 40),
		maxAge:          time.Duration(num("USERDEMO_MAX_AGE_DAYS", 7)) * 24 * time.Hour,
		sessionsPerHour: num("USERDEMO_SESSIONS_PER_HOUR", 5),
		uploadsPerHour:  num("USERDEMO_UPLOADS_PER_HOUR", 60),
	}
	if cfg.dir == "" {
		cfg.dir = "/userdemos"
	}
	return cfg
}

const (
	udRepsPerGroup = 3 // demos of a group a new upload is compared against
	udParseWait    = 45 * time.Second
)

var (
	udIDRe  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	udExtRe = regexp.MustCompile(`(?i)\.dm_(\d{2})$`)
)

// udDemo is one uploaded demo and what became of it.
type udDemo struct {
	N         int    `json:"n"`
	Protocol  int    `json:"protocol"`
	Size      int64  `json:"size"`
	Status    string `json:"status"` // first, synced, unsynced
	Group     int    `json:"group"`
	Reason    string `json:"reason,omitempty"`  // why it is not in an earlier group
	Warning   string `json:"warning,omitempty"` // e.g. a truncated demo
	LinkedTo  []int  `json:"linked_to,omitempty"`
	Anchors   int    `json:"anchors,omitempty"`
	Basis     string `json:"basis,omitempty"` // what the sync rests on
	Map       string `json:"map"`
	Recorder  string `json:"recorder"`
	Spectator bool   `json:"spectator"`
	StartMs   int    `json:"start_ms"` // in the demo's own clock
	EndMs     int    `json:"end_ms"`
	Frames    int    `json:"frames"`
	// BaseOffsetMs puts this demo on its group's clock: base = own + offset.
	BaseOffsetMs int `json:"base_offset_ms"`
}

func (d *udDemo) span() int { return d.EndMs - d.StartMs }

type udSession struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Next    int       `json:"next"`
	Demos   []udDemo  `json:"demos"`
}

// udGroup is the summary of one synced group, for the page.
type udGroup struct {
	Group    int    `json:"group"`
	Demos    []int  `json:"demos"`
	Map      string `json:"map"`
	StartMs  int    `json:"start_ms"` // on the group's clock
	EndMs    int    `json:"end_ms"`
	Playable bool   `json:"playable"`
}

type userDemos struct {
	cfg   userDemoConfig
	parse chan struct{} // one heavy job (parse, sync, merge) at a time

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per session, so uploads into one session are serial

	rl rateLimiter
}

func startUserDemos(cfg userDemoConfig) {
	if !cfg.enabled {
		return
	}
	if cfg.key == "" {
		log.Printf("userdemos: USERDEMO_UPLOAD_ENABLED is set but USERDEMO_UPLOAD_KEY is empty; leaving the endpoints off")
		return
	}
	if err := os.MkdirAll(cfg.dir, 0o700); err != nil {
		log.Printf("userdemos: %v; leaving the endpoints off", err)
		return
	}
	// Parsing holds a few hundred MB per demo.  Without a limit from the environment, keep Go's
	// heap from running away: the container may be killed outright, and nothing restarts this process.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(1536 << 20)
	}
	u := newUserDemos(cfg)
	u.routes(http.DefaultServeMux)
	go u.sweep()
	log.Printf("userdemos: enabled, storing in %s", cfg.dir)
}

func newUserDemos(cfg userDemoConfig) *userDemos {
	return &userDemos{cfg: cfg, parse: make(chan struct{}, 1), locks: map[string]*sync.Mutex{}}
}

func (u *userDemos) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/userdemos", u.createSession)
	mux.HandleFunc("POST /api/userdemos/{id}/demos", u.upload)
	mux.HandleFunc("GET /api/userdemos/{id}", u.state)
	mux.HandleFunc("DELETE /api/userdemos/{id}/demos/{n}", u.remove)
	mux.HandleFunc("GET /api/userdemos/{id}/paths", u.paths)
}

// ---- plumbing ----

func udJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func udFail(w http.ResponseWriter, code int, msg string) {
	udJSON(w, code, map[string]string{"error": msg})
}

// authorized checks the upload key in constant time.
func (u *userDemos) authorized(r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Upload-Key")), []byte(u.cfg.key)) == 1
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

func (u *userDemos) lock(id string) func() {
	u.mu.Lock()
	m := u.locks[id]
	if m == nil {
		m = &sync.Mutex{}
		u.locks[id] = m
	}
	u.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// heavy runs a parse/merge job in the single worker slot, waiting a bounded
// time for it.  Returned memory is handed back to the OS afterwards.
func (u *userDemos) heavy(w http.ResponseWriter, job func()) bool {
	select {
	case u.parse <- struct{}{}:
	case <-time.After(udParseWait):
		udFail(w, http.StatusServiceUnavailable, "busy, try again shortly")
		return false
	}
	defer func() { <-u.parse; debug.FreeOSMemory() }()
	job()
	return true
}

func (u *userDemos) sessionDir(id string) string { return filepath.Join(u.cfg.dir, id) }

func (u *userDemos) load(id string) (*udSession, error) {
	b, err := os.ReadFile(filepath.Join(u.sessionDir(id), "session.json"))
	if err != nil {
		return nil, err
	}
	var s udSession
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (u *userDemos) save(s *udSession) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := filepath.Join(u.sessionDir(s.ID), "session.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (u *userDemos) demoPath(id string, d *udDemo) string {
	return filepath.Join(u.sessionDir(id), fmt.Sprintf("%d.dm_%d", d.N, d.Protocol))
}

// session loads the session named in the request, answering for unknown ids.
func (u *userDemos) session(w http.ResponseWriter, r *http.Request) (*udSession, bool) {
	id := r.PathValue("id")
	if !udIDRe.MatchString(id) {
		udFail(w, http.StatusNotFound, "no such session")
		return nil, false
	}
	s, err := u.load(id)
	if err != nil {
		udFail(w, http.StatusNotFound, "no such session")
		return nil, false
	}
	return s, true
}

// parseDemo reads a stored demo, keeping only what the merge needs.
func (u *userDemos) parseDemo(path string, protocol int) (*demo.Demo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, perr := demo.ParseWith(data, protocol, demo.Options{Entities: demo.KeepPlayersEvents})
	if d == nil || len(d.Frames) == 0 {
		if perr == nil {
			perr = fmt.Errorf("the demo has no frames")
		}
		return nil, perr
	}
	return d, nil // a truncated demo keeps the frames before the break
}

// ---- endpoints ----

func (u *userDemos) createSession(w http.ResponseWriter, r *http.Request) {
	if !u.authorized(r) {
		udFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	if !u.rl.allow("s:"+clientIP(r), u.cfg.sessionsPerHour) {
		udFail(w, http.StatusTooManyRequests, "too many sessions, try later")
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		udFail(w, http.StatusInternalServerError, "no randomness")
		return
	}
	s := &udSession{ID: hex.EncodeToString(raw[:]), Created: time.Now().UTC(), Demos: []udDemo{}}
	if err := os.MkdirAll(u.sessionDir(s.ID), 0o700); err != nil || u.save(s) != nil {
		udFail(w, http.StatusInternalServerError, "could not create the session")
		return
	}
	udJSON(w, http.StatusCreated, map[string]string{"id": s.ID})
}

func (u *userDemos) upload(w http.ResponseWriter, r *http.Request) {
	if !u.authorized(r) {
		udFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	if !u.rl.allow("u:"+clientIP(r), u.cfg.uploadsPerHour) {
		udFail(w, http.StatusTooManyRequests, "too many uploads, try later")
		return
	}
	s, ok := u.session(w, r)
	if !ok {
		return
	}
	defer u.lock(s.ID)()
	if s, ok = u.session(w, r); !ok { // re-read under the lock
		return
	}
	if len(s.Demos) >= u.cfg.maxDemos {
		udFail(w, http.StatusConflict, fmt.Sprintf("a session holds at most %d demos", u.cfg.maxDemos))
		return
	}

	// the file name only tells the protocol; it is never used as a path
	m := udExtRe.FindStringSubmatch(r.URL.Query().Get("name"))
	if m == nil {
		udFail(w, http.StatusUnsupportedMediaType, "send one raw .dm_57, .dm_59 or .dm_60 file (not a zip)")
		return
	}
	proto, _ := strconv.Atoi(m[1])
	if demo.LayoutFor(proto) == nil {
		udFail(w, http.StatusUnsupportedMediaType, fmt.Sprintf("demo protocol %d is not supported", proto))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, u.cfg.maxFileBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		udFail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than %d MB", u.cfg.maxFileBytes>>20))
		return
	}
	if len(data) < 16 || (data[0] == 'P' && data[1] == 'K') {
		udFail(w, http.StatusUnsupportedMediaType, "that is not a raw demo file (zips are not accepted)")
		return
	}

	var verdict *udDemo
	var failure string
	if !u.heavy(w, func() { verdict, failure = u.addDemo(s, data, proto) }) {
		return
	}
	if failure != "" {
		udFail(w, http.StatusUnprocessableEntity, failure)
		return
	}
	if err := u.save(s); err != nil {
		udFail(w, http.StatusInternalServerError, "could not save the session")
		return
	}
	udJSON(w, http.StatusCreated, map[string]interface{}{"demo": verdict, "groups": udGroups(s)})
}

// addDemo parses a new demo, stores it and decides which group it joins.  It
// is called with the worker slot held.  A non-empty string is a user-facing
// rejection.
func (u *userDemos) addDemo(s *udSession, data []byte, proto int) (*udDemo, string) {
	d, perr := demo.ParseWith(data, proto, demo.Options{Entities: demo.KeepPlayersEvents})
	if d == nil || len(d.Frames) == 0 {
		return nil, "could not read that demo: " + errText(perr, "no frames")
	}
	info := demo.Describe(d)
	nd := udDemo{
		N: s.Next, Protocol: proto, Size: int64(len(data)), Map: info.Map, Recorder: info.Recorder,
		Spectator: info.Spectator, StartMs: info.StartTime, EndMs: info.EndTime, Frames: info.Frames, Group: -1,
	}
	if perr != nil {
		nd.Warning = "the demo is cut short or damaged; the part before the damage is used"
	}
	if info.Map == "" {
		return nil, "that demo does not say which map it is from"
	}

	// compare with a few demos of every existing group; the demos are read
	// back from disk one at a time, so at most two are parsed at once
	linked := map[int]bool{}
	var firstReason string
	groupOffset := map[int]int{} // group -> this demo's offset onto that group's clock
	for _, g := range groupIDs(s) {
		for _, rep := range u.reps(s, g) {
			rd, err := u.parseDemo(u.demoPath(s.ID, rep), rep.Protocol)
			if err != nil {
				continue
			}
			res := demo.Sync(rd, d)
			rd = nil
			if res.Synced {
				if !linked[g] {
					linked[g] = true
					groupOffset[g] = res.OffsetMs + rep.BaseOffsetMs
					nd.LinkedTo = append(nd.LinkedTo, rep.N)
					if res.CmdAnchors > nd.Anchors {
						nd.Anchors, nd.Basis = res.CmdAnchors, res.Basis
					}
				}
				break
			}
			if firstReason == "" {
				firstReason = res.Reason
			}
		}
	}
	switch {
	case len(s.Demos) == 0:
		nd.Status, nd.Group = "first", 0
	case len(linked) > 0:
		ids := make([]int, 0, len(linked))
		for g := range linked {
			ids = append(ids, g)
		}
		sort.Ints(ids)
		nd.Status, nd.Group, nd.BaseOffsetMs = "synced", ids[0], groupOffset[ids[0]]
		// a demo that links two groups joins them: the later group moves onto the earlier one's clock
		for _, g := range ids[1:] {
			shift := groupOffset[ids[0]] - groupOffset[g]
			for i := range s.Demos {
				if s.Demos[i].Group == g {
					s.Demos[i].Group, s.Demos[i].BaseOffsetMs = ids[0], s.Demos[i].BaseOffsetMs+shift
				}
			}
		}
	default:
		nd.Status, nd.Group, nd.Reason = "unsynced", nextGroup(s), firstReason
	}

	if err := os.WriteFile(u.demoPath(s.ID, &nd), data, 0o600); err != nil {
		return nil, "could not store the demo"
	}
	s.Demos = append(s.Demos, nd)
	s.Next++
	return &s.Demos[len(s.Demos)-1], ""
}

func errText(err error, def string) string {
	if err != nil {
		return err.Error()
	}
	return def
}

func groupIDs(s *udSession) []int {
	seen := map[int]bool{}
	var out []int
	for _, d := range s.Demos {
		if !seen[d.Group] {
			seen[d.Group] = true
			out = append(out, d.Group)
		}
	}
	sort.Ints(out)
	return out
}

func nextGroup(s *udSession) int {
	n := 0
	for _, d := range s.Demos {
		if d.Group >= n {
			n = d.Group + 1
		}
	}
	return n
}

// reps picks the demos of a group a new upload is compared against: the
// longest ones, which overlap most of the game.
func (u *userDemos) reps(s *udSession, group int) []*udDemo {
	var ds []*udDemo
	for i := range s.Demos {
		if s.Demos[i].Group == group {
			ds = append(ds, &s.Demos[i])
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].span() > ds[j].span() })
	if len(ds) > udRepsPerGroup {
		ds = ds[:udRepsPerGroup]
	}
	return ds
}

func udGroups(s *udSession) []udGroup {
	byGroup := map[int]*udGroup{}
	var order []int
	for _, d := range s.Demos {
		g := byGroup[d.Group]
		if g == nil {
			g = &udGroup{Group: d.Group, Map: d.Map, StartMs: d.StartMs + d.BaseOffsetMs, EndMs: d.EndMs + d.BaseOffsetMs}
			byGroup[d.Group] = g
			order = append(order, d.Group)
		}
		g.Demos = append(g.Demos, d.N)
		if v := d.StartMs + d.BaseOffsetMs; v < g.StartMs {
			g.StartMs = v
		}
		if v := d.EndMs + d.BaseOffsetMs; v > g.EndMs {
			g.EndMs = v
		}
		g.Playable = true
	}
	sort.Ints(order)
	out := make([]udGroup, 0, len(order))
	for _, id := range order {
		out = append(out, *byGroup[id])
	}
	return out
}

func (u *userDemos) state(w http.ResponseWriter, r *http.Request) {
	s, ok := u.session(w, r)
	if !ok {
		return
	}
	udJSON(w, http.StatusOK, map[string]interface{}{"id": s.ID, "demos": s.Demos, "groups": udGroups(s), "max_demos": u.cfg.maxDemos})
}

// remove deletes one demo.  The re-check is shallow: the other demos keep
// their groups, and a group that a removed demo bridged is reported by the
// merge as demos that do not sync.
func (u *userDemos) remove(w http.ResponseWriter, r *http.Request) {
	if !u.authorized(r) {
		udFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	s, ok := u.session(w, r)
	if !ok {
		return
	}
	defer u.lock(s.ID)()
	if s, ok = u.session(w, r); !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		udFail(w, http.StatusBadRequest, "bad demo number")
		return
	}
	for i := range s.Demos {
		if s.Demos[i].N == n {
			os.Remove(u.demoPath(s.ID, &s.Demos[i]))
			s.Demos = append(s.Demos[:i], s.Demos[i+1:]...)
			if err := u.save(s); err != nil {
				udFail(w, http.StatusInternalServerError, "could not save the session")
				return
			}
			udJSON(w, http.StatusOK, map[string]interface{}{"demos": s.Demos, "groups": udGroups(s)})
			return
		}
	}
	udFail(w, http.StatusNotFound, "no such demo in the session")
}

// userPathsResponse is /api/games/{match}/paths plus the event feed the page
// shows (the game server builds that from its stats; here it comes from the
// events reconstructed out of the demos) and how the merge went.
type userPathsResponse struct {
	pathsResponse
	Feed   []userFeedEvent `json:"feed"`
	Rounds []userRound     `json:"rounds"`
	// Shots are the bullets fired, drawn as lines from the shooter; they end
	// at a hit where one was found.
	Shots  []replay.Shot   `json:"shots"`
	Source userPathsSource `json:"source"`
}

// userRound is one stretch of live play on the replay's clock.  Time between
// rounds (warm-up, intermission, scoreboard) is not in the replay.  The
// game's clock for the round reads limit_ms - (t - clock_start_ms).
type userRound struct {
	N            int   `json:"n"`
	Of           int   `json:"of"`
	StartMs      int32 `json:"start_ms"`
	EndMs        int32 `json:"end_ms"`
	BeatMs       int32 `json:"beat_ms,omitempty"` // the time to beat in the second round of a stopwatch match
	LimitMs      int32 `json:"limit_ms"`
	ClockStartMs int32 `json:"clock_start_ms"`
}

// userFeedEvent is one line of the page's event feed (see feed.js).
type userFeedEvent struct {
	TMs        int32     `json:"t_ms"` // from the start of the replay
	Type       string    `json:"type"` // kill, teamkill, suicide
	Actor      string    `json:"actor"`
	ActorTeam  string    `json:"actor_team"`
	ActorSlot  int32     `json:"actor_slot"`
	Target     string    `json:"target"`
	TargetTeam string    `json:"target_team"`
	TargetSlot int32     `json:"target_slot"`
	Pos        *[3]int32 `json:"pos,omitempty"` // where it happened; objectives have none
	// Text is the game's own wording of an objective event ("Allies have stolen
	// the War Documents!"); the page shows it as it is.
	Text string `json:"text,omitempty"`
}

// buildFeed turns the merged replay's kills into the feed.  Teams are read
// from the players' samples at the time of the event.
func buildFeed(rep *replay.Replay, startMs int32, objectives []demo.Objective, rounds []demo.Round) []userFeedEvent {
	team := func(client int32, t int32) string {
		i := sort.Search(len(rep.Frames), func(i int) bool { return rep.Frames[i].ServerTime >= t })
		for j := i; j < len(rep.Frames) && j < i+3; j++ {
			for _, s := range rep.Frames[j].Samples {
				if s.ClientNum == client {
					switch s.Team {
					case 1:
						return "axis"
					case 2:
						return "allies"
					}
					return ""
				}
			}
		}
		return ""
	}
	name := func(c int32) string {
		if c < 0 || int(c) >= len(rep.Header.PlayerNames) {
			return ""
		}
		return demo.CleanName(rep.Header.PlayerNames[c])
	}
	out := []userFeedEvent{}
	for _, e := range rep.Events {
		var typ string
		switch e.Type {
		case replay.EventKill:
			typ = "kill"
		case replay.EventTeamkill:
			typ = "teamkill"
		case replay.EventSuicide:
			typ = "suicide"
		case replay.EventRevive:
			typ = "revive"
		default:
			continue
		}
		pos := [3]int32{int32(e.Origin[0]), int32(e.Origin[1]), int32(e.Origin[2])}
		if typ == "revive" && e.ActorClientNum < 0 {
			// nobody could be told as the reviver: say what happened to the player
			out = append(out, userFeedEvent{
				TMs: e.ServerTime - startMs, Type: typ, Text: name(e.TargetClientNum) + " was revived",
				ActorSlot: -1, TargetSlot: e.TargetClientNum, TargetTeam: team(e.TargetClientNum, e.ServerTime), Pos: &pos,
			})
			continue
		}
		out = append(out, userFeedEvent{
			TMs: e.ServerTime - startMs, Type: typ,
			Actor: name(e.ActorClientNum), ActorTeam: team(e.ActorClientNum, e.ServerTime), ActorSlot: e.ActorClientNum,
			Target: name(e.TargetClientNum), TargetTeam: team(e.TargetClientNum, e.ServerTime), TargetSlot: e.TargetClientNum,
			Pos: &pos,
		})
	}
	teamName := map[int32]string{1: "axis", 2: "allies"}
	for _, o := range objectives {
		out = append(out, userFeedEvent{
			TMs: o.Time - startMs, Type: o.Kind, Text: o.Text,
			Actor: name(o.Actor), ActorTeam: teamName[o.Team], ActorSlot: o.Actor, TargetSlot: -1,
		})
	}
	mmss := func(ms int32) string { return fmt.Sprintf("%d:%02d", ms/60000, ms/1000%60) }
	for _, r := range rounds {
		begin := fmt.Sprintf("Round %d of %d begins", r.N, r.Of)
		if r.N > 1 {
			begin += ": the sides have swapped"
		}
		if r.BeatMs > 0 {
			begin += ", time to beat " + mmss(r.BeatMs)
		}
		end := fmt.Sprintf("Round %d of %d ends", r.N, r.Of)
		if r.LimitMs > 0 {
			end += ", " + mmss(r.LimitMs-(r.EndMs-r.ClockStartMs)) + " left on the clock"
		}
		out = append(out,
			userFeedEvent{TMs: r.StartMs - startMs, Type: "round", Text: begin, TargetSlot: -1, ActorSlot: -1},
			userFeedEvent{TMs: r.EndMs - startMs, Type: "round", Text: end, TargetSlot: -1, ActorSlot: -1})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TMs < out[j].TMs })
	return out
}

type userPathsSource struct {
	Group int                `json:"group"`
	Parts []demo.PartReport  `json:"parts"`
	Stats userPathsMergeStat `json:"stats"`
}

type userPathsMergeStat struct {
	Frames           int     `json:"frames"`
	AvgPlayers       float64 `json:"avg_players"`
	PlayersWithExact int     `json:"players_with_exact_health"`
	Kills            int     `json:"kills"`
	HealthLostHp     int     `json:"health_lost_hp"`
	AttributedHp     int     `json:"attributed_hp"`
}

func (u *userDemos) paths(w http.ResponseWriter, r *http.Request) {
	s, ok := u.session(w, r)
	if !ok {
		return
	}
	group, err := strconv.Atoi(r.URL.Query().Get("group"))
	if err != nil {
		group = 0
	}
	var members []*udDemo
	for i := range s.Demos {
		if s.Demos[i].Group == group {
			members = append(members, &s.Demos[i])
		}
	}
	if len(members) == 0 {
		udFail(w, http.StatusNotFound, "no such group")
		return
	}

	// a finished merge is kept on disk, keyed by exactly which demos went in
	h := fnv.New64a()
	for _, m := range members {
		fmt.Fprintf(h, "%d:%d:%d;", m.N, m.Size, m.BaseOffsetMs)
	}
	cache := filepath.Join(u.sessionDir(s.ID), fmt.Sprintf("paths_%d_%x.json", group, h.Sum64()))
	if b, err := os.ReadFile(cache); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Write(b)
		return
	}

	var body []byte
	var failure string
	if !u.heavy(w, func() { body, failure = u.buildPaths(s, group, members) }) {
		return
	}
	if failure != "" {
		udFail(w, http.StatusUnprocessableEntity, failure)
		return
	}
	// drop merges of earlier versions of this group, then keep this one
	if old, _ := filepath.Glob(filepath.Join(u.sessionDir(s.ID), fmt.Sprintf("paths_%d_*.json", group))); old != nil {
		for _, f := range old {
			os.Remove(f)
		}
	}
	os.WriteFile(cache, body, 0o600)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(body)
}

func (u *userDemos) buildPaths(s *udSession, group int, members []*udDemo) ([]byte, string) {
	ref := 0
	for i, m := range members {
		if m.span() > members[ref].span() {
			ref = i
		}
	}
	rep, mr, err := demo.MergeStream(len(members), ref, func(i int) (*demo.Demo, error) {
		return u.parseDemo(u.demoPath(s.ID, members[i]), members[i].Protocol)
	})
	if err != nil {
		return nil, "could not merge that group: " + err.Error()
	}
	// Only live play is shown: the warm-up, the intermission (where every
	// client is moved to the same spot, so all players seem to fly to one
	// place) and the scoreboard are cut, and later rounds follow earlier ones.
	objectives, carries := mr.Objectives, mr.Carries
	var rounds []demo.Round
	if len(mr.Rounds) > 0 {
		comp := demo.NewCompressor(mr.Rounds)
		if live := comp.Replay(rep); len(live.Frames) > 0 {
			rep, rounds = live, comp.Rounds()
			objectives, carries = comp.MapObjectives(mr.Objectives), comp.MapCarries(mr.Carries)
		}
	}
	p := replay.BuildPaths(rep, 250)
	resp := userPathsResponse{pathsResponse: pathsResponse{Paths: p, Entities: replay.BuildEntities(rep, replay.PathOptions{}), Carries: []viewCarry{}},
		Feed: buildFeed(rep, p.StartMs, objectives, rounds), Rounds: []userRound{}, Shots: replay.BuildShots(rep, replay.PathOptions{})}
	for _, r := range rounds {
		resp.Rounds = append(resp.Rounds, userRound{r.N, r.Of, r.StartMs, r.EndMs, r.BeatMs, r.LimitMs, r.ClockStartMs})
	}
	for _, c := range carries {
		resp.Carries = append(resp.Carries, viewCarry{Slot: c.Slot, StartMs: c.StartMs, EndMs: c.EndMs})
	}
	for _, pp := range p.Players {
		name := demo.CleanName(rep.Header.PlayerNames[pp.Slot%len(rep.Header.PlayerNames)])
		if name == "" {
			name = "Player"
		}
		resp.Players = append(resp.Players, pathsPlayer{PlayerPath: pp, Name: name})
	}
	kills := 0
	for _, e := range rep.Events {
		if e.Type == replay.EventKill {
			kills++
		}
	}
	resp.Source = userPathsSource{Group: group, Parts: mr.Parts, Stats: userPathsMergeStat{
		Frames: mr.Frames, AvgPlayers: mr.AvgPlayers, PlayersWithExact: mr.OwnHealthPlayers, Kills: kills,
		HealthLostHp: mr.Damage.Attributed + mr.Damage.Explosive + mr.Damage.Unattributed, AttributedHp: mr.Damage.Attributed + mr.Damage.Explosive,
	}}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, "could not build the paths"
	}
	return b, ""
}

// sweep removes sessions nobody touched for maxAge.
func (u *userDemos) sweep() {
	for {
		entries, _ := os.ReadDir(u.cfg.dir)
		for _, e := range entries {
			if !e.IsDir() || !udIDRe.MatchString(e.Name()) {
				continue
			}
			var newest time.Time
			files, _ := os.ReadDir(filepath.Join(u.cfg.dir, e.Name()))
			for _, f := range files {
				if info, err := f.Info(); err == nil && info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
			if !newest.IsZero() && time.Since(newest) > u.cfg.maxAge {
				if err := os.RemoveAll(filepath.Join(u.cfg.dir, e.Name())); err == nil {
					log.Printf("userdemos: removed idle session %s", e.Name())
				}
			}
		}
		time.Sleep(10 * time.Minute)
	}
}
