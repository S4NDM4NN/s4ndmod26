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
	Slot           int              `json:"slot"`
	Name           string           `json:"name"`
	NameRaw        string           `json:"name_raw,omitempty"` // with colour codes, when the game wrote them
	Bot            bool             `json:"bot"`
	Team           string           `json:"team"`  // axis | allies, as of their last stats line
	Class          int              `json:"class"` // likewise
	Rounds         int              `json:"rounds"`
	Kills          int              `json:"kills"`
	Deaths         int              `json:"deaths"`
	Suicides       int              `json:"suicides"`
	TeamKills      int              `json:"teamkills"`
	Efficiency     int              `json:"eff"`
	Gibs           int              `json:"gibs"`
	Revives        int              `json:"revives"`
	DamageGiven    int              `json:"damage_given"`
	DamageReceived int              `json:"damage_received"`
	TeamDamage     int              `json:"team_damage"`
	Score          int              `json:"score"`
	TeamGibs       int              `json:"team_gibs"`
	Goombas        int              `json:"goombas"`
	Poisons        int              `json:"poisons"`
	KnifeKills     int              `json:"knife_kills"`
	KillStreak     int              `json:"kill_streak"`  // best over the game
	DeathStreak    int              `json:"death_streak"` // worst over the game
	DoubleKills    int              `json:"double_kills"`
	TripleKills    int              `json:"triple_kills"`
	QuadKills      int              `json:"quad_kills"`
	FirstBlood     int              `json:"first_blood"`
	FirstDeath     int              `json:"first_death"`
	PlayMs         int              `json:"play_ms"`
	Objectives     *ObjectiveCounts `json:"objectives,omitempty"`      // from the event feed, when the game has one
	Nemesis        *Rival           `json:"nemesis,omitempty"`         // who killed them most
	FavoriteVictim *Rival           `json:"favorite_victim,omitempty"` // whom they killed most
	Weapons        []WeaponStats    `json:"weapons"`
}

// ObjectiveCounts is what a player did with the map's objectives, from the game's event feed.
type ObjectiveCounts struct {
	Plants   int `json:"plants"`
	Defuses  int `json:"defuses"`
	Captures int `json:"captures"`
	Steals   int `json:"steals"`  // picked up the objective
	Returns  int `json:"returns"` // took it back
	Denials  int `json:"denials"`
}

// Total is every objective event counted.
func (o *ObjectiveCounts) Total() int {
	if o == nil {
		return 0
	}
	return o.Plants + o.Defuses + o.Captures + o.Steals + o.Returns + o.Denials
}

// RoundWinner is the team the server named as winner of a round.
type RoundWinner struct {
	Round  int    `json:"round"`
	Winner string `json:"winner"` // axis | allies
}

// Rival is another player and how many times one of them killed the other.
type Rival struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

