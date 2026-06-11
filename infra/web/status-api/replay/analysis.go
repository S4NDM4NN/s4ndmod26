package replay

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// pm_type values
const (
	pmNormal = 0
	pmDead   = 2
)

// pm_flags bitmask
const pmfLimbo = 0x4000

// team values
const (
	teamFree  = 0
	teamRed   = 1
	teamBlue  = 2
	teamSpec  = 3
)

var gametypeNames = map[int]string{
	2: "Objective",
	3: "Stopwatch",
	5: "Capture & Hold",
	6: "Last Man Standing",
	7: "Follow the Leader",
}

// ---- JSON output types ----

type AnalysisMeta struct {
	Map          string      `json:"map"`
	Gametype     int         `json:"gametype"`
	GametypeName string      `json:"gametype_name"`
	DurationMs   int32       `json:"duration_ms"`
	StartTimeMs  int32       `json:"start_time_ms"`
	EndTimeMs    int32       `json:"end_time_ms"`
	FrameCount   int         `json:"frame_count"`
	EventCount   int         `json:"event_count"`
	RecordMsec   int32       `json:"record_msec"`
	POTG         *POTGInfo   `json:"potg,omitempty"`
	MatchEndMs   int32       `json:"match_end_ms,omitempty"`
	GeneratedAt  string      `json:"generated_at"`
	MatchStartAt string      `json:"match_start_at,omitempty"`
}

type POTGInfo struct {
	Actor         int   `json:"actor"`
	Score         int32 `json:"score"`
	WindowStartMs int32 `json:"window_start_ms"`
	WindowEndMs   int32 `json:"window_end_ms"`
	ClipStartMs   int32 `json:"clip_start_ms"`
	ClipEndMs     int32 `json:"clip_end_ms"`
}

type Interval struct {
	StartMs int32 `json:"start_ms"`
	EndMs   int32 `json:"end_ms"`
}

type WeaponPeriod struct {
	StartMs int32 `json:"start_ms"`
	EndMs   int32 `json:"end_ms"`
	Weapon  int32 `json:"weapon"`
}

type HealthSample struct {
	TimeMs int32 `json:"t"`
	Health int32 `json:"h"`
}

type HPDeltaEvent struct {
	TimeMs int32 `json:"t"`
	Delta  int32 `json:"d"`
	Source int32 `json:"s"` // client num of attacker/healer; -1 = unknown (env/healthpack)
}

type PlayerInfo struct {
	DisplayName         string         `json:"display_name"`
	Team                int32          `json:"team"`
	PlayerClass         int32          `json:"player_class"` // -1 unknown, 0 soldier, 1 medic, 2 engineer, 3 lt
	AliveIntervals      []Interval     `json:"alive_intervals"`
	WeaponPeriods       []WeaponPeriod `json:"weapon_periods"`
	MaxHealth           int32          `json:"max_health"`
	HealthSamples       []HealthSample `json:"health_samples"`
	HealEvents          []HPDeltaEvent `json:"heal_events"`
	AmmoGiveEvents      []HPDeltaEvent `json:"ammo_give_events"`
	DamageEvents        []HPDeltaEvent `json:"damage_events"`
	TotalDamageDealt    int32          `json:"total_damage_dealt"`
	TotalDamageReceived int32          `json:"total_damage_received"`
	TotalHealing        int32          `json:"total_healing"`
	Kills               int32          `json:"kills"`
	Deaths              int32          `json:"deaths"`
	FinalHealth         int32          `json:"final_health"` // HP at replay end; 0 = dead/unknown
}

type AnalysisEvent struct {
	ServerTimeMs int32      `json:"server_time_ms"`
	Type         string     `json:"type"`
	Actor        int32      `json:"actor"`
	Target       int32      `json:"target"`
	MeansOfDeath int32      `json:"means_of_death"`
	Score        int32      `json:"score"`
	Extra        int32      `json:"extra,omitempty"`
	Origin       [3]float32 `json:"origin"`
}

type DamageConnection struct {
	Attacker    int32 `json:"attacker"`
	Victim      int32 `json:"victim"`
	TotalDamage int32 `json:"total_damage"`
	HitCount    int   `json:"hit_count"`
	FirstHitMs  int32 `json:"first_hit_ms"`
	LastHitMs   int32 `json:"last_hit_ms"`
}

type EngagementParticipant struct {
	ClientNum   int32 `json:"client_num"`
	Team        int32 `json:"team"`
	DamageDealt int32 `json:"damage_dealt"`  // dealt to opposing-team players this engagement
	DamageTaken int32 `json:"damage_taken"`  // received from opposing-team players this engagement
	StartHealth int32 `json:"start_health"`  // HP at engagement start (0 = unknown)
}

type Engagement struct {
	Index          int                     `json:"index"`
	StartMs        int32                   `json:"start_ms"`
	EndMs          int32                   `json:"end_ms"`
	Classification string                  `json:"classification"`
	Participants   []EngagementParticipant `json:"participants"`
	TotalDamage    int32                   `json:"total_damage"`
	HitCount       int                     `json:"hit_count"`
}

// FlagCarry tracks a single continuous flag-hold period.
// PW_REDFLAG=12, PW_BLUEFLAG=13 (powerup_t from bg_public.h).
type FlagCarry struct {
	CarrierClientNum int32  `json:"carrier_client_num"`
	FlagType         int32  `json:"flag_type"`  // 12=red flag, 13=blue flag
	StartMs          int32  `json:"start_ms"`
	EndMs            int32  `json:"end_ms"`     // 0 = still active at replay end
	Outcome          string `json:"outcome"`    // "captured", "dropped", "returned", "active"
}

