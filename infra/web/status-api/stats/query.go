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
	LongestKill    *LongestKill     `json:"longest_kill,omitempty"`    // farthest hitscan kill of the game
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
	Rounds       int           `json:"rounds"`           // rounds played so far (a stopwatch game has two)
	Round        int           `json:"round,omitempty"`  // the one round these numbers are for; 0 when they add every round together
	Winner       string        `json:"winner,omitempty"` // the winner of the round shown (the last round's when every round is added), when the server named one
	RoundWinners []RoundWinner `json:"round_winners,omitempty"`
	Players      []PlayerStats `json:"players"`
	Awards       []Award       `json:"awards"`
	Shame        []Award       `json:"shame"`
	TopShots     []TopShot     `json:"topshots"`
	BottomShots  []TopShot     `json:"bottomshots"`
}

// ErrNoGame is returned for a match nothing has been ingested for.
var ErrNoGame = errors.New("no stats for that game")

// Game loads one game's stats with every round added together.
func (s *Store) Game(ctx context.Context, match string) (*GameStats, error) {
	return s.GameRound(ctx, match, 0)
}

// GameRound loads one game's stats for a single round, so a stopwatch game's two rounds (the teams swap sides between them) are
// not mixed.  round 0 adds every round together; a negative round means the first one played.
func (s *Store) GameRound(ctx context.Context, match string, round int) (*GameStats, error) {
	g := &GameStats{Match: match}
	var gid int64
	err := s.pool.QueryRow(ctx, `SELECT id, map, started_at FROM games WHERE match = $1`, match).Scan(&gid, &g.Map, &g.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoGame
	}
	if err != nil {
		return nil, err
	}
	var first, last int
	if err := s.pool.QueryRow(ctx, `SELECT coalesce(min(round), 1), coalesce(max(round), 1) FROM game_players WHERE game_id = $1`, gid).Scan(&first, &last); err != nil {
		return nil, err
	}
	g.Rounds = last
	if round < 0 {
		round = first
	}
	if round > last {
		round = last
	}
	if round > 0 {
		g.Round = round
	}

	rows, err := s.pool.Query(ctx, `
		SELECT slot, name, bot,
		       (array_agg(name_raw ORDER BY round DESC, id DESC))[1],
		       (array_agg(team  ORDER BY round DESC, id DESC))[1],
		       (array_agg(class ORDER BY round DESC, id DESC))[1],
		       count(DISTINCT round),
		       sum(kills), sum(deaths), sum(suicides), sum(teamkills), sum(gibs), sum(revives),
		       sum(damage_given), sum(damage_received), sum(team_damage), sum(score),
		       sum(team_gibs), sum(goombas), sum(poisons), sum(knife_kills), max(kill_streak), max(death_streak),
		       sum(double_kills), sum(triple_kills), sum(quad_kills), sum(first_blood), sum(first_death), sum(play_ms),
		       max(longest_kill),
		       (array_agg(longest_kill_weapon ORDER BY longest_kill DESC, id))[1],
		       (array_agg(longest_kill_victim ORDER BY longest_kill DESC, id))[1]
		FROM game_players WHERE game_id = $1 AND ($2 = 0 OR round = $2)
		GROUP BY slot, name, bot`, gid, round)
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
		var lk LongestKill
		if err := rows.Scan(&p.Slot, &p.Name, &p.Bot, &p.NameRaw, &p.Team, &p.Class, &p.Rounds,
			&p.Kills, &p.Deaths, &p.Suicides, &p.TeamKills, &p.Gibs, &p.Revives,
			&p.DamageGiven, &p.DamageReceived, &p.TeamDamage, &p.Score,
			&p.TeamGibs, &p.Goombas, &p.Poisons, &p.KnifeKills, &p.KillStreak, &p.DeathStreak,
			&p.DoubleKills, &p.TripleKills, &p.QuadKills, &p.FirstBlood, &p.FirstDeath, &p.PlayMs,
			&lk.Dist, &lk.Weapon, &lk.Victim); err != nil {
			rows.Close()
			return nil, err
		}
		if lk.Dist > 0 {
			p.LongestKill = &lk
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
		WHERE p.game_id = $1 AND ($2 = 0 OR p.round = $2)
		GROUP BY p.slot, p.name, p.bot, w.weapon`, gid, round)
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

	if err := s.loadRivals(ctx, gid, g.Round, g.Players); err != nil {
		return nil, err
	}
	if err := s.loadObjectives(ctx, gid, g.Round, g.Players); err != nil {
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
func (s *Store) loadRivals(ctx context.Context, gid int64, round int, players []PlayerStats) error {
	rows, err := s.pool.Query(ctx, `
		SELECT p.slot, p.name, v.victim_slot, v.victim_name, sum(v.kills)
		FROM game_player_victims v JOIN game_players p ON p.id = v.player_id
		WHERE p.game_id = $1 AND ($2 = 0 OR p.round = $2)
		GROUP BY p.slot, p.name, v.victim_slot, v.victim_name`, gid, round)
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

// roundWindow is the stretch of the recording (ms) a round took up, for counting the feed's events per round: each round ends with a
// match-end event, so round n runs from the previous one to its own.  ok is false when the feed cannot tell (a round whose start was
// never recorded), and then nothing is counted rather than another round's events.  Round 0 is the whole recording.
func (s *Store) roundWindow(ctx context.Context, gid int64, round int) (from, to int, ok bool, err error) {
	const never = 1 << 30
	if round <= 0 {
		return 0, never, true, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT t_ms FROM game_events WHERE game_id = $1 AND type = 'end' ORDER BY t_ms`, gid)
	if err != nil {
		return 0, 0, false, err
	}
	defer rows.Close()
	var ends []int
	for rows.Next() {
		var t int
		if err := rows.Scan(&t); err != nil {
			return 0, 0, false, err
		}
		ends = append(ends, t)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, err
	}
	if round-2 >= len(ends) {
		return 0, 0, false, nil
	}
	from, to = 0, never
	if round >= 2 {
		from = ends[round-2]
	}
	if round-1 < len(ends) {
		to = ends[round-1] + 1
	}
	return from, to, true, nil
}

// loadObjectives counts each player's objective events (plants, defuses, captures ...) from the stored event feed.  Players are
// matched by name, which is how the feed names them; a game without a feed leaves the counts empty.
func (s *Store) loadObjectives(ctx context.Context, gid int64, round int, players []PlayerStats) error {
	from, to, ok, err := s.roundWindow(ctx, gid, round)
	if err != nil || !ok {
		return err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT actor_name, type, count(*) FROM game_events
		WHERE game_id = $1 AND type IN ('plant','defuse','capture','steal','return','denial') AND actor_name <> ''
		  AND t_ms >= $2 AND t_ms < $3
		GROUP BY actor_name, type`, gid, from, to)
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
		if g.Round == 0 || g.Round == r.Round {
			g.Winner = r.Winner
		}
	}
	return rows.Err()
}
