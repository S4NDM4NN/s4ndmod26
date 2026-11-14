package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"s4ndmod26/status-api/demo"
	"s4ndmod26/status-api/demostore"
	"s4ndmod26/status-api/replay"
	"s4ndmod26/status-api/replayviews"
	"s4ndmod26/status-api/stats"
)

// What the viewer is given for a match: the merged demos in the shape
// /api/games/{match}/paths returns, plus the event feed, the rounds and the
// shot lines the demo viewer shows.

// userPathsResponse is /api/games/{match}/paths plus the event feed the page
// shows (the game server builds that from its stats; here it comes from the
// events reconstructed out of the demos) and how the merge went.
type userPathsResponse struct {
	replayviews.PathsResponse
	Feed   []userFeedEvent `json:"feed"`
	Rounds []userRound     `json:"rounds"`
	// Shots are the bullets fired, drawn as lines from the shooter; they end
	// at a hit where one was found.
	Shots  []replay.Shot   `json:"shots"`
	Source userPathsSource `json:"source"`
	// Stats are the counts the demos give reliably (see demo.BuildStats).
	Stats *demo.MatchStats `json:"stats"`
}

// userRound is one stretch of live play on the replay's clock.  Time between
// rounds (warm-up, intermission, scoreboard) is not in the replay.  The
// game's clock for the round reads limit_ms - (t - clock_start_ms).
type userRound struct {
	Game         int   `json:"game"`
	N            int   `json:"n"`
	Of           int   `json:"of"`
	StartMs      int32 `json:"start_ms"`
	EndMs        int32 `json:"end_ms"`
	BeatMs       int32 `json:"beat_ms,omitempty"` // the time to beat in the second round of a stopwatch match
	LimitMs      int32 `json:"limit_ms"`
	ClockStartMs int32 `json:"clock_start_ms"`
	// Reinforcement waves: a team's dead come back at clock_start_ms + phase + k * limbo, so the time to the next is
	// limbo - (t - clock_start_ms - phase) mod limbo.  Left out where the demos did not show it.
	AxisLimboMs   int32 `json:"axis_limbo_ms,omitempty"`
	AxisPhaseMs   int32 `json:"axis_phase_ms,omitempty"`
	AlliesLimboMs int32 `json:"allies_limbo_ms,omitempty"`
	AlliesPhaseMs int32 `json:"allies_phase_ms,omitempty"`
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
	// Weapon is what a kill was made with (MP-40, Grenade, Falling ...) or, for a strike, Airstrike or Artillery.
	Weapon string `json:"weapon,omitempty"`
}