// DynamitePlant tracks a single plant attempt from plant time to resolution.
type DynamitePlant struct {
	PlanterClientNum int32  `json:"planter_client_num"`
	DefuserClientNum int32  `json:"defuser_client_num"` // -1 if not defused
	Team             int32  `json:"team"`               // planter's team
	StartMs          int32  `json:"start_ms"`
	EndMs            int32  `json:"end_ms"`             // 0 = unresolved at replay end
	Outcome          string `json:"outcome"`            // "exploded", "defused", "active"
}

// SpawnCapture records a single spawn-point (team_WOLF_checkpoint with SPAWNPOINT
// spawnflag) touch event.  PointName comes from the header's spawnPointNames table.
type SpawnCapture struct {
	ClientNum  int32  `json:"client_num"`
	Team       int32  `json:"team"`
	TimeMs     int32  `json:"time_ms"`
	PointIndex int32  `json:"point_index"`
	PointName  string `json:"point_name"`
}

type Analysis struct {
	Meta              AnalysisMeta           `json:"meta"`
	Players           map[string]*PlayerInfo `json:"players"`
	Events            []AnalysisEvent        `json:"events"`
	DamageConnections []DamageConnection     `json:"damage_connections"`
	Engagements       []Engagement           `json:"engagements"`
	FlagCarries       []FlagCarry            `json:"flag_carries"`
	DynamitePlants    []DynamitePlant        `json:"dynamite_plants"`
	SpawnCaptures     []SpawnCapture         `json:"spawn_captures,omitempty"`
}

