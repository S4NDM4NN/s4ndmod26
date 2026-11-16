package stats

import (
	"context"
	"time"
)

// WeekStats is what a player (all the keys of one account) did in one stretch of time, for the weekly email.
type WeekStats struct {
	Games       int          `json:"games"`
	Wins        int          `json:"wins"`
	Losses      int          `json:"losses"`
	Draws       int          `json:"draws"` // a game with no winner; a game left early counts as played but neither won nor lost
	Kills       int          `json:"kills"`
	Deaths      int          `json:"deaths"`
	Headshots   int          `json:"headshots"`
	Revives     int          `json:"revives"`
	DamageGiven int          `json:"damage_given"`
	PlayMs      int          `json:"play_ms"`
	BestStreak  int          `json:"best_streak"` // most kills in a row without dying, in any one game
	Longest     *LongestKill `json:"longest_kill,omitempty"`

	TopClass       int    `json:"top_class"` // the class played longest (see ClassName); -1 when none
	TopClassMs     int    `json:"top_class_ms"`
	TopWeapon      string `json:"top_weapon,omitempty"` // the weapon with the most kills
	TopWeaponKills int    `json:"top_weapon_kills"`
	TopWeaponHits  int    `json:"top_weapon_hits"`
	TopWeaponAtts  int    `json:"top_weapon_atts"` // shots fired with it
}

// minAccuracyShots is how many shots a weapon needs before its accuracy is worth showing.
const minAccuracyShots = 5

// TopWeaponAccuracy is the share of the top weapon's shots that hit, in percent; false when there were too few shots to mean anything
// (a knife or a grenade has no shots to speak of).
func (w WeekStats) TopWeaponAccuracy() (float64, bool) {
	if w.TopWeaponAtts < minAccuracyShots {
		return 0, false
	}
	return 100 * float64(w.TopWeaponHits) / float64(w.TopWeaponAtts), true
}

// ClassName is the name of a player class number (0 soldier, 1 medic, 2 engineer, 3 lieutenant).
func ClassName(c int) string {
	switch c {
	case 0:
		return "Soldier"
	case 1:
		return "Medic"
	case 2:
		return "Engineer"
	case 3:
		return "Lieutenant"
	}
	return ""
}

// WeeklySummary adds up the games of the given keys that started in [from, to).  A game counts for the week it started in.  Bots have no
// key, so they never appear here.
func (s *Store) WeeklySummary(ctx context.Context, pids []string, from, to time.Time) (WeekStats, error) {
	w := WeekStats{TopClass: -1}
	if len(pids) == 0 {
		return w, nil
	}
	rows, _, err := s.playerGames(ctx, pids)
	if err != nil {
		return w, err
	}
	for _, r := range rows {
		if r.StartedAt == nil || r.StartedAt.Before(from) || !r.StartedAt.Before(to) {
			continue
		}
		w.Games++
		switch r.Result {
		case "win":
			w.Wins++
		case "loss":
			w.Losses++
		case "draw":
			w.Draws++
		}
		w.Kills += r.Kills
		w.Deaths += r.Deaths
		w.Revives += r.revives
		w.DamageGiven += r.dmgGiven
		w.PlayMs += r.PlayMs
		if r.KillStrk > w.BestStreak {
			w.BestStreak = r.KillStrk
		}
		if r.longest.Dist > 0 && (w.Longest == nil || r.longest.Dist > w.Longest.Dist) {
			l := r.longest
			w.Longest = &l
		}
	}
	if w.Games == 0 {
		return w, nil
	}
	// the class played longest
	err = s.pool.QueryRow(ctx, `
		SELECT p.class, sum(p.play_ms)::int FROM game_players p JOIN games g ON g.id = p.game_id
		WHERE p.pid = ANY($1) AND g.started_at >= $2 AND g.started_at < $3
		GROUP BY p.class ORDER BY sum(p.play_ms) DESC, p.class LIMIT 1`, pids, from, to).Scan(&w.TopClass, &w.TopClassMs)
	if err != nil && err.Error() != "no rows in result set" {
		return w, err
	}
	// headshots, and the weapon with the most kills
	wr, err := s.pool.Query(ctx, `
		SELECT w.weapon, sum(w.kills)::int, sum(w.headshots)::int, sum(w.hits)::int, sum(w.atts)::int
		FROM game_player_weapons w JOIN game_players p ON p.id = w.player_id JOIN games g ON g.id = p.game_id
		WHERE p.pid = ANY($1) AND g.started_at >= $2 AND g.started_at < $3
		GROUP BY w.weapon`, pids, from, to)
	if err != nil {
		return w, err
	}
	defer wr.Close()
	for wr.Next() {
		var name string
		var kills, hs, hits, atts int
		if err := wr.Scan(&name, &kills, &hs, &hits, &atts); err != nil {
			return w, err
		}
		w.Headshots += hs
		if kills > w.TopWeaponKills || (kills == w.TopWeaponKills && kills > 0 && name < w.TopWeapon) {
			w.TopWeapon, w.TopWeaponKills, w.TopWeaponHits, w.TopWeaponAtts = name, kills, hits, atts
		}
	}
	return w, wr.Err()
}
