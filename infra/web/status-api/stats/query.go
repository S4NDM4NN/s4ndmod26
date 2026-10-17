package stats

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// WeaponStats is OSP's five numbers for one weapon.
type WeaponStats struct {
	Name      string `json:"name"`
	Hits      int    `json:"hits"`
	Atts      int    `json:"atts"`
	Kills     int    `json:"kills"`
	Deaths    int    `json:"deaths"`
	Headshots int    `json:"headshots"`
}

// PlayerStats is one player's totals for the game (every round they played, added up).
type PlayerStats struct {
	Slot           int           `json:"slot"`
	Name           string        `json:"name"`
	Bot            bool          `json:"bot"`
	Team           string        `json:"team"`  // axis | allies, as of their last stats line
	Class          int           `json:"class"` // likewise
	Rounds         int           `json:"rounds"`
	Kills          int           `json:"kills"`
	Deaths         int           `json:"deaths"`
	Suicides       int           `json:"suicides"`
	TeamKills      int           `json:"teamkills"`
	Efficiency     int           `json:"eff"`
	Gibs           int           `json:"gibs"`
	Revives        int           `json:"revives"`
	DamageGiven    int           `json:"damage_given"`
	DamageReceived int           `json:"damage_received"`
	TeamDamage     int           `json:"team_damage"`
	Score          int           `json:"score"`
	Weapons        []WeaponStats `json:"weapons"`
}

// GameStats is everything the per-game stats page shows.
type GameStats struct {
	Match       string        `json:"match"`
	Map         string        `json:"map"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	Rounds      int           `json:"rounds"`
	Players     []PlayerStats `json:"players"`
	TopShots    []TopShot     `json:"topshots"`
	BottomShots []TopShot     `json:"bottomshots"`
}

// ErrNoGame is returned for a match nothing has been ingested for.
var ErrNoGame = errors.New("no stats for that game")

// Game loads one game's stats.
func (s *Store) Game(ctx context.Context, match string) (*GameStats, error) {
	g := &GameStats{Match: match}
	var gid int64
	err := s.pool.QueryRow(ctx, `SELECT id, map, started_at FROM games WHERE match = $1`, match).Scan(&gid, &g.Map, &g.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoGame
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT slot, name, bot,
		       (array_agg(team  ORDER BY round DESC, id DESC))[1],
		       (array_agg(class ORDER BY round DESC, id DESC))[1],
		       count(DISTINCT round), max(round),
		       sum(kills), sum(deaths), sum(suicides), sum(teamkills), sum(gibs), sum(revives),
		       sum(damage_given), sum(damage_received), sum(team_damage), sum(score)
		FROM game_players WHERE game_id = $1
		GROUP BY slot, name, bot`, gid)
	if err != nil {
		return nil, err
	}
	type key struct {
		slot int
		name string
		bot  bool
	}
	index := map[key]int{}
	for rows.Next() {
		var p PlayerStats
		var maxRound int
		if err := rows.Scan(&p.Slot, &p.Name, &p.Bot, &p.Team, &p.Class, &p.Rounds, &maxRound,
			&p.Kills, &p.Deaths, &p.Suicides, &p.TeamKills, &p.Gibs, &p.Revives,
			&p.DamageGiven, &p.DamageReceived, &p.TeamDamage, &p.Score); err != nil {
			rows.Close()
			return nil, err
		}
		if maxRound > g.Rounds {
			g.Rounds = maxRound
		}
		p.Efficiency = Efficiency(p.Kills, p.Deaths)
		p.Weapons = []WeaponStats{}
		index[key{p.Slot, p.Name, p.Bot}] = len(g.Players)
		g.Players = append(g.Players, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	wrows, err := s.pool.Query(ctx, `
		SELECT p.slot, p.name, p.bot, w.weapon, sum(w.hits), sum(w.atts), sum(w.kills), sum(w.deaths), sum(w.headshots)
		FROM game_player_weapons w JOIN game_players p ON p.id = w.player_id
		WHERE p.game_id = $1
		GROUP BY p.slot, p.name, p.bot, w.weapon`, gid)
	if err != nil {
		return nil, err
	}
	defer wrows.Close()
	for wrows.Next() {
		var k key
		var w WeaponStats
		if err := wrows.Scan(&k.slot, &k.name, &k.bot, &w.Name, &w.Hits, &w.Atts, &w.Kills, &w.Deaths, &w.Headshots); err != nil {
			return nil, err
		}
		if i, ok := index[k]; ok {
			g.Players[i].Weapons = append(g.Players[i].Weapons, w)
		}
	}
	if err := wrows.Err(); err != nil {
		return nil, err
	}

	weaponRank := map[string]int{}
	for i, n := range WeaponOrder {
		weaponRank[n] = i
	}
	for i := range g.Players {
		ws := g.Players[i].Weapons
		sort.Slice(ws, func(a, b int) bool { return weaponRank[ws[a].Name] < weaponRank[ws[b].Name] })
	}
	// teams together (axis first), best score first
	sort.SliceStable(g.Players, func(a, b int) bool {
		pa, pb := g.Players[a], g.Players[b]
		if pa.Team != pb.Team {
			return pa.Team == "axis"
		}
		if pa.Score != pb.Score {
			return pa.Score > pb.Score
		}
		if pa.Kills != pb.Kills {
			return pa.Kills > pb.Kills
		}
		return pa.Name < pb.Name
	})
	g.TopShots = TopShots(g.Players, true, TopShotsMinAtts)
	g.BottomShots = TopShots(g.Players, false, TopShotsMinAtts)
	return g, nil
}

// GamesWithStats lists the matches that have stats ingested.
func (s *Store) GamesWithStats(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT match FROM games`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out[m] = true
	}
	return out, rows.Err()
}
