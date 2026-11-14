package stats

import (
	"context"
	"errors"
	"sort"
	"time"
)

// ErrNoPlayer is returned for a player key with no games.
var ErrNoPlayer = errors.New("stats: no such player")

// PlayerGame is one game of a player: what they did in it and how it ended for them.
type PlayerGame struct {
	Match     string     `json:"match"` // the name the game is stored (and linked) under
	Map       string     `json:"map"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	ServerID  string     `json:"server_id,omitempty"` // empty: the local (official) server
	Pool      string     `json:"pool"`
	Team      string     `json:"team,omitempty"` // as of the last round they played
	Result    string     `json:"result"`         // win | loss | draw (the game had no winner) | left (gone before the last round ended)
	Kills     int        `json:"kills"`
	Deaths    int        `json:"deaths"`
	Score     int        `json:"score"`
	PlayMs    int        `json:"play_ms"`
	KillStrk  int        `json:"kill_streak"`
}

// PlayerTotals is everything a player did in one pool, added up over their games.
type PlayerTotals struct {
	Pool           string        `json:"pool"`
	Games          int           `json:"games"`
	Wins           int           `json:"wins"`
	Losses         int           `json:"losses"`
	Draws          int           `json:"draws"`
	Kills          int           `json:"kills"`
	Deaths         int           `json:"deaths"`
	Efficiency     int           `json:"eff"`
	Suicides       int           `json:"suicides"`
	TeamKills      int           `json:"teamkills"`
	Gibs           int           `json:"gibs"`
	Revives        int           `json:"revives"`
	DamageGiven    int           `json:"damage_given"`
	DamageReceived int           `json:"damage_received"`
	Score          int           `json:"score"`
	PlayMs         int           `json:"play_ms"`
	Headshots      int           `json:"headshots"`
	Records        PlayerRecords `json:"records"`
	Weapons        []WeaponStats `json:"weapons"`
}

// PlayerRecords are the player's bests in one pool.
type PlayerRecords struct {
	KillStreak  int          `json:"kill_streak"`            // most kills in a row without dying, in any one game
	BestScore   int          `json:"best_score"`             // the highest score in one game
	MostKills   int          `json:"most_kills"`             // the most kills in one game
	LongestKill *LongestKill `json:"longest_kill,omitempty"` // the farthest hitscan kill
}

// PlayerSummary is a player's name and their totals, one entry per pool they have played in.
type PlayerSummary struct {
	PID   string         `json:"pid"`
	Name  string         `json:"name"` // the name they last played under
	Pools []PlayerTotals `json:"pools"`
}

type playerGameRow struct {
	PlayerGame
	suicides, teamkills, gibs, revives, dmgGiven, dmgRecv int
	longest                                               LongestKill
}

// maxPlayerGames bounds how many games one player's history reads (the newest).
const maxPlayerGames = 20000

// playerGames loads a player's games, newest first.  The result of a game is judged by the last round of the game: the player's team
// then against that round's winner (a stopwatch game's second round decides it).
func (s *Store) playerGames(ctx context.Context, pid string) ([]playerGameRow, string, error) {
	rows, err := s.pool.Query(ctx, `
		WITH mine AS (SELECT DISTINCT game_id FROM game_players WHERE pid = $1),
		     lr   AS (SELECT p.game_id, max(p.round) AS r FROM game_players p JOIN mine m ON m.game_id = p.game_id GROUP BY p.game_id)
		SELECT g.match, g.map, g.started_at, g.server_id, g.pool,
		       (array_agg(p.team ORDER BY p.round DESC, p.id DESC))[1],
		       (array_agg(p.team ORDER BY p.id DESC) FILTER (WHERE p.round = lr.r))[1],
		       (array_agg(p.event ORDER BY p.id DESC) FILTER (WHERE p.round = lr.r))[1],
		       (SELECT winner FROM game_rounds w WHERE w.game_id = g.id AND w.round = lr.r),
		       sum(p.kills), sum(p.deaths), sum(p.score), sum(p.play_ms), max(p.kill_streak),
		       sum(p.suicides), sum(p.teamkills), sum(p.gibs), sum(p.revives), sum(p.damage_given), sum(p.damage_received),
		       max(p.longest_kill), (array_agg(p.longest_kill_weapon ORDER BY p.longest_kill DESC, p.id))[1],
		       (array_agg(p.longest_kill_victim ORDER BY p.longest_kill DESC, p.id))[1],
		       (array_agg(p.name ORDER BY p.id DESC))[1]
		FROM game_players p JOIN games g ON g.id = p.game_id JOIN lr ON lr.game_id = g.id
		WHERE p.pid = $1
		GROUP BY g.id, g.match, g.map, g.started_at, g.server_id, g.pool, lr.r
		ORDER BY g.started_at DESC NULLS LAST, g.id DESC
		LIMIT $2`, pid, maxPlayerGames)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []playerGameRow
	var lastName string
	for rows.Next() {
		var r playerGameRow
		var lastTeam, finalTeam, finalEvent, winner *string
		var name string
		if err := rows.Scan(&r.Match, &r.Map, &r.StartedAt, &r.ServerID, &r.Pool, &lastTeam, &finalTeam, &finalEvent, &winner,
			&r.Kills, &r.Deaths, &r.Score, &r.PlayMs, &r.KillStrk,
			&r.suicides, &r.teamkills, &r.gibs, &r.revives, &r.dmgGiven, &r.dmgRecv,
			&r.longest.Dist, &r.longest.Weapon, &r.longest.Victim, &name); err != nil {
			return nil, "", err
		}
		if lastTeam != nil {
			r.Team = *lastTeam
		}
		switch {
		case finalTeam == nil || (finalEvent != nil && *finalEvent == "disconnect"):
			r.Result = "left"
		case winner == nil:
			r.Result = "draw"
		case *winner == *finalTeam:
			r.Result = "win"
		default:
			r.Result = "loss"
		}
		if lastName == "" {
			lastName = name // newest game first
		}
		out = append(out, r)
	}
	return out, lastName, rows.Err()
}

// Player is a player's totals per pool.  Returns ErrNoPlayer when the key has no games.
func (s *Store) Player(ctx context.Context, pid string) (*PlayerSummary, error) {
	games, name, err := s.playerGames(ctx, pid)
	if err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, ErrNoPlayer
	}
	sum := &PlayerSummary{PID: pid, Name: name}
	byPool := map[string]*PlayerTotals{}
	for _, g := range games {
		t := byPool[g.Pool]
		if t == nil {
			t = &PlayerTotals{Pool: g.Pool, Weapons: []WeaponStats{}}
			byPool[g.Pool] = t
		}
		t.Games++
		switch g.Result {
		case "win":
			t.Wins++
		case "loss":
			t.Losses++
		case "draw":
			t.Draws++
		}
		t.Kills += g.Kills
		t.Deaths += g.Deaths
		t.Suicides += g.suicides
		t.TeamKills += g.teamkills
		t.Gibs += g.gibs
		t.Revives += g.revives
		t.DamageGiven += g.dmgGiven
		t.DamageReceived += g.dmgRecv
		t.Score += g.Score
		t.PlayMs += g.PlayMs
		r := &t.Records
		if g.KillStrk > r.KillStreak {
			r.KillStreak = g.KillStrk
		}
		if g.Score > r.BestScore {
			r.BestScore = g.Score
		}
		if g.Kills > r.MostKills {
			r.MostKills = g.Kills
		}
		if g.longest.Dist > 0 && (r.LongestKill == nil || g.longest.Dist > r.LongestKill.Dist) {
			lk := g.longest
			r.LongestKill = &lk
		}
	}
	wrows, err := s.pool.Query(ctx, `
		SELECT g.pool, w.weapon, sum(w.hits), sum(w.atts), sum(w.kills), sum(w.deaths), sum(w.headshots)
		FROM game_player_weapons w JOIN game_players p ON p.id = w.player_id JOIN games g ON g.id = p.game_id
		WHERE p.pid = $1 GROUP BY g.pool, w.weapon`, pid)
	if err != nil {
		return nil, err
	}
	defer wrows.Close()
	for wrows.Next() {
		var pool string
		var w WeaponStats
		if err := wrows.Scan(&pool, &w.Name, &w.Hits, &w.Atts, &w.Kills, &w.Deaths, &w.Headshots); err != nil {
			return nil, err
		}
		if t := byPool[pool]; t != nil {
			t.Weapons = append(t.Weapons, w)
			t.Headshots += w.Headshots
		}
	}
	if err := wrows.Err(); err != nil {
		return nil, err
	}
	rank := map[string]int{}
	for i, n := range WeaponOrder {
		rank[n] = i
	}
	for _, t := range byPool {
		t.Efficiency = Efficiency(t.Kills, t.Deaths)
		sort.Slice(t.Weapons, func(a, b int) bool { return rank[t.Weapons[a].Name] < rank[t.Weapons[b].Name] })
		sum.Pools = append(sum.Pools, *t)
	}
	sort.Slice(sum.Pools, func(a, b int) bool { // official first
		if sum.Pools[a].Pool == PoolOfficial || sum.Pools[b].Pool == PoolOfficial {
			return sum.Pools[a].Pool == PoolOfficial
		}
		return sum.Pools[a].Pool < sum.Pools[b].Pool
	})
	return sum, nil
}

// PlayerHistory is a page of a player's games, newest first; pool "" means every pool.  total counts all of them.
func (s *Store) PlayerHistory(ctx context.Context, pid, pool string, limit, offset int) (games []PlayerGame, total int, err error) {
	rows, _, err := s.playerGames(ctx, pid)
	if err != nil {
		return nil, 0, err
	}
	var all []PlayerGame
	for _, r := range rows {
		if pool == "" || r.Pool == pool {
			all = append(all, r.PlayerGame)
		}
	}
	total = len(all)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}