// GameStats is everything the per-game stats page shows.
type GameStats struct {
	Match        string        `json:"match"`
	Map          string        `json:"map"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	Rounds       int           `json:"rounds"`
	Winner       string        `json:"winner,omitempty"` // the game's winner: the last round's, when the server named one
	RoundWinners []RoundWinner `json:"round_winners,omitempty"`
	Players      []PlayerStats `json:"players"`
	Awards       []Award       `json:"awards"`
	Shame        []Award       `json:"shame"`
	TopShots     []TopShot     `json:"topshots"`
	BottomShots  []TopShot     `json:"bottomshots"`
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
		       (array_agg(name_raw ORDER BY round DESC, id DESC))[1],
		       (array_agg(team  ORDER BY round DESC, id DESC))[1],
		       (array_agg(class ORDER BY round DESC, id DESC))[1],
		       count(DISTINCT round), max(round),
		       sum(kills), sum(deaths), sum(suicides), sum(teamkills), sum(gibs), sum(revives),
		       sum(damage_given), sum(damage_received), sum(team_damage), sum(score),
		       sum(team_gibs), sum(goombas), sum(poisons), sum(knife_kills), max(kill_streak), max(death_streak),
		       sum(double_kills), sum(triple_kills), sum(quad_kills), sum(first_blood), sum(first_death), sum(play_ms)
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
		if err := rows.Scan(&p.Slot, &p.Name, &p.Bot, &p.NameRaw, &p.Team, &p.Class, &p.Rounds, &maxRound,
			&p.Kills, &p.Deaths, &p.Suicides, &p.TeamKills, &p.Gibs, &p.Revives,
			&p.DamageGiven, &p.DamageReceived, &p.TeamDamage, &p.Score,
			&p.TeamGibs, &p.Goombas, &p.Poisons, &p.KnifeKills, &p.KillStreak, &p.DeathStreak,
			&p.DoubleKills, &p.TripleKills, &p.QuadKills, &p.FirstBlood, &p.FirstDeath, &p.PlayMs); err != nil {
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

	if err := s.loadRivals(ctx, gid, g.Players); err != nil {
		return nil, err
	}
	if err := s.loadObjectives(ctx, gid, g.Players); err != nil {
		return nil, err
	}
	if err := s.loadWinners(ctx, gid, g); err != nil {
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
	g.Awards, g.Shame = Awards(g.Players)
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

// loadRivals fills in each player's nemesis (who killed them most) and favorite victim (whom they killed most), from the enemy
// kills the server counted per victim.  Players are matched by slot and name, which is how the game recorded the victim.
func (s *Store) loadRivals(ctx context.Context, gid int64, players []PlayerStats) error {
	rows, err := s.pool.Query(ctx, `
		SELECT p.slot, p.name, v.victim_slot, v.victim_name, sum(v.kills)
		FROM game_player_victims v JOIN game_players p ON p.id = v.player_id
		WHERE p.game_id = $1
		GROUP BY p.slot, p.name, v.victim_slot, v.victim_name`, gid)
	if err != nil {
		return err
	}
	defer rows.Close()

	type who struct {
		slot int
		name string
	}
	idx := map[who]int{}
	for i, p := range players {
		idx[who{p.Slot, p.Name}] = i
	}
	favorite := map[int]Rival{}
	nemesis := map[int]Rival{}
	better := func(cur Rival, ok bool, cand Rival) bool { // more kills wins, then the name (so the result does not depend on row order)
		return !ok || cand.N > cur.N || (cand.N == cur.N && cand.Name < cur.Name)
	}
	for rows.Next() {
		var ks, vs, n int
		var kn, vn string
		if err := rows.Scan(&ks, &kn, &vs, &vn, &n); err != nil {
			return err
		}
		if ki, ok := idx[who{ks, kn}]; ok {
			cur, has := favorite[ki]
			if c := (Rival{Name: vn, N: n}); better(cur, has, c) {
				favorite[ki] = c
			}
		}
		if vi, ok := idx[who{vs, vn}]; ok {
			cur, has := nemesis[vi]
			if c := (Rival{Name: kn, N: n}); better(cur, has, c) {
				nemesis[vi] = c
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range players {
		if r, ok := favorite[i]; ok {
			c := r
			players[i].FavoriteVictim = &c
		}
		if r, ok := nemesis[i]; ok {
			c := r
			players[i].Nemesis = &c
		}
	}
	return nil
}

// loadObjectives counts each player's objective events (plants, defuses, captures ...) from the stored event feed.  Players are
// matched by name, which is how the feed names them; a game without a feed leaves the counts empty.
func (s *Store) loadObjectives(ctx context.Context, gid int64, players []PlayerStats) error {
	rows, err := s.pool.Query(ctx, `
		SELECT actor_name, type, count(*) FROM game_events
		WHERE game_id = $1 AND type IN ('plant','defuse','capture','steal','return','denial') AND actor_name <> ''
		GROUP BY actor_name, type`, gid)
	if err != nil {
		return err
	}
	defer rows.Close()
	byName := map[string]int{}
	for i := len(players) - 1; i >= 0; i-- { // the first player of a name wins, should two share one
		byName[players[i].Name] = i
	}
	for rows.Next() {
		var name, typ string
		var n int
		if err := rows.Scan(&name, &typ, &n); err != nil {
			return err
		}
		i, ok := byName[name]
		if !ok {
			continue
		}
		if players[i].Objectives == nil {
			players[i].Objectives = &ObjectiveCounts{}
		}
		o := players[i].Objectives
		switch typ {
		case "plant":
			o.Plants += n
		case "defuse":
			o.Defuses += n
		case "capture":
			o.Captures += n
		case "steal":
			o.Steals += n
		case "return":
			o.Returns += n
		case "denial":
			o.Denials += n
		}
	}
	return rows.Err()
}

// loadWinners fills in who won each round and the game.  The game's winner is the last round that has one: in a stopwatch game the
// second round settles it, otherwise there is only one.
func (s *Store) loadWinners(ctx context.Context, gid int64, g *GameStats) error {
	rows, err := s.pool.Query(ctx, `SELECT round, winner FROM game_rounds WHERE game_id = $1 ORDER BY round`, gid)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r RoundWinner
		if err := rows.Scan(&r.Round, &r.Winner); err != nil {
			return err
		}
		g.RoundWinners = append(g.RoundWinners, r)
		g.Winner = r.Winner
	}
	return rows.Err()
}