// stripColors removes Quake-style ^N color codes.
func stripColors(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '^' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseMatchStartAt extracts the real-world match start time from the .txt
// filename, which is formatted as replays_YYYYMMDD_HHMMSS.txt.
func parseMatchStartAt(txtPath string) string {
	base := strings.TrimSuffix(filepath.Base(txtPath), ".txt")
	parts := strings.SplitN(base, "_", 3)
	if len(parts) == 3 {
		t, err := time.ParseInLocation("20060102_150405", parts[1]+"_"+parts[2], time.UTC)
		if err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// ---- metadata parsing ----

func parseMeta(txtPath string) map[string]string {
	m := make(map[string]string)
	f, err := os.Open(txtPath)
	if err != nil {
		return m
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.IndexByte(line, '='); idx >= 0 {
			m[line[:idx]] = line[idx+1:]
		}
	}
	return m
}

// ---- alive / weapon state tracking ----

func isAlive(s *Sample) bool {
	return s.Health > 0 && s.PmType != pmDead && (s.PmFlags&pmfLimbo) == 0
}

type playerState struct {
	lastAlive      bool
	aliveStart     int32
	lastWeapon     int32
	weaponStart    int32
	lastTeam       int32
	playerClass    int32
	maxHealth      int32
	lastHealth     int32
	aliveIntervals []Interval
	weaponPeriods  []WeaponPeriod
}

func newPlayerState() *playerState {
	return &playerState{
		lastWeapon:    -1,
		playerClass:  -1,
		aliveIntervals: []Interval{},
		weaponPeriods:  []WeaponPeriod{},
	}
}

func (ps *playerState) update(s *Sample, t int32) {
	alive := isAlive(s)

	if s.Health > ps.maxHealth {
		ps.maxHealth = s.Health
	}
	if s.Health >= 0 {
		ps.lastHealth = s.Health
	}
	if s.Team != 0 {
		ps.lastTeam = s.Team
	}
	if s.PlayerClass >= 0 {
		ps.playerClass = s.PlayerClass
	}

	if alive && !ps.lastAlive {
		ps.aliveStart = t
	} else if !alive && ps.lastAlive {
		ps.aliveIntervals = append(ps.aliveIntervals, Interval{ps.aliveStart, t})
		// Close weapon period at death so it shows in the timeline for this life.
		if ps.lastWeapon >= 0 {
			ps.weaponPeriods = append(ps.weaponPeriods, WeaponPeriod{ps.weaponStart, t, ps.lastWeapon})
			ps.lastWeapon = -1 // reset so next life opens a fresh period
		}
	}
	ps.lastAlive = alive

	if alive && s.Weapon >= 0 {
		if ps.lastWeapon < 0 {
			ps.lastWeapon = s.Weapon
			ps.weaponStart = t
		} else if s.Weapon != ps.lastWeapon {
			ps.weaponPeriods = append(ps.weaponPeriods, WeaponPeriod{ps.weaponStart, t, ps.lastWeapon})
			ps.lastWeapon = s.Weapon
			ps.weaponStart = t
		}
	}
}

func (ps *playerState) finalize(endTime int32) {
	if ps.lastAlive {
		ps.aliveIntervals = append(ps.aliveIntervals, Interval{ps.aliveStart, endTime})
	}
	if ps.lastWeapon >= 0 && ps.lastAlive {
		ps.weaponPeriods = append(ps.weaponPeriods, WeaponPeriod{ps.weaponStart, endTime, ps.lastWeapon})
	}
}

// ---- damage connection derivation ----

type damageKey struct {
	attacker int32
	victim   int32
}

type damageAcc struct {
	total      int32
	hits       int
	firstHitMs int32
	lastHitMs  int32
}

func dist3(a, b [3]float32) float32 {
	dx := a[0] - b[0]
	dy := a[1] - b[1]
	dz := a[2] - b[2]
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

const damageAttributionRadius = float32(2000)

// attributeDamage builds a damage connection map from consecutive frame health drops.
// maxClients is used to exclude non-player entity samples (MG42 barrels, missiles, etc.).
func attributeDamage(frames []Frame, maxClients int32) map[damageKey]*damageAcc {
	accs := make(map[damageKey]*damageAcc)

	// Build per-client lookup for two consecutive frames.
	type clientSnapshot struct {
		health int32
		team   int32
		origin [3]float32
		alive  bool
	}

	prev := make(map[int32]clientSnapshot)

	for _, frame := range frames {
		curr := make(map[int32]clientSnapshot, len(frame.Samples))
		for i := range frame.Samples {
			s := &frame.Samples[i]
			if s.ClientNum >= maxClients {
				continue
			}
			curr[s.ClientNum] = clientSnapshot{
				health: s.Health,
				team:   s.Team,
				origin: s.Origin,
				alive:  isAlive(s),
			}
		}

		for cnum, cs := range curr {
			if cnum >= maxClients {
				continue
			}
			ps, hadPrev := prev[cnum]
			if !hadPrev || !cs.alive || !ps.alive {
				continue
			}
			drop := ps.health - cs.health
			if drop <= 0 {
				continue
			}

			// Find potential attackers: opposite team, alive, within radius.
			var candidates []int32
			for anum, as := range curr {
				if anum == cnum || anum >= maxClients || !as.alive {
					continue
				}
				if as.team == cs.team || as.team == teamSpec || as.team == teamFree || cs.team == teamSpec || cs.team == teamFree {
					continue
				}
				if dist3(as.origin, cs.origin) <= damageAttributionRadius {
					candidates = append(candidates, anum)
				}
			}
			if len(candidates) == 0 {
				continue
			}

			share := drop / int32(len(candidates))
			if share == 0 {
				share = 1
			}
			for _, anum := range candidates {
				key := damageKey{anum, cnum}
				acc := accs[key]
				if acc == nil {
					acc = &damageAcc{firstHitMs: frame.ServerTime}
					accs[key] = acc
				}
				acc.total += share
				acc.hits++
				acc.lastHitMs = frame.ServerTime
			}
		}

		prev = curr
	}

	return accs
}

// ---- exact damage event helpers ----

func hasDamageEvents(events []Event) bool {
	for i := range events {
		if EventType(events[i].Type) == EventDamage {
			return true
		}
	}
	return false
}

func buildDamageFromEvents(events []Event, maxClients int32) map[damageKey]*damageAcc {
	accs := make(map[damageKey]*damageAcc)
	for i := range events {
		ev := &events[i]
		if EventType(ev.Type) != EventDamage {
			continue
		}
		if ev.ActorClientNum < 0 || ev.ActorClientNum >= maxClients {
			continue
		}
		if ev.TargetClientNum < 0 || ev.TargetClientNum >= maxClients {
			continue
		}
		key := damageKey{attacker: ev.ActorClientNum, victim: ev.TargetClientNum}
		acc := accs[key]
		if acc == nil {
			acc = &damageAcc{firstHitMs: ev.ServerTime}
			accs[key] = acc
		}
		acc.total += ev.Score
		acc.hits++
		acc.lastHitMs = ev.ServerTime
	}
	return accs
}

// ---- engagement computation ----

const engagementGapMs = int32(5000)

// healthAt returns the health of clientNum at time t using a sorted [(t,h)...] slice.
// Returns the last sample at or before t, or 0 if none.
type hSample struct{ t, h int32 }

func healthAt(samples []hSample, t int32) int32 {
	// Binary search for the last sample with s.t <= t.
	lo, hi := 0, len(samples)
	for lo < hi {
		mid := (lo + hi) / 2
		if samples[mid].t <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0
	}
	return samples[lo-1].h
}

// computeEngagements groups exact DAMAGE events into time-windowed engagement clusters.
// Events separated by more than engagementGapMs with no shared participant close the window.
// Requires playerTeams map (clientNum → team) for classification and damage_dealt/taken.
// healthSamples maps clientNum → sorted health timeseries for start-health lookup.
func computeEngagements(events []Event, maxClients int32, playerTeams map[int32]int32, healthSamples map[int32][]hSample) []Engagement {
	type openEng struct {
		startMs    int32
		lastMs     int32
		totalDmg   int32
		hitCount   int
		dmgDealt   map[int32]int32 // attacker → total
		dmgTaken   map[int32]int32 // victim → total
		members    map[int32]bool  // all client nums
	}

	var open []*openEng
	var closed []Engagement

	isValid := func(n int32) bool { return n >= 0 && n < maxClients }

	for i := range events {
		ev := &events[i]
		if EventType(ev.Type) != EventDamage {
			continue
		}
		if !isValid(ev.ActorClientNum) || !isValid(ev.TargetClientNum) {
			continue
		}
		a, v := ev.ActorClientNum, ev.TargetClientNum

		// Close stale open engagements into closed list.
		remaining := open[:0]
		for _, g := range open {
			if ev.ServerTime-g.lastMs > engagementGapMs {
				if g.hitCount >= 2 {
					closed = append(closed, finalizeEngagement(g.startMs, g.lastMs, g.totalDmg, g.hitCount, g.dmgDealt, g.dmgTaken, g.members, playerTeams, healthSamples, len(closed)))
				}
			} else {
				remaining = append(remaining, g)
			}
		}
		open = remaining

		// Find all open engagements that share a participant with this event.
		// Multiple matches mean two previously separate fight threads are now connected
		// (e.g. A∈eng1 damages D∈eng2), so we merge them all into one.
		var matchIdxs []int
		for idx, g := range open {
			if g.members[a] || g.members[v] {
				matchIdxs = append(matchIdxs, idx)
			}
		}

		var primary *openEng
		if len(matchIdxs) == 0 {
			primary = &openEng{
				startMs:  ev.ServerTime,
				dmgDealt: make(map[int32]int32),
				dmgTaken: make(map[int32]int32),
				members:  make(map[int32]bool),
			}
			open = append(open, primary)
		} else {
			primary = open[matchIdxs[0]]
			// Merge any additional matched engagements into primary, then remove them.
			if len(matchIdxs) > 1 {
				for _, idx := range matchIdxs[1:] {
					src := open[idx]
					if src.startMs < primary.startMs {
						primary.startMs = src.startMs
					}
					if src.lastMs > primary.lastMs {
						primary.lastMs = src.lastMs
					}
					primary.totalDmg += src.totalDmg
					primary.hitCount += src.hitCount
					for k, d := range src.dmgDealt { primary.dmgDealt[k] += d }
					for k, d := range src.dmgTaken { primary.dmgTaken[k] += d }
					for k := range src.members   { primary.members[k] = true }
				}
				// Rebuild open without the merged-away engagements (keep index 0 match).
				keep := open[:0]
				mergedSet := make(map[int]bool, len(matchIdxs)-1)
				for _, idx := range matchIdxs[1:] { mergedSet[idx] = true }
				for idx, g := range open {
					if !mergedSet[idx] {
						keep = append(keep, g)
					}
				}
				open = keep
			}
		}

		primary.lastMs = ev.ServerTime
		primary.totalDmg += ev.Score
		primary.hitCount++
		primary.dmgDealt[a] += ev.Score
		primary.dmgTaken[v] += ev.Score
		primary.members[a] = true
		primary.members[v] = true
	}

	// Flush remaining open engagements.
	for _, g := range open {
		if g.hitCount >= 2 {
			closed = append(closed, finalizeEngagement(g.startMs, g.lastMs, g.totalDmg, g.hitCount, g.dmgDealt, g.dmgTaken, g.members, playerTeams, healthSamples, len(closed)))
		}
	}

	return closed
}

func finalizeEngagement(startMs, endMs, totalDmg int32, hitCount int,
	dmgDealt, dmgTaken map[int32]int32, members map[int32]bool,
	playerTeams map[int32]int32, healthSamples map[int32][]hSample, index int) Engagement {

	var parts []EngagementParticipant
	for cnum := range members {
		parts = append(parts, EngagementParticipant{
			ClientNum:   cnum,
			Team:        playerTeams[cnum],
			DamageDealt: dmgDealt[cnum],
			DamageTaken: dmgTaken[cnum],
			StartHealth: healthAt(healthSamples[cnum], startMs),
		})
	}

	classification := classifyEngagement(parts)

	return Engagement{
		Index:          index,
		StartMs:        startMs,
		EndMs:          endMs,
		Classification: classification,
		Participants:   parts,
		TotalDamage:    totalDmg,
		HitCount:       hitCount,
	}
}

func classifyEngagement(parts []EngagementParticipant) string {
	// Only consider real combat teams (1=Axis, 2=Allies). Team 0/3 are unassigned/spectators.
	// Count every unique player per team who either dealt OR took damage (no double-counting —
	// each participant appears once in the slice regardless of whether they did both).
	byTeam := make(map[int32]int)
	for _, p := range parts {
		if p.Team != 1 && p.Team != 2 {
			continue
		}
		byTeam[p.Team]++
	}

	if len(byTeam) < 2 {
		return "skirmish"
	}

	var sides []int
	for _, count := range byTeam {
		sides = append(sides, count)
	}
	sort.Ints(sides) // a ≤ b

	a, b := sides[0], sides[len(sides)-1]
	switch {
	case a == 1 && b == 1:
		return "1v1"
	case a == 1 && b == 2:
		return "focus_fire"
	case a == 1 && b >= 3:
		return "ambush"
	default:
		return "team_fight"
	}
}

// ---- clutch detection ----

const clutchMaxStartHP = int32(19) // < 20 HP

// rocketMODs: panzerfaust / rocket / mortar — needs 3+ kills to qualify for clutch.
var rocketMODs = map[int32]bool{
	6: true, 7: true,   // MOD_ROCKET, MOD_ROCKET_SPLASH
	29: true, 30: true, // MOD_PANZERFAUST, MOD_ROCKET_LAUNCHER
	40: true, 41: true, // MOD_MORTAR, MOD_MORTAR_SPLASH
}

// grenadeMODs: hand grenade / launcher — needs 2+ kills to qualify for clutch.
var grenadeMODs = map[int32]bool{
	4: true, 5: true,   // MOD_GRENADE, MOD_GRENADE_SPLASH
	31: true, 38: true, // MOD_GRENADE_LAUNCHER, MOD_GRENADE_PINEAPPLE
}

func isKillEventType(t EventType) bool {
	switch t {
	case EventKill, EventHeadshot, EventExplosiveKill, EventKnifeKill, EventMultikill, EventTeamkill:
		return true
	}
	return false
}

func clutchKillsQualify(mods []int32) bool {
	var rockets, grenades int
	for _, m := range mods {
		if rocketMODs[m] {
			rockets++
		}
		if grenadeMODs[m] {
			grenades++
		}
	}
	total := len(mods)
	if rockets > 0 && total < 3 {
		return false
	}
	if grenades > 0 && rockets == 0 && total < 2 {
		return false
	}
	return true
}

// detectClutch post-processes engagements to reclassify low-HP scenarios:
//   - Solo player (<20 HP) who kills all opponents with valid weapons (+ survives if <3 kills) → "clutch"
//   - Solo player (<20 HP) who loses the fight → "skirmish"
func detectClutch(engs []Engagement, events []Event) []Engagement {
	result := make([]Engagement, len(engs))
	copy(result, engs)

	for i := range result {
		eng := &result[i]

		// Collect participants by team (combat teams only).
		teamMap := make(map[int32][]EngagementParticipant)
		for _, p := range eng.Participants {
			if p.Team != 1 && p.Team != 2 {
				continue
			}
			teamMap[p.Team] = append(teamMap[p.Team], p)
		}
		if len(teamMap) != 2 {
			continue
		}

		// Build a sorted [small, large] team slice.
		type teamEntry struct {
			id    int32
			parts []EngagementParticipant
		}
		var teams [2]teamEntry
		ti := 0
		for id, parts := range teamMap {
			teams[ti] = teamEntry{id, parts}
			ti++
		}
		if len(teams[0].parts) > len(teams[1].parts) {
			teams[0], teams[1] = teams[1], teams[0]
		}

		// Only handle scenarios where one side has exactly 1 player vs 1-3 opponents.
		if len(teams[0].parts) != 1 || len(teams[1].parts) > 3 {
			continue
		}

		solo := teams[0].parts[0]
		opponents := teams[1].parts

		// Only apply HP-based logic when solo player entered low.
		if solo.StartHealth <= 0 || solo.StartHealth > clutchMaxStartHP {
			continue
		}

		// Build opponent set.
		oppSet := make(map[int32]bool, len(opponents))
		for _, op := range opponents {
			oppSet[op.ClientNum] = true
		}

		// Gather kills made by solo player against opponents within the engagement window.
		type killRec struct{ t, mod int32 }
		var soloKills []killRec
		var lastKillMs int32
		for j := range events {
			ev := &events[j]
			if ev.ServerTime < eng.StartMs || ev.ServerTime > eng.EndMs+1000 {
				continue
			}
			if !isKillEventType(ev.Type) {
				continue
			}
			if ev.ActorClientNum == solo.ClientNum && oppSet[ev.TargetClientNum] {
				soloKills = append(soloKills, killRec{ev.ServerTime, ev.MeansOfDeath})
				if ev.ServerTime > lastKillMs {
					lastKillMs = ev.ServerTime
				}
			}
		}

		// Low-HP player lost (didn't kill all opponents) → skirmish regardless of original label.
		if len(soloKills) < len(opponents) {
			eng.Classification = "skirmish"
			continue
		}

		// Weapon thresholds.
		mods := make([]int32, len(soloKills))
		for k, kr := range soloKills {
			mods[k] = kr.mod
		}
		if !clutchKillsQualify(mods) {
			continue
		}

		// Survival check: required when < 3 kills (triple kill allows death).
		if len(soloKills) < 3 {
			died := false
			for j := range events {
				ev := &events[j]
				if ev.ServerTime < lastKillMs || ev.ServerTime > eng.EndMs+2000 {
					continue
				}
				if ev.TargetClientNum == solo.ClientNum && isKillEventType(ev.Type) {
					died = true
					break
				}
			}
			if died {
				continue
			}
		}

		eng.Classification = "clutch"
	}

	return result
}

// ---- flag carry computation ----

// computeFlagCarries scans objective events and builds carry periods.
// extra field on STEAL/RETURN events holds the powerup giTag (PW_REDFLAG=12, PW_BLUEFLAG=13).
// Objective-mode items have giTag=0; we fall back to team heuristic:
//   stealer BLUE(Allies)→12, stealer RED(Axis)→13
//   returner RED(Axis)→12,   returner BLUE(Allies)→13
func computeFlagCarries(events []Event, playerTeams map[int32]int32, replayEndMs int32, dynamiteExplosions map[int]bool) []FlagCarry {
	// open carries: flagType → *FlagCarry (only one carry per flag at a time)
	open := make(map[int32]*FlagCarry)
	var carries []FlagCarry

	flagTypeForSteal := func(extra, actorTeam int32) int32 {
		if extra != 0 {
			return extra
		}
		if actorTeam == teamBlue {
			return 12 // Allies carrying Axis objective → PW_REDFLAG
		}
		if actorTeam == teamRed {
			return 13 // Axis carrying Allied objective → PW_BLUEFLAG
		}
		return 0
	}
	flagTypeForReturn := func(extra, actorTeam int32) int32 {
		if extra != 0 {
			return extra
		}
		if actorTeam == teamRed {
			return 12 // Axis returning their own objective (Allies had PW_REDFLAG)
		}
		if actorTeam == teamBlue {
			return 13 // Allies returning their own objective (Axis had PW_BLUEFLAG)
		}
		return 0
	}

	for i := range events {
		ev := &events[i]
		switch ev.Type {
		case EventObjectiveSteal:
			flagType := flagTypeForSteal(ev.Extra, playerTeams[ev.ActorClientNum])
			// Close any prior open carry for this flag (shouldn't happen, but be safe).
			if prev, ok := open[flagType]; ok {
				prev.EndMs = ev.ServerTime
				prev.Outcome = "dropped"
				carries = append(carries, *prev)
			}
			c := &FlagCarry{
				CarrierClientNum: ev.ActorClientNum,
				FlagType:         flagType,
				StartMs:          ev.ServerTime,
			}
			open[flagType] = c

		case EventObjectiveCapture:
			// Skip captures that are really dynamite explosions.
			if dynamiteExplosions[i] {
				break
			}
			// Find which flag this carrier had. If we have an open carry for this actor, close it.
			closed := false
			for flagType, c := range open {
				if c.CarrierClientNum == ev.ActorClientNum {
					c.EndMs = ev.ServerTime
					c.Outcome = "captured"
					carries = append(carries, *c)
					delete(open, flagType)
					closed = true
					break
				}
			}
			// No open carry → spawn-flag or area capture: emit a zero-duration entry.
			if !closed {
				carries = append(carries, FlagCarry{
					CarrierClientNum: ev.ActorClientNum,
					FlagType:         0, // spawn/area capture — no specific flag powerup
					StartMs:          ev.ServerTime,
					EndMs:            ev.ServerTime,
					Outcome:          "captured",
				})
			}

		case EventObjectiveReturn:
			flagType := flagTypeForReturn(ev.Extra, playerTeams[ev.ActorClientNum])
			if c, ok := open[flagType]; ok {
				c.EndMs = ev.ServerTime
				if ev.TargetClientNum >= 0 {
					// Carrier was killed — flag dropped.
					c.Outcome = "dropped"
				} else {
					// Defender walked the flag back to base.
					c.Outcome = "returned"
				}
				carries = append(carries, *c)
				delete(open, flagType)
			}
		}
	}

	// Any still-open carry at replay end is "active".
	for _, c := range open {
		c.EndMs = replayEndMs
		c.Outcome = "active"
		carries = append(carries, *c)
	}
	return carries
}

// ---- dynamite plant computation ----

func computeDynamitePlants(events []Event, playerTeams map[int32]int32, replayEndMs int32) ([]DynamitePlant, map[int]bool) {
	// We track open plants as a stack (multiple plants can be active simultaneously in theory).
	var open []*DynamitePlant
	var plants []DynamitePlant
	explosionIndices := make(map[int]bool)

	closeFirst := func(outcome string, t int32, defuser int32) {
		if len(open) == 0 {
			return
		}
		// Close the oldest open plant.
		p := open[0]
		open = open[1:]
		p.EndMs = t
		p.Outcome = outcome
		p.DefuserClientNum = defuser
		plants = append(plants, *p)
	}

	for i := range events {
		ev := &events[i]
		switch ev.Type {
		case EventObjectivePlant:
			p := &DynamitePlant{
				PlanterClientNum: ev.ActorClientNum,
				DefuserClientNum: -1,
				Team:             playerTeams[ev.ActorClientNum],
				StartMs:          ev.ServerTime,
			}
			open = append(open, p)

		case EventObjectiveDefuse:
			closeFirst("defused", ev.ServerTime, ev.ActorClientNum)

		case EventObjectiveCapture:
			// A capture while a plant is open means the dynamite exploded.
			if len(open) > 0 {
				closeFirst("exploded", ev.ServerTime, -1)
				explosionIndices[i] = true
			}
		}
	}

	for _, p := range open {
		p.EndMs = replayEndMs
		p.Outcome = "active"
		plants = append(plants, *p)
	}
	return plants, explosionIndices
}

// ---- main analysis function ----

// Analyze derives a full Analysis from a parsed Replay and companion .txt metadata.
func Analyze(r *Replay, txtPath string) *Analysis {
	meta := parseMeta(txtPath)

	a := &Analysis{
		Players:           make(map[string]*PlayerInfo),
		Events:            []AnalysisEvent{},
		DamageConnections: []DamageConnection{},
		Engagements:       []Engagement{},
	}

	// --- meta ---
	var startMs, endMs int32
	if len(r.Frames) > 0 {
		startMs = r.Frames[0].ServerTime
		endMs = r.Frames[len(r.Frames)-1].ServerTime
	}

	gtName := gametypeNames[int(r.Header.Gametype)]
	if gtName == "" {
		gtName = strconv.Itoa(int(r.Header.Gametype))
	}

	a.Meta = AnalysisMeta{
		Map:          r.Header.MapName,
		Gametype:     int(r.Header.Gametype),
		GametypeName: gtName,
		DurationMs:   endMs - startMs,
		StartTimeMs:  startMs,
		EndTimeMs:    endMs,
		FrameCount:   len(r.Frames),
		EventCount:   len(r.Events),
		RecordMsec:   r.Header.RecordMsec,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		MatchStartAt: parseMatchStartAt(txtPath),
	}

	// POTG from metadata
	if meta["selectionTarget"] != "" && meta["selectionTarget"] != "-1" {
		actor, _ := strconv.Atoi(meta["selectionTarget"])
		score64, _ := strconv.ParseInt(meta["selectionScore"], 10, 32)
		wStart, _ := strconv.ParseInt(meta["selectionWindowStart"], 10, 32)
		wEnd, _ := strconv.ParseInt(meta["selectionWindowEnd"], 10, 32)
		cStart, _ := strconv.ParseInt(meta["selectionClipStart"], 10, 32)
		cEnd, _ := strconv.ParseInt(meta["selectionClipEnd"], 10, 32)
		a.Meta.POTG = &POTGInfo{
			Actor:         actor,
			Score:         int32(score64),
			WindowStartMs: int32(wStart),
			WindowEndMs:   int32(wEnd),
			ClipStartMs:   int32(cStart),
			ClipEndMs:     int32(cEnd),
		}
	}

	// --- per-player state tracking ---
	// Only track samples for actual player slots (clientNum < maxClients).
	// World entities (MG42 barrels, missiles, items) also appear in frames but
	// have entity numbers >= maxClients and should be excluded here.
	maxClients := r.Header.MaxClients
	states := make(map[int32]*playerState)

	for _, frame := range r.Frames {
		for i := range frame.Samples {
			s := &frame.Samples[i]
			if s.ClientNum >= maxClients {
				continue
			}
			ps := states[s.ClientNum]
			if ps == nil {
				ps = newPlayerState()
				states[s.ClientNum] = ps
			}
			ps.update(s, frame.ServerTime)
		}
	}

	for cnum, ps := range states {
		ps.finalize(endMs)
		key := strconv.Itoa(int(cnum))
		name := "Player " + key
		if raw, ok := meta["player_"+key]; ok && raw != "" {
			// Completed match: use the .txt metadata (authoritative).
			name = stripColors(strings.TrimSpace(raw))
			if name == "" {
				name = "Player " + key
			}
		} else if int(cnum) < len(r.Header.PlayerNames) && r.Header.PlayerNames[cnum] != "" {
			// Live match: use the name embedded in the v5 archive header.
			name = r.Header.PlayerNames[cnum]
		}
		a.Players[key] = &PlayerInfo{
			DisplayName:    name,
			Team:           ps.lastTeam,
			PlayerClass:    ps.playerClass,
			AliveIntervals: ps.aliveIntervals,
			WeaponPeriods:  ps.weaponPeriods,
			MaxHealth:      ps.maxHealth,
			FinalHealth:    ps.lastHealth,
		}
	}

	// --- events + kill/death tallies ---
	// Kill events: KILL, EXPLOSIVE_KILL, KNIFE_KILL, TEAMKILL, and OBJECTIVE_RETURN
	// when target >= 0 (carrier kill). HEADSHOT and MULTIKILL are bonus score events
	// for the same kill moment and must not be double-counted.
	killEventTypes := map[EventType]bool{
		EventKill: true, EventExplosiveKill: true, EventKnifeKill: true, EventTeamkill: true,
	}
	for _, ev := range r.Events {
		evType := ev.Type.String()
		isCarrierKill := ev.Type == EventObjectiveReturn && ev.TargetClientNum >= 0
		if isCarrierKill {
			evType = "OBJECTIVE_CARRIER_KILL"
		}
		a.Events = append(a.Events, AnalysisEvent{
			ServerTimeMs: ev.ServerTime,
			Type:         evType,
			Actor:        ev.ActorClientNum,
			Target:       ev.TargetClientNum,
			MeansOfDeath: ev.MeansOfDeath,
			Score:        ev.Score,
			Extra:        ev.Extra,
			Origin:       ev.Origin,
		})

		isKill := killEventTypes[ev.Type] || isCarrierKill
		isSuicide := ev.Type == EventSuicide
		if isKill || isSuicide {
			actor := ev.ActorClientNum
			target := ev.TargetClientNum
			if isCarrierKill {
				target = ev.TargetClientNum
			}
			if isKill && actor != target {
				if pi, ok := a.Players[strconv.Itoa(int(actor))]; ok {
					pi.Kills++
				}
			}
			victim := target
			if isSuicide {
				victim = actor
			}
			if pi, ok := a.Players[strconv.Itoa(int(victim))]; ok {
				pi.Deaths++
			}
		}
	}

	// --- damage connections ---
	// If the replay contains exact DAMAGE events (emitted by the mod when a player
	// damages another player), use those directly.  Fall back to the proximity
	// heuristic only when no such events exist (older replays).
	var dmg map[damageKey]*damageAcc
	if hasDamageEvents(r.Events) {
		dmg = buildDamageFromEvents(r.Events, maxClients)
	} else {
		dmg = attributeDamage(r.Frames, maxClients)
	}
	for key, acc := range dmg {
		a.DamageConnections = append(a.DamageConnections, DamageConnection{
			Attacker:    key.attacker,
			Victim:      key.victim,
			TotalDamage: acc.total,
			HitCount:    acc.hits,
			FirstHitMs:  acc.firstHitMs,
			LastHitMs:   acc.lastHitMs,
		})
	}

	// --- engagements ---
	// Build a clientNum → team lookup from the already-resolved a.Players map.
	playerTeams := make(map[int32]int32, len(a.Players))
	for key, pi := range a.Players {
		cnum, _ := strconv.Atoi(key)
		playerTeams[int32(cnum)] = pi.Team
	}
	// Build a raw health timeseries from frames for start-health lookup.
	engHealthSamples := make(map[int32][]hSample)
	for i := range r.Frames {
		f := &r.Frames[i]
		for j := range f.Samples {
			s := &f.Samples[j]
			engHealthSamples[s.ClientNum] = append(engHealthSamples[s.ClientNum], hSample{t: f.ServerTime, h: s.Health})
		}
	}
	a.Engagements = computeEngagements(r.Events, maxClients, playerTeams, engHealthSamples)
	a.Engagements = detectClutch(a.Engagements, r.Events)

	// --- dynamite plants (must run before flag carries to identify explosion events) ---
	var dynamiteExplosions map[int]bool
	a.DynamitePlants, dynamiteExplosions = computeDynamitePlants(r.Events, playerTeams, endMs)

	// --- flag carries ---
	a.FlagCarries = computeFlagCarries(r.Events, playerTeams, endMs, dynamiteExplosions)

	// --- match end ---
	for i := range r.Events {
		if EventType(r.Events[i].Type) == EventMatchEnd {
			a.Meta.MatchEndMs = r.Events[i].ServerTime
			break
		}
	}

	// --- spawn captures ---
	for i := range r.Events {
		ev := &r.Events[i]
		if EventType(ev.Type) != EventSpawnCapture {
			continue
		}
		name := ""
		if int(ev.Extra) < len(r.Header.SpawnPointNames) {
			name = r.Header.SpawnPointNames[ev.Extra]
		}
		a.SpawnCaptures = append(a.SpawnCaptures, SpawnCapture{
			ClientNum:  ev.ActorClientNum,
			Team:       playerTeams[ev.ActorClientNum],
			TimeMs:     ev.ServerTime,
			PointIndex: ev.Extra,
			PointName:  name,
		})
	}

	// --- health time series, heal/damage events with source attribution ---
	// Downsample health to 1 sample per 1000ms. Attribute HP drops using exact
	// DAMAGE events when available (mod v5+), falling back to a proximity search
	// for older replays. HP gains are attributed via MEDPACK_PICKUP events.
	const healthSampleIntervalMs = int32(1000)

	// Build per-victim index of exact damage events: victim → sorted list.
	type exactDmgEntry struct{ timeMs, attacker, amount int32 }
	exactDmgIndex := make(map[int32][]exactDmgEntry)
	for i := range r.Events {
		ev := &r.Events[i]
		if EventType(ev.Type) != EventDamage {
			continue
		}
		if ev.TargetClientNum >= 0 && ev.TargetClientNum < maxClients &&
			ev.ActorClientNum >= 0 && ev.ActorClientNum < maxClients {
			exactDmgIndex[ev.TargetClientNum] = append(
				exactDmgIndex[ev.TargetClientNum],
				exactDmgEntry{ev.ServerTime, ev.ActorClientNum, ev.Score},
			)
		}
	}
	useExactDamage := len(exactDmgIndex) > 0

	// Pre-index MEDPACK_PICKUP events by recipient for fast heal attribution.
	// Each entry is (serverTime, medicClientNum).
	type medpackEntry struct{ timeMs, medic int32 }
	medpackIndex := make(map[int32][]medpackEntry)
	// Collect AMMO_GIVE events keyed by the LT (actor) for per-player ammo_give_events.
	ammoGiveByLT := make(map[int32][]HPDeltaEvent)
	for i := range r.Events {
		ev := &r.Events[i]
		if EventType(ev.Type) == EventMedpackPickup {
			if ev.TargetClientNum >= 0 {
				medpackIndex[ev.TargetClientNum] = append(
					medpackIndex[ev.TargetClientNum],
					medpackEntry{ev.ServerTime, ev.ActorClientNum},
				)
			}
			continue
		}
		if EventType(ev.Type) == EventAmmoGive {
			ammoGiveByLT[ev.ActorClientNum] = append(
				ammoGiveByLT[ev.ActorClientNum],
				HPDeltaEvent{TimeMs: ev.ServerTime, Delta: ev.Score, Source: ev.TargetClientNum},
			)
		}
	}

	type hpTrack struct {
		lastHealth   int32
		lastAlive    bool
		nextSampleMs int32
		samples      []HealthSample
		heals        []HPDeltaEvent
		dmgEvs       []HPDeltaEvent
		totalHealing int32
		totalDmgRecv int32
	}

	hpTracks := make(map[int32]*hpTrack)

	// Snapshot of every client within the current frame — used for source attribution.
	type clientSnap struct {
		health int32
		team   int32
		weapon int32
		origin [3]float32
		alive  bool
	}

	var prevSnap map[int32]clientSnap

	for _, frame := range r.Frames {
		t := frame.ServerTime

		// Build current frame snapshot (player slots only).
		currSnap := make(map[int32]clientSnap, len(frame.Samples))
		for i := range frame.Samples {
			s := &frame.Samples[i]
			if s.ClientNum >= maxClients {
				continue
			}
			currSnap[s.ClientNum] = clientSnap{
				health: s.Health,
				team:   s.Team,
				weapon: s.Weapon,
				origin: s.Origin,
				alive:  isAlive(s),
			}
		}

		for cnum, cs := range currSnap {
			ht := hpTracks[cnum]
			if ht == nil {
				ht = &hpTrack{
					nextSampleMs: t,
					samples:      []HealthSample{},
					heals:        []HPDeltaEvent{},
					dmgEvs:       []HPDeltaEvent{},
				}
				hpTracks[cnum] = ht
			}

			if cs.alive {
				if ht.lastAlive {
					delta := cs.health - ht.lastHealth
					if delta > 0 {
						// Health gain: find the MEDPACK_PICKUP event closest in time (≤200ms)
						// for this player. The mod emits this event when a player picks up
						// a thrown health pack, carrying the throwing medic's client number.
						source := int32(-1)
						bestDt := int32(201)
						for _, mp := range medpackIndex[cnum] {
							dt := mp.timeMs - t
							if dt < 0 {
								dt = -dt
							}
							if dt < bestDt {
								bestDt = dt
								source = mp.medic
							}
						}
						ht.heals = append(ht.heals, HPDeltaEvent{t, delta, source})
						ht.totalHealing += delta
					} else if delta < 0 {
						// Health drop: attribute using exact DAMAGE events when present,
						// otherwise fall back to proximity search.
						source := int32(-1)
						if useExactDamage {
							// Find the damage event for this victim closest in time to t.
							best := int32(200) // within 200ms window (4 frames at 50ms)
							for _, de := range exactDmgIndex[cnum] {
								dt := de.timeMs - t
								if dt < 0 {
									dt = -dt
								}
								if dt < best {
									best = dt
									source = de.attacker
								}
							}
						} else {
							minD := damageAttributionRadius + 1
							for anum, as := range currSnap {
								if anum == cnum || !as.alive {
									continue
								}
								if as.team == cs.team || as.team == teamSpec || as.team == teamFree ||
									cs.team == teamSpec || cs.team == teamFree {
									continue
								}
								if d := dist3(as.origin, cs.origin); d <= damageAttributionRadius {
									if d < minD || (d == minD && (source < 0 || anum < source)) {
										minD = d
										source = anum
									}
								}
							}
						}
						ht.dmgEvs = append(ht.dmgEvs, HPDeltaEvent{t, -delta, source})
						ht.totalDmgRecv += -delta
					}
				}
				// Downsample health for sparkline.
				if t >= ht.nextSampleMs {
					ht.samples = append(ht.samples, HealthSample{t, cs.health})
					ht.nextSampleMs = t + healthSampleIntervalMs
				}
			} else if ht.lastAlive {
				// Player just died — h=0 sentinel breaks the sparkline polyline so
				// the JS flush() doesn't draw a diagonal from death HP to respawn HP.
				ht.samples = append(ht.samples, HealthSample{t, 0})
				ht.nextSampleMs = t + healthSampleIntervalMs
			}

			ht.lastHealth = cs.health
			ht.lastAlive = cs.alive
		}

		// Ensure players not present in this frame still advance their hpTrack.
		for cnum, ht := range hpTracks {
			if _, seen := currSnap[cnum]; !seen {
				ht.lastAlive = false
			}
		}

		prevSnap = currSnap
		_ = prevSnap // kept for future use
	}

	// Derive total damage dealt per player from damage connections.
	dmgDealt := make(map[int32]int32)
	for key, acc := range dmg {
		dmgDealt[key.attacker] += acc.total
	}

	for cnum, ht := range hpTracks {
		key := strconv.Itoa(int(cnum))
		pi := a.Players[key]
		if pi == nil {
			continue
		}
		pi.HealthSamples = ht.samples
		pi.HealEvents = ht.heals
		pi.AmmoGiveEvents = ammoGiveByLT[cnum]
		pi.DamageEvents = ht.dmgEvs
		pi.TotalHealing = ht.totalHealing
		pi.TotalDamageReceived = ht.totalDmgRecv
		pi.TotalDamageDealt = dmgDealt[cnum]
	}

	return a
}

// MarshalJSON serializes an Analysis to JSON bytes.
func MarshalJSON(a *Analysis) ([]byte, error) {
	return json.MarshalIndent(a, "", "  ")
}