// buildFeed turns the merged replay's kills into the feed.  Teams are read
// from the players' samples at the time of the event.
func buildFeed(rep *replay.Replay, startMs int32, objectives []demo.Objective, rounds []demo.Round, ents *replay.Entities) []userFeedEvent {
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
		weapon := ""
		if typ != "revive" {
			weapon = stats.ModName(e.MeansOfDeath) // "killed X with MP-40"; the same table the game stats feed uses
		}
		out = append(out, userFeedEvent{
			TMs: e.ServerTime - startMs, Type: typ, Weapon: weapon,
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
	// Support: a client demo has no event for these, so they are read from the entities.  A pack taken is the drop's end, with the
	// player who dropped it as far as it can be told; a strike is the smoke canister somebody threw.
	for _, d := range ents.Drops {
		if d.Death || d.Fate != "taken" {
			continue
		}
		typ, text := "ammo", "Ammo pack taken"
		if d.Kind == "medpack" {
			typ, text = "medpack", "Medic pack taken"
		}
		pos := d.At
		out = append(out, userFeedEvent{TMs: d.T1 - startMs, Type: typ, Text: text,
			Actor: name(d.Slot), ActorTeam: team(d.Slot, d.T0), ActorSlot: d.Slot, TargetSlot: -1, Pos: &pos})
	}
	for _, pr := range ents.Projectiles {
		if pr.Kind != "marker" || pr.Slot < 0 || len(pr.Pts) < 4 {
			continue
		}
		pos := [3]int32{pr.Pts[len(pr.Pts)-3], pr.Pts[len(pr.Pts)-2], pr.Pts[len(pr.Pts)-1]}
		out = append(out, userFeedEvent{TMs: pr.T0 - startMs, Type: "strike", Weapon: map[int32]string{45: "Airstrike", 46: "Artillery"}[pr.Weapon],
			Actor: name(pr.Slot), ActorTeam: team(pr.Slot, pr.T0), ActorSlot: pr.Slot, TargetSlot: -1, Pos: &pos})
	}
	mmss := func(ms int32) string { return fmt.Sprintf("%d:%02d", ms/60000, ms/1000%60) }
	games := false
	for _, r := range rounds {
		games = games || r.Game > 1
	}
	for i, r := range rounds {
		label := fmt.Sprintf("Round %d of %d", r.N, r.Of)
		if games {
			label = fmt.Sprintf("Game %d, %s", max(r.Game, 1), label)
		}
		begin := label + " begins"
		if r.N > 1 {
			begin += ": the sides have swapped"
		}
		if r.BeatMs > 0 {
			begin += ", time to beat " + mmss(r.BeatMs)
		}
		end := label + " ends"
		if r.LimitMs > 0 {
			end += ", " + mmss(r.LimitMs-(r.EndMs-r.ClockStartMs)) + " left on the clock"
			// a stopwatch match is about the time set: the first round says what it set, the second what it was up against, even when
			// it beat it
			switch {
			case r.BeatMs > 0:
				end += " (time to beat " + mmss(r.BeatMs) + ")"
			case r.Of > 1 && r.N < r.Of:
				set := min(r.LimitMs, r.EndMs-r.ClockStartMs)
				if i+1 < len(rounds) && rounds[i+1].Game == r.Game && rounds[i+1].BeatMs > 0 {
					set = rounds[i+1].BeatMs // the game's own figure, exact
				}
				end += " (time set " + mmss(set) + ")"
			}
		}
		out = append(out,
			userFeedEvent{TMs: r.StartMs - startMs, Type: "round", Text: begin, TargetSlot: -1, ActorSlot: -1},
			userFeedEvent{TMs: r.EndMs - startMs, Type: "round", Text: end, TargetSlot: -1, ActorSlot: -1})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TMs < out[j].TMs })
	return out
}

type userPathsSource struct {
	Match string             `json:"match"`
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

// buildMatchPaths merges a match's demos (load hands them over one at a time,
// so only two are parsed at once) and builds what the viewer reads.
func buildMatchPaths(matchID string, members []demostore.Demo, load func(i int) (*demo.Demo, error)) ([]byte, error) {
	ref := 0
	for i, m := range members {
		if m.Span() > members[ref].Span() {
			ref = i
		}
	}
	rep, mr, err := demo.MergeStream(len(members), ref, load)
	if err != nil {
		return nil, fmt.Errorf("could not merge that match: %w", err)
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
	ents := replay.BuildEntities(rep, replay.PathOptions{})
	stats := demo.BuildStats(rep, rounds, objectives, carries)
	for i := range stats.Ticks { // on the clock the feed uses: from the start of the replay
		stats.Ticks[i].TMs -= p.StartMs
	}
	resp := userPathsResponse{Stats: stats, PathsResponse: replayviews.PathsResponse{Paths: p, Entities: ents, Carries: []replayviews.ViewCarry{}},
		Feed: buildFeed(rep, p.StartMs, objectives, rounds, ents), Rounds: []userRound{}, Shots: replay.BuildShots(rep, replay.PathOptions{})}
	for _, r := range rounds {
		resp.Rounds = append(resp.Rounds, userRound{max(r.Game, 1), r.N, r.Of, r.StartMs, r.EndMs, r.BeatMs, r.LimitMs, r.ClockStartMs,
			r.Axis.LimboMs, r.Axis.PhaseMs, r.Allies.LimboMs, r.Allies.PhaseMs})
	}
	for _, c := range carries {
		resp.Carries = append(resp.Carries, replayviews.ViewCarry{Slot: c.Slot, StartMs: c.StartMs, EndMs: c.EndMs})
	}
	for _, pp := range p.Players {
		name := demo.CleanName(rep.Header.PlayerNames[pp.Slot%len(rep.Header.PlayerNames)])
		if name == "" {
			name = "Player"
		}
		resp.Players = append(resp.Players, replayviews.PathsPlayer{PlayerPath: pp, Name: name})
	}
	kills := 0
	for _, e := range rep.Events {
		if e.Type == replay.EventKill {
			kills++
		}
	}
	resp.Source = userPathsSource{Match: matchID, Parts: mr.Parts, Stats: userPathsMergeStat{
		Frames: mr.Frames, AvgPlayers: mr.AvgPlayers, PlayersWithExact: mr.OwnHealthPlayers, Kills: kills,
		HealthLostHp: mr.Damage.Attributed + mr.Damage.Explosive + mr.Damage.Unattributed, AttributedHp: mr.Damage.Attributed + mr.Damage.Explosive,
	}}
	return json.Marshal(resp)
}
