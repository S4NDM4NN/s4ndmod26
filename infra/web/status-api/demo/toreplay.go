package demo

import (
	"sort"
	"strconv"
	"strings"

	"s4ndmod26/status-api/replay"
)

// Spike: convert one client demo into the replay.Replay that the status-api
// analysis and viewer consume.  A client demo only carries what the
// recording client could see, so several fields are approximations; the
// Report says how much of the result is measured and how much is guessed.
const (
	etPlayerType = 1
	efDead       = 0x00000001
	statHealth   = 0
	pmDeadType   = 2
	entityWorld  = 1022
)

// Report describes the fidelity of a conversion.
type Report struct {
	Frames        int
	PlayerSamples int
	// OwnSamples are frames with a sample built from the playerState (the
	// recorder, or whoever they follow): exact health, velocity and angles.
	OwnSamples int
	// Recorder is the client slot that made the demo; a Spectator recorder
	// (e.g. a shoutcaster) is a camera, not a player, and gets no sample.
	Recorder  int
	Spectator bool
	// ObituaryType is the eType the obituary events were found under (0 if
	// none was convincing, in which case no kill events are produced).
	ObituaryType int
	// Obituaries is how many kill-style events were reconstructed;
	// WarmupObituaries were left out because the match was not being played.
	Obituaries, WarmupObituaries int
	// DeadSamples are other players flagged EF_DEAD.  Their health is unknown
	// (entityState carries none), so alive players are given a placeholder.
	DeadSamples int
	Players     int
	// Health says how much of the health and class in the result was read
	// from the demo; Health.Drops are the losses of health seen (damage
	// received, attacker unknown).
	Health HealthReport
	// FleshType is the eType the bullet-hit events were found under (0 if
	// none was convincing, in which case damage has no shooter).
	FleshType int
	Damage    DamageReport
	// Objectives are the announced objective events (plants, thefts,
	// captures ...) with who did them where that could be told; Carries are
	// the stretches players carried the objective.
	Objectives []Objective
	Carries    []Carry
}

// conv is one demo converted, before health is resolved.  real[i] has bit n
// set when frame i's sample of client n came from a playerState.
type conv struct {
	rep    *replay.Replay
	report Report
	real   []uint64
	hp     *healthData
	ph     []phaseChange
	hits   []hit // bullet hits on players, in this demo's clock
	flight []flight
	carry  []carryObs // who held the objective flag when, as this demo saw it
	cps    []cpMsg    // objective messages
}

// ToReplay converts a demo.  It reads Frames, so the demo must have been
// parsed with at least KeepPlayersEvents.
func ToReplay(d *Demo) (*replay.Replay, Report) {
	c := convert(d)
	c.rep.Events = dedupeEvents(c.rep.Events)
	c.report.Health = applyHealth(c.rep, c.real, c.hp, func(t int) bool { return live(c.ph, t) })
	evs, dr := attributeDamage(c.report.Health.Drops, unionHits([][]hit{c.hits}))
	xev, xhp := attributeExplosions(c.rep, dr.Left, unionFlights([][]flight{c.flight}))
	dr.Explosive, dr.Unattributed = xhp, dr.Unattributed-xhp
	c.report.Damage = dr
	c.report.Carries = buildCarries(append([]carryObs(nil), c.carry...))
	c.report.Objectives = resolveObjectives(unionCP([][]cpMsg{c.cps}), c.report.Carries)
	c.rep.Events = append(append(c.rep.Events, evs...), xev...)
	sort.SliceStable(c.rep.Events, func(i, j int) bool { return c.rep.Events[i].ServerTime < c.rep.Events[j].ServerTime })
	return c.rep, c.report
}

