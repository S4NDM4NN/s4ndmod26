package replay

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
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
	DisplayName        string         `json:"display_name"`
	Team               int32          `json:"team"`
	AliveIntervals     []Interval     `json:"alive_intervals"`
	WeaponPeriods      []WeaponPeriod `json:"weapon_periods"`
	MaxHealth          int32          `json:"max_health"`
	HealthSamples      []HealthSample `json:"health_samples"`
	HealEvents         []HPDeltaEvent `json:"heal_events"`
	DamageEvents       []HPDeltaEvent `json:"damage_events"`
	TotalDamageDealt   int32          `json:"total_damage_dealt"`
	TotalDamageReceived int32         `json:"total_damage_received"`
	TotalHealing       int32          `json:"total_healing"`
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

type Analysis struct {
	Meta               AnalysisMeta              `json:"meta"`
	Players            map[string]*PlayerInfo    `json:"players"`
	Events             []AnalysisEvent           `json:"events"`
	DamageConnections  []DamageConnection        `json:"damage_connections"`
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
	maxHealth      int32
	aliveIntervals []Interval
	weaponPeriods  []WeaponPeriod
}

func newPlayerState() *playerState {
	return &playerState{
		lastWeapon:    -1,
		aliveIntervals: []Interval{},
		weaponPeriods:  []WeaponPeriod{},
	}
}

func (ps *playerState) update(s *Sample, t int32) {
	alive := isAlive(s)

	if s.Health > ps.maxHealth {
		ps.maxHealth = s.Health
	}
	if s.Team != 0 {
		ps.lastTeam = s.Team
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

// ---- main analysis function ----

// Analyze derives a full Analysis from a parsed Replay and companion .txt metadata.
func Analyze(r *Replay, txtPath string) *Analysis {
	meta := parseMeta(txtPath)

	a := &Analysis{
		Players:            make(map[string]*PlayerInfo),
		Events:             []AnalysisEvent{},
		DamageConnections:  []DamageConnection{},
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
			AliveIntervals: ps.aliveIntervals,
			WeaponPeriods:  ps.weaponPeriods,
			MaxHealth:      ps.maxHealth,
		}
	}

	// --- events ---
	for _, ev := range r.Events {
		evType := ev.Type.String()
		// OBJECTIVE_RETURN fires for two distinct actions in the C code:
		//   target >= 0 → attacker killed the flag carrier (flag drops, can be re-picked)
		//   target == -1 → defender physically walked to dropped flag and returned it to base
		if ev.Type == EventObjectiveReturn && ev.TargetClientNum >= 0 {
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
	}

	// --- damage connections ---
	dmg := attributeDamage(r.Frames, maxClients)
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

	// --- health time series, heal/damage events with source attribution ---
	// Downsample health to 1 sample per 1000ms. Attribute HP drops to the closest
	// enemy within damageAttributionRadius. HP gains are attributed via
	// MEDPACK_PICKUP events (actor=medic, target=patient) emitted by the mod when
	// a player picks up a thrown health pack; source == -1 means medic self-regen
	// or a pack with no recorded thrower.
	const healthSampleIntervalMs = int32(1000)

	// Pre-index MEDPACK_PICKUP events by recipient for fast heal attribution.
	// Each entry is (serverTime, medicClientNum).
	type medpackEntry struct{ timeMs, medic int32 }
	medpackIndex := make(map[int32][]medpackEntry)
	for i := range r.Events {
		ev := &r.Events[i]
		if EventType(ev.Type) != EventMedpackPickup {
			continue
		}
		if ev.TargetClientNum >= 0 {
			medpackIndex[ev.TargetClientNum] = append(
				medpackIndex[ev.TargetClientNum],
				medpackEntry{ev.ServerTime, ev.ActorClientNum},
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
						// Health drop: find closest enemy within attribution radius.
						source := int32(-1)
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