// convert turns a demo into a replay whose health values are not yet
// resolved (see applyHealth).
func convert(d *Demo) *conv {
	var rep Report
	hd := newHealthData()
	for _, c := range d.Commands {
		if strings.HasPrefix(c.Text, "tinfo ") && c.ServerTime >= 0 {
			hd.addTinfo(c.ServerTime, c.Text)
		}
	}
	var realMask []uint64
	pcl := d.Layout.PlayerIdx("clientNum")
	info := Describe(d)
	lay := d.Layout
	ent := func(n string) int { return lay.EntityIdx(n) }
	ix, it, ie := ent("pos.trBase[0]"), ent("eType"), ent("eFlags")
	iv, ia, iw, itm := ent("pos.trDelta[0]"), ent("apos.trBase[0]"), ent("weapon"), ent("teamNum")
	pOrg, pVel, pVA := lay.PlayerIdx("origin[0]"), lay.PlayerIdx("velocity[0]"), lay.PlayerIdx("viewangles[0]")
	pType, pFlags, pWS := lay.PlayerIdx("pm_type"), lay.PlayerIdx("pm_flags"), lay.PlayerIdx("weaponstate")
	pWeap := lay.PlayerIdx("weapon")
	iOther, iOther2, iParm := ent("otherEntityNum"), ent("otherEntityNum2"), ent("eventParm")
	lastOrigin := map[int][3]float32{}
	ph := phases(d)
	teams := buildTeamTimeline(d)
	obitType := detectObituary(d)
	rep.ObituaryType = obitType
	fleshType := detectFlesh(d, obitType)
	rep.FleshType = fleshType
	var hits []hit
	hitRun := map[int]bool{} // flesh-hit entities seen in the previous frame
	iWeapon, iBase, iDelta, iTrTime := ent("weapon"), ent("pos.trBase[0]"), ent("pos.trDelta[0]"), ent("pos.trTime")
	iPowerups := ent("powerups")
	lastCarry := map[int]uint32{}
	var carryObservations []carryObs
	runs := map[int]*flight{} // missiles in flight
	var flights []flight
	obit := map[int]bool{} // event entities that were an obituary in the previous frame

	h := replay.ArchiveHeader{
		Version:    8,
		MaxClients: maxClients,
		MapName:    info.Map,
	}
	h.Gametype = int32(atoi(InfoValue(d.Configstrings[csServerInfo], "g_gametype")))
	for n, p := range info.Players {
		if n < len(h.PlayerNames) {
			h.PlayerNames[n] = p.Name
		}
	}
	rep.Players = len(info.Players)
	rep.Recorder, rep.Spectator = d.ClientNum, info.Spectator

	f3 := func(w []uint32, i int) [3]float32 {
		return [3]float32{float32(Float(w[i])), float32(Float(w[i+1])), float32(Float(w[i+2]))}
	}

	out := &replay.Replay{Header: h}
	var gaps []int
	for fi := range d.Frames {
		fr := &d.Frames[fi]
		if fi > 0 {
			gaps = append(gaps, fr.ServerTime-d.Frames[fi-1].ServerTime)
		}
		f := replay.Frame{ServerTime: int32(fr.ServerTime)}
		nowObit := map[int]bool{}
		nowHit := map[int]bool{}
		seenMissile := map[int]bool{}
		for ei := range fr.Entities {
			e := &fr.Entities[ei]
			if e.Number >= maxClients && int(e.F[it]) == etMissile {
				seenMissile[e.Number] = true
				fl := runs[e.Number]
				if fl == nil {
					b := f3(e.F[:], iBase)
					fl = &flight{num: e.Number, wp: int(e.F[iWeapon]), first: int32(fr.ServerTime),
						launchT: int32(e.F[iTrTime]), lx: b[0], ly: b[1], lz: b[2]}
					runs[e.Number] = fl
				}
				fl.last = int32(fr.ServerTime)
				// linear extrapolation from the current trajectory base
				b, v := f3(e.F[:], iBase), f3(e.F[:], iDelta)
				dt := float32(fr.ServerTime-int(int32(e.F[iTrTime]))) / 1000
				fl.ex, fl.ey, fl.ez = b[0]+v[0]*dt, b[1]+v[1]*dt, b[2]+v[2]*dt
				continue
			}
			if fleshType != 0 && e.Number >= maxClients && int(e.F[it]) == fleshType {
				nowHit[e.Number] = true
				if v, a := int(e.F[iParm]), int(e.F[iOther]); !hitRun[e.Number] && v < maxClients && a < maxClients && live(ph, fr.ServerTime) {
					hits = append(hits, hit{int32(fr.ServerTime), int32(v), int32(a)})
				}
				continue
			}
			if obitType != 0 && e.Number >= maxClients && int(e.F[it]) == obitType {
				// a temp entity lives for several snapshots; report it once
				nowObit[e.Number] = true
				if !obit[e.Number] {
					if live(ph, fr.ServerTime) {
						out.Events = append(out.Events, obituary(e, iOther, iOther2, iParm, fr.ServerTime, teams, lastOrigin))
						rep.Obituaries++
					} else {
						rep.WarmupObituaries++
					}
				}
				continue
			}
			if e.Number >= maxClients || int(e.F[it]) != etPlayerType {
				continue
			}
			s := replay.Sample{
				ClientNum:   int32(e.Number),
				Team:        int32(e.F[itm]),
				Weapon:      int32(e.F[iw]),
				PlayerClass: -1,
				Origin:      f3(e.F[:], ix),
				Velocity:    f3(e.F[:], iv),
				ViewAngles:  f3(e.F[:], ia),
				EType:       etPlayerType,
				Health:      100, // placeholder: not in entityState
			}
			// entityState.teamNum is not the 1 axis / 2 allies team (it
			// disagrees within one team), so use the roster configstring
			// as it stood at this moment
			if tm := teams.at(e.Number, fr.ServerTime); tm != 0 {
				s.Team = int32(tm)
			}
			if e.F[ie]&efDead != 0 {
				s.Health, s.PmType = 0, pmDeadType
				rep.DeadSamples++
			}
			lastOrigin[e.Number] = s.Origin
			if iPowerups >= 0 {
				if m := e.F[iPowerups] & carryMask; m != lastCarry[e.Number] {
					lastCarry[e.Number] = m
					carryObservations = append(carryObservations, carryObs{int32(fr.ServerTime), int32(e.Number), m})
				}
			}
			f.Samples = append(f.Samples, s)
			rep.PlayerSamples++
		}
		obit, hitRun = nowObit, nowHit
		for n, fl := range runs {
			if !seenMissile[n] {
				flights = append(flights, *fl)
				delete(runs, n)
			}
		}
		// The playerState belongs to ps.clientNum: the recorder while alive,
		// but the player they follow after dying.  It carries exact health
		// and angles; the server leaves the recorder's own entity out of
		// the snapshot, and a followed player's entity sample is replaced.
		var mask uint64
		owner := d.ClientNum
		if pcl >= 0 {
			owner = int(fr.PS.F[pcl])
		}
		if owner >= 0 && owner < maxClients && !(info.Spectator && owner == d.ClientNum) {
			ps := &fr.PS
			s := replay.Sample{
				ClientNum:   int32(owner),
				Health:      ps.Stats[statHealth],
				PlayerClass: -1,
				Origin:      f3(ps.F[:], pOrg),
				Velocity:    f3(ps.F[:], pVel),
				ViewAngles:  f3(ps.F[:], pVA),
				PmType:      int32(ps.F[pType]),
				PmFlags:     int32(ps.F[pFlags]),
				Weaponstate: int32(ps.F[pWS]),
				Weapon:      int32(ps.F[pWeap]),
				EType:       etPlayerType,
			}
			s.Team = int32(teams.at(owner, fr.ServerTime))
			hd.addExact(owner, fr.ServerTime, s.Health)
			var pm uint32
			if ps.Powerups[pwRedFlag] != 0 {
				pm |= 1 << pwRedFlag
			}
			if ps.Powerups[pwBlueFlag] != 0 {
				pm |= 1 << pwBlueFlag
			}
			if pm != lastCarry[owner] {
				lastCarry[owner] = pm
				carryObservations = append(carryObservations, carryObs{int32(fr.ServerTime), int32(owner), pm})
			}
			if s.Health < 0 {
				s.Health = 0
			}
			replaced := false
			for i := range f.Samples {
				if f.Samples[i].ClientNum == s.ClientNum {
					f.Samples[i], replaced = s, true
					break
				}
			}
			if !replaced {
				f.Samples = append(f.Samples, s)
				rep.PlayerSamples++
			}
			rep.OwnSamples++
			mask = 1 << uint(owner)
		}
		realMask = append(realMask, mask)
		out.Frames = append(out.Frames, f)
	}
	if len(gaps) > 0 {
		sort.Ints(gaps)
		out.Header.RecordMsec = int32(gaps[len(gaps)/2])
	}
	out.Header.FrameCount = int32(len(out.Frames))
	rep.Frames = len(out.Frames)
	for _, fl := range runs {
		flights = append(flights, *fl)
	}
	return &conv{rep: out, report: rep, real: realMask, hp: hd, ph: ph, hits: hits, flight: flights,
		carry: carryObservations, cps: objectiveMessages(d)}
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// obituary turns an EV_OBITUARY temp entity into a kill-style event.  The
// game sets otherEntityNum to the victim, otherEntityNum2 to the killer
// (the world for environmental deaths) and eventParm to the means of death.
func obituary(e *EntityState, iOther, iOther2, iParm, t int, teams teamTimeline, origin map[int][3]float32) replay.Event {
	victim, killer := int(e.F[iOther]), int(e.F[iOther2])
	ev := replay.Event{
		ServerTime:      int32(t),
		ActorClientNum:  int32(killer),
		TargetClientNum: int32(victim),
		Type:            replay.EventKill,
		MeansOfDeath:    int32(e.F[iParm]),
		InflictorEntNum: -1,
		LaunchEntNum:    -1,
		Origin:          origin[victim],
		AttackerOrigin:  origin[killer],
	}
	if killer == victim || killer >= maxClients {
		ev.Type = replay.EventSuicide
		ev.ActorClientNum = int32(victim)
	} else if at := teams.at(killer, t); at != 0 && at == teams.at(victim, t) {
		ev.Type = replay.EventTeamkill
	}
	return ev
}

// Event numbering differs between protocols (and mod builds): the obituary
// is eType 125 in dm_60 demos and 123 in dm_59.  Rather than keep a table, find
// it from the data.  The obituary temp entity names the victim in
// otherEntityNum, so among event entities it is the type whose victim most
// often is a player who is flagged EF_DEAD right then.  Other kinds that
// touch a player (a bullet hitting flesh) score a few percent; obituaries
// score 50% or more (the rest are deaths out of the recorder's sight).
const (
	obitMinShare  = 0.30
	obitMinEvents = 5
	obitBeforeMs  = 300
	obitAfterMs   = 600
)

func detectObituary(d *Demo) int {
	lay := d.Layout
	it, ie, io := lay.EntityIdx("eType"), lay.EntityIdx("eFlags"), lay.EntityIdx("otherEntityNum")
	if it < 0 || ie < 0 || io < 0 {
		return 0
	}
	dead := map[int]bool{}
	deaths := map[int][]int{} // victim -> times EF_DEAD turned on
	for fi := range d.Frames {
		fr := &d.Frames[fi]
		for ei := range fr.Entities {
			e := &fr.Entities[ei]
			if e.Number < maxClients && int(e.F[it]) == etPlayerType {
				isDead := e.F[ie]&efDead != 0
				if isDead && !dead[e.Number] {
					deaths[e.Number] = append(deaths[e.Number], fr.ServerTime)
				}
				dead[e.Number] = isDead
			}
		}
	}
	type score struct{ frames, near, runs int }
	scores := map[int]*score{}
	prev := map[int]int{}
	for fi := range d.Frames {
		fr := &d.Frames[fi]
		cur := map[int]int{}
		for ei := range fr.Entities {
			e := &fr.Entities[ei]
			et := int(e.F[it])
			if e.Number < maxClients || et < etEvents {
				continue
			}
			sc := scores[et]
			if sc == nil {
				sc = &score{}
				scores[et] = sc
			}
			sc.frames++
			cur[e.Number] = et
			if prev[e.Number] != et {
				sc.runs++
			}
			for _, t := range deaths[int(e.F[io])] {
				if dt := t - fr.ServerTime; dt >= -obitBeforeMs && dt <= obitAfterMs {
					sc.near++
					break
				}
			}
		}
		prev = cur
	}
	best, bestShare := 0, obitMinShare
	for et, sc := range scores {
		share := float64(sc.near) / float64(sc.frames)
		if sc.runs >= obitMinEvents && share >= bestShare {
			best, bestShare = et, share
		}
	}
	return best
}
